package archives

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/state"
)

// freeBytes probes free space under path. Tests may replace via SetFreeBytesFunc.
var freeBytes = diskFreeBytes

// SetFreeBytesFunc replaces the free-space probe used by CheckRelocateSpace.
// Returns a restore function. For tests only.
func SetFreeBytesFunc(fn func(path string) (free int64, ok bool)) (restore func()) {
	prev := freeBytes
	if fn == nil {
		freeBytes = diskFreeBytes
	} else {
		freeBytes = fn
	}
	return func() { freeBytes = prev }
}

// ArchiveFilePath returns the permanent Linux path for rec under archives_dir:
// `{archives_dir}/{basename}`. When that name is already taken by a different
// file, appends `--{archive_id[:8]}` before the extension.
//
// source, when non-empty, is used instead of rec.ArchivePath for same-file checks.
func ArchiveFilePath(cfg *config.Config, rec *state.ArchiveRecord, source string) (string, error) {
	if rec == nil {
		return "", relocateErrorf("archive record is nil")
	}
	root := ArchivesDirPath(cfg)
	if root == "" {
		return "", relocateErrorf("archives_dir is not configured")
	}

	basename := filepath.Base(strings.TrimSpace(rec.ArchiveBasename))
	if basename == "" || basename == "." || basename == ".." {
		id := rec.ArchiveID
		if len(id) > 8 {
			id = id[:8]
		}
		basename = "archive-" + id + ".bin"
	}

	src := source
	if src == "" {
		src = rec.ArchivePath
	}

	primary := filepath.Join(root, basename)
	st, err := os.Stat(primary)
	if err != nil || !st.Mode().IsRegular() {
		// Missing or non-file (parity: not Path.is_file()) → use basename.
		return primary, nil
	}

	srcInfo, srcErr := os.Stat(src)
	if srcErr != nil || !srcInfo.Mode().IsRegular() {
		// Source not a file: keep primary (parity with archives.py).
		return primary, nil
	}
	if os.SameFile(st, srcInfo) || samePath(primary, src) {
		return primary, nil
	}

	return disambiguatedPath(root, basename, rec.ArchiveID), nil
}

func disambiguatedPath(root, basename, archiveID string) string {
	ext := filepath.Ext(basename)
	stem := strings.TrimSuffix(basename, ext)
	id := archiveID
	if len(id) > 8 {
		id = id[:8]
	}
	return filepath.Join(root, stem+"--"+id+ext)
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

// ShouldRelocate reports whether rec should be moved into archives_dir before
// first index (parity with tarmount-wsl should_relocate).
func ShouldRelocate(cfg *config.Config, rec *state.ArchiveRecord) bool {
	if cfg == nil || rec == nil {
		return false
	}
	if !cfg.MoveArchivesToLinux {
		return false
	}
	if ArchivesDirPath(cfg) == "" {
		return false
	}
	if IsArchivesPath(cfg, rec.ArchivePath) {
		return false
	}
	dest, err := ArchiveFilePath(cfg, rec, "")
	if err != nil {
		return false
	}
	// If the computed destination already exists as a regular file, do not
	// relocate (either already in place, or unresolvable name collision).
	st, err := os.Stat(dest)
	if err != nil {
		return true
	}
	if !st.Mode().IsRegular() {
		// Python Path.is_file() is false for dirs/specials → still relocate.
		return true
	}
	return false
}

// CheckRelocateSpace ensures archives_dir has room for archiveBytes plus
// min_free_bytes and archive_relocate_overhead_bytes. Creates archives_dir
// when missing. When free space cannot be determined, logs a warning and
// allows the relocate (parity with Python).
func CheckRelocateSpace(cfg *config.Config, archiveBytes int64) error {
	root := ArchivesDirPath(cfg)
	if root == "" {
		return relocateErrorf("archives_dir is not configured")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return relocateErrorf("cannot create archives_dir: %v", err)
	}
	free, ok := freeBytes(root)
	if !ok {
		slog.Warn("could not determine free space for archives_dir", "archives_dir", root)
		return nil
	}
	var minFree, overhead int64
	if cfg != nil {
		minFree = int64(cfg.MinFreeBytes)
		overhead = int64(cfg.ArchiveRelocateOverheadBytes)
	}
	required := archiveBytes + minFree + overhead
	if free < required {
		return relocateErrorf("insufficient_space_for_relocate: need %d bytes, have %d", required, free)
	}
	return nil
}

// RelocateArchive moves source (default: rec.ArchivePath) into archives_dir.
// Also moves a convert metadata sidecar when present next to the source.
// Returns the destination path.
func RelocateArchive(cfg *config.Config, rec *state.ArchiveRecord, source string) (string, error) {
	if rec == nil {
		return "", relocateErrorf("archive record is nil")
	}
	src := source
	if src == "" {
		src = rec.ArchivePath
	}
	srcInfo, err := os.Stat(src)
	if err != nil || !srcInfo.Mode().IsRegular() {
		return "", relocateErrorf("archive not found: %s", src)
	}

	dest, err := ArchiveFilePath(cfg, rec, src)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", relocateErrorf("cannot create archives_dir: %v", err)
	}

	if destInfo, err := os.Stat(dest); err == nil && destInfo.Mode().IsRegular() {
		if os.SameFile(destInfo, srcInfo) || samePath(dest, src) {
			return dest, nil
		}
		return "", relocateErrorf("archives destination already exists: %s", dest)
	}

	if err := CheckRelocateSpace(cfg, srcInfo.Size()); err != nil {
		return "", err
	}

	slog.Info("archive relocate start",
		"event", "archive_relocate_start",
		"archive_id", rec.ArchiveID,
		"src", src,
		"dest", dest,
		"size", srcInfo.Size(),
	)

	if err := moveFile(src, dest); err != nil {
		if _, stErr := os.Stat(dest); stErr == nil {
			_ = os.Remove(dest)
		}
		return "", relocateErrorf("archive relocate failed: %v", err)
	}

	// Move convert metadata sidecar when present.
	sidecarSrc := convert.MetadataPath(src)
	if st, err := os.Stat(sidecarSrc); err == nil && st.Mode().IsRegular() {
		sidecarDest := convert.MetadataPath(dest)
		if _, err := os.Stat(sidecarDest); err == nil {
			_ = os.Remove(sidecarDest)
		}
		if err := moveFile(sidecarSrc, sidecarDest); err != nil {
			slog.Warn("archive relocate sidecar failed",
				"event", "archive_relocate_sidecar_failed",
				"archive_id", rec.ArchiveID,
				"src", sidecarSrc,
				"dest", sidecarDest,
				"err", err,
			)
		}
	}

	destInfo, err := os.Stat(dest)
	size := int64(0)
	if err == nil {
		size = destInfo.Size()
	}
	slog.Info("archive relocate done",
		"event", "archive_relocate_done",
		"archive_id", rec.ArchiveID,
		"path", dest,
		"size", size,
	)
	return dest, nil
}

// RemoveSupersededSource deletes originalPath after convert/relocate when it is
// no longer the active copy. Also removes a convert metadata sidecar if present.
// Returns true when the original was removed.
func RemoveSupersededSource(cfg *config.Config, originalPath, activePath, archiveID string) bool {
	if cfg == nil || !cfg.MoveArchivesToLinux {
		return false
	}
	if originalPath == "" || activePath == "" {
		return false
	}

	origInfo, err := os.Stat(originalPath)
	if err != nil || !origInfo.Mode().IsRegular() {
		return false
	}

	if actInfo, err := os.Stat(activePath); err == nil && actInfo.Mode().IsRegular() {
		if os.SameFile(origInfo, actInfo) {
			return false
		}
	}
	if samePath(originalPath, activePath) {
		return false
	}

	if IsArchivesPath(cfg, originalPath) || IsConvertedOutputPath(cfg, originalPath) {
		return false
	}

	if err := os.Remove(originalPath); err != nil {
		slog.Warn("archive source remove failed",
			"event", "archive_source_remove_failed",
			"archive_id", archiveID,
			"path", originalPath,
			"err", err,
		)
		return false
	}

	sidecar := convert.MetadataPath(originalPath)
	if st, err := os.Stat(sidecar); err == nil && st.Mode().IsRegular() {
		_ = os.Remove(sidecar)
	}

	slog.Info("archive source removed",
		"event", "archive_source_removed",
		"archive_id", archiveID,
		"path", originalPath,
		"active", activePath,
	)
	return true
}

// moveFile renames src to dest, falling back to copy+remove (cross-device /
// EXDEV and other rename failures, parity with shutil.move).
func moveFile(src, dest string) error {
	err := os.Rename(src, dest)
	if err == nil {
		return nil
	}
	if copyErr := copyFileContents(src, dest); copyErr != nil {
		// Prefer the original rename error for cross-device LinkError cases.
		var linkErr *os.LinkError
		if errors.As(err, &linkErr) {
			return err
		}
		return copyErr
	}
	if rmErr := os.Remove(src); rmErr != nil {
		_ = os.Remove(dest)
		return rmErr
	}
	return nil
}

func copyFileContents(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	st, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
