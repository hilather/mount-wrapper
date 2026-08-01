package cleaner

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
)

// HandleOverlay applies overlay_cleanup policy to overlayPath.
// Returns (action, destOrEmpty). dest is set only for quarantine.
//
// Path safety: when overlayPath is non-empty and exists, it must resolve under
// overlayDir; otherwise action is OverlayRefused and an error is returned.
func HandleOverlay(
	overlayPath string,
	archiveID string,
	overlayDir string,
	policy string,
	now time.Time,
) (action string, dest string, err error) {
	if overlayPath == "" {
		return OverlayNone, "", nil
	}
	info, statErr := os.Lstat(overlayPath)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return OverlayMissing, "", nil
		}
		return OverlayNone, "", fmt.Errorf("overlay stat %s: %w", overlayPath, statErr)
	}

	if !PathUnderRoot(overlayPath, overlayDir) {
		return OverlayRefused, "", fmt.Errorf(
			"overlay path %q is not under overlay_dir %q; refusing destructive action",
			overlayPath, overlayDir,
		)
	}

	if policy == "" {
		policy = config.OverlayCleanupQuarantine
	}

	switch policy {
	case config.OverlayCleanupRetain:
		return OverlayRetained, overlayPath, nil

	case config.OverlayCleanupDelete:
		if err := removePath(overlayPath, info); err != nil {
			return OverlayNone, "", fmt.Errorf("overlay delete %s: %w", overlayPath, err)
		}
		return OverlayDeleted, "", nil

	default: // quarantine
		if now.IsZero() {
			now = time.Now().UTC()
		}
		qroot := QuarantineDir(overlayDir)
		if err := os.MkdirAll(qroot, 0o755); err != nil {
			return OverlayNone, "", fmt.Errorf("create quarantine dir: %w", err)
		}
		if !PathUnderRoot(qroot, overlayDir) {
			return OverlayRefused, "", fmt.Errorf("quarantine dir %q escapes overlay_dir", qroot)
		}
		ts := now.UTC().Format("20060102T150405Z")
		destPath := filepath.Join(qroot, fmt.Sprintf("%s-%s", archiveID, ts))
		n := 0
		for {
			if _, err := os.Lstat(destPath); err != nil {
				if os.IsNotExist(err) {
					break
				}
				return OverlayNone, "", fmt.Errorf("quarantine dest stat: %w", err)
			}
			n++
			destPath = filepath.Join(qroot, fmt.Sprintf("%s-%s-%d", archiveID, ts, n))
		}
		if err := movePath(overlayPath, destPath); err != nil {
			return OverlayNone, "", fmt.Errorf("quarantine move %s → %s: %w", overlayPath, destPath, err)
		}
		slog.Info("quarantined overlay", "archive_id", archiveID, "dest", destPath)
		return OverlayQuarantined, destPath, nil
	}
}

func removePath(path string, info os.FileInfo) error {
	if info.IsDir() {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

// movePath moves src to dest, falling back to copy+remove on cross-device rename.
func movePath(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := copyDir(src, dest); err != nil {
			_ = os.RemoveAll(dest)
			return err
		}
		return os.RemoveAll(src)
	}
	if err := copyFile(src, dest, info.Mode()); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return os.Remove(src)
}

func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func copyDir(src, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dest, e.Name())
		info, err := e.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}
