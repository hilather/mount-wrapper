package mounter

import (
	"strings"

	"github.com/hilather/mount-wrapper/internal/paths"
)

// IndexPathAllowed refuses index (and related on-disk engine) paths on DrvFs
// unless allowIndexesOnDrvfs is true.
//
// Call before writing indexes/overlays under a path derived from operator input.
// Config load already guards configured index_dir; this covers dynamic paths.
func IndexPathAllowed(indexPath string, allowIndexesOnDrvfs bool) error {
	if allowIndexesOnDrvfs {
		return nil
	}
	indexPath = strings.TrimSpace(indexPath)
	if indexPath == "" {
		return nil
	}
	if paths.IsDrvFsPath(indexPath) {
		return mounterErrorf(
			"index path %q appears to be on DrvFs (/mnt/<drive>). "+
				"Keep indexes on the Linux filesystem, or set allow_indexes_on_drvfs: true",
			indexPath,
		)
	}
	return nil
}

// EnginePathsAllowed checks index, overlay, and mount paths against DrvFs policy.
func EnginePathsAllowed(indexPath, overlayPath, mountPath string, allowIndexesOnDrvfs bool) error {
	if err := IndexPathAllowed(indexPath, allowIndexesOnDrvfs); err != nil {
		return err
	}
	if err := IndexPathAllowed(overlayPath, allowIndexesOnDrvfs); err != nil {
		// Rephrase overlay errors slightly for clarity.
		if !allowIndexesOnDrvfs && paths.IsDrvFsPath(overlayPath) {
			return mounterErrorf(
				"overlay path %q appears to be on DrvFs (/mnt/<drive>). "+
					"Keep overlays on the Linux filesystem, or set allow_indexes_on_drvfs: true",
				overlayPath,
			)
		}
		return err
	}
	if err := IndexPathAllowed(mountPath, allowIndexesOnDrvfs); err != nil {
		if !allowIndexesOnDrvfs && paths.IsDrvFsPath(mountPath) {
			return mounterErrorf(
				"mount path %q appears to be on DrvFs (/mnt/<drive>). "+
					"Keep mounts on the Linux filesystem, or set allow_indexes_on_drvfs: true",
				mountPath,
			)
		}
		return err
	}
	return nil
}
