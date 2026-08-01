package convert

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// EmbeddedArchiveSuffixes are basename suffixes treated as nested archives
// (aligned with ratarmount-rs automount /archive set; parity with zip_repack).
var EmbeddedArchiveSuffixes = []string{
	".tar.gz",
	".tgz",
	".tar.bz2",
	".tbz2",
	".tbz",
	".tar.xz",
	".txz",
	".tar.zst",
	".tar.zstd",
	".tzst",
	".tar",
	".zip",
	".jar",
	".7z",
	".rar",
	".cab",
	".ar",
	".a",
	".cpio",
	".sqlar",
	".squashfs",
	".asar",
	".xar",
	".warc",
}

// MethodZipRepack is the convert metadata method string for zip→7z repack.
const MethodZipRepack = "zip-repack-7z"

// IsZipPath reports whether path has a .zip suffix (case-insensitive).
func IsZipPath(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".zip")
}

// MemberLooksLikeEmbeddedArchive reports whether name basename matches a
// recursive-mount archive suffix (parity with member_looks_like_embedded_archive).
func MemberLooksLikeEmbeddedArchive(name string) bool {
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if base == "" || base == "." || base == ".." {
		return false
	}
	lower := strings.ToLower(base)
	for _, suffix := range EmbeddedArchiveSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// ZipHasEmbeddedArchives reports whether path is a zip containing at least one
// embedded archive member.
func ZipHasEmbeddedArchives(path string) bool {
	if !IsZipPath(path) {
		return false
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	r, err := zip.OpenReader(path)
	if err != nil {
		return false
	}
	defer r.Close()
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if MemberLooksLikeEmbeddedArchive(f.Name) {
			return true
		}
	}
	return false
}

// ZipRepackEstimate holds peak-disk estimate for zip→7z repack.
type ZipRepackEstimate struct {
	SourceBytes       int64
	UncompressedBytes int64
	PeakBytes         int64
}

// EstimateZipRepackPeakDiskBytes estimates peak bytes needed to extract and
// repack source as stored 7z (source + extracted + output ≈ source + 2×uncomp).
func EstimateZipRepackPeakDiskBytes(source string) (ZipRepackEstimate, error) {
	st, err := os.Stat(source)
	if err != nil {
		return ZipRepackEstimate{}, convertErrorf("zip_estimate", "%v", err)
	}
	r, err := zip.OpenReader(source)
	if err != nil {
		return ZipRepackEstimate{}, convertErrorf("zip_estimate", "%v", err)
	}
	defer r.Close()
	var uncompressed int64
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		uncompressed += int64(f.UncompressedSize64)
	}
	est := ZipRepackEstimate{
		SourceBytes:       st.Size(),
		UncompressedBytes: uncompressed,
		PeakBytes:         st.Size() + uncompressed + uncompressed,
	}
	return est, nil
}

// ShouldRepackZip reports whether a zip should be repacked to non-solid 7z.
// Parity with zip_repack.should_repack_zip_convert.
func ShouldRepackZip(cfg *config.Config, archivePath string) bool {
	if cfg == nil || !cfg.Convert7zNonsolid || !cfg.ConvertZipTo7z {
		return false
	}
	if !IsZipPath(archivePath) {
		return false
	}
	st, err := os.Stat(archivePath)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	dest := ZipRepackDestPath(archivePath)
	if HasConvertMetadata(dest) {
		return false
	}
	if st, err := os.Stat(dest); err == nil && st.Mode().IsRegular() {
		return false
	}
	return ZipHasEmbeddedArchives(archivePath)
}

// ZipRepackDestPath returns the .7z path beside a .zip source.
func ZipRepackDestPath(zipPath string) string {
	ext := filepath.Ext(zipPath)
	return strings.TrimSuffix(zipPath, ext) + ".7z"
}

// ZipRepackPartialPath returns the partial output path for atomic rename.
func ZipRepackPartialPath(dest7z string) string {
	return dest7z + ".partial"
}

// ZipRepackWorkDir returns the extract work directory next to the zip.
func ZipRepackWorkDir(zipPath string) string {
	return zipPath + ".repack.work"
}

// ZipRepackBackupPath returns the kept-source backup path.
func ZipRepackBackupPath(zipPath string) string {
	return zipPath + ".pre-repack.bak"
}

// BuildZipExtractCmd builds `7z x -y -o{workDir}/ {source}` argv.
func BuildZipExtractCmd(sevenZipBin, source, workDir string) []string {
	bin := strings.TrimSpace(sevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	// 7z requires -o without a space; trailing slash matches Python.
	return []string{bin, "x", "-y", "-o" + workDir + string(filepath.Separator), source}
}

// BuildZipCreate7zCmd builds `7z a -t7z -ms=off -mx=0 -y {partial} *` argv.
// Call with cwd set to the extract work directory.
func BuildZipCreate7zCmd(sevenZipBin, partialDest string) []string {
	bin := strings.TrimSpace(sevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	return []string{bin, "a", "-t7z", "-ms=off", "-mx=0", "-y", partialDest, "*"}
}

// ZipRepackMinOKSize returns the minimum acceptable partial output size
// (max(source/4, 1024)) — parity with repack_zip_to_7z_inplace.
func ZipRepackMinOKSize(sourceSize int64) int64 {
	minOK := sourceSize / 4
	if minOK < 1024 {
		return 1024
	}
	return minOK
}

// ShouldPreconvert reports whether archivePath needs convert/repack before mount.
// Combines flatten, zip-repack, and archiveconverter gates (parity with
// zip_repack.should_preconvert). needsFlatten is optional for flatten probe.
func ShouldPreconvert(cfg *config.Config, archivePath string, opts ResolveOptions, needsFlatten FlattenNeededFunc) bool {
	if ShouldFlattenConvert(cfg, archivePath, needsFlatten) {
		return true
	}
	if ShouldRepackZip(cfg, archivePath) {
		return true
	}
	if !ArchiveconverterAvailable(cfg, opts) {
		return false
	}
	if !IsSevenzPath(archivePath) {
		return false
	}
	if IsConvertedPath(cfg, archivePath) {
		return false
	}
	if HasConvertMetadata(archivePath) {
		return false
	}
	return true
}
