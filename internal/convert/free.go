package convert

import (
	"log"
	"os"
)

// freeBytes probes free space under path. Tests may replace via SetFreeBytesFunc.
var freeBytes = diskFreeBytes

// SetFreeBytesFunc replaces the free-space probe used by convert space gates.
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

// ConvertSpaceRequired returns bytes needed for archiveconverter convert:
// archiveBytes*2 + minFree + overhead (parity with converter.check_convert_space).
func ConvertSpaceRequired(archiveBytes, minFree, overhead int64) int64 {
	if archiveBytes < 0 {
		archiveBytes = 0
	}
	if minFree < 0 {
		minFree = 0
	}
	if overhead < 0 {
		overhead = 0
	}
	return archiveBytes*2 + minFree + overhead
}

// CheckConvertSpace ensures destDir has room for convert output + temp workspace.
// Creates destDir when missing. When free space cannot be determined, logs a
// warning and allows the convert (parity with Python).
func CheckConvertSpace(destDir string, archiveBytes, minFree, overhead int64) error {
	if destDir == "" {
		return convertErrorf("check_space", "empty convert dest dir")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return convertErrorf("check_space", "cannot create dest dir: %v", err)
	}
	free, ok := freeBytes(destDir)
	if !ok {
		log.Printf("could not determine free space for convert dest=%s", destDir)
		return nil
	}
	required := ConvertSpaceRequired(archiveBytes, minFree, overhead)
	if free < required {
		return convertErrorf("check_space",
			"insufficient_space_for_convert: need %d bytes, have %d", required, free)
	}
	return nil
}

// ZipRepackSpaceRequired returns peak + overhead + minFree
// (parity with zip_repack.require_zip_repack_disk_space).
func ZipRepackSpaceRequired(peakBytes, overhead, minFree int64) int64 {
	if peakBytes < 0 {
		peakBytes = 0
	}
	if overhead < 0 {
		overhead = 0
	}
	if minFree < 0 {
		minFree = 0
	}
	return peakBytes + overhead + minFree
}

// CheckZipRepackSpace ensures destDir has room for zip repack.
// When free space cannot be determined, logs a warning and allows the repack.
func CheckZipRepackSpace(destDir string, peakBytes, overhead, minFree int64) error {
	if destDir == "" {
		return convertErrorf("check_zip_space", "empty dest dir")
	}
	free, ok := freeBytes(destDir)
	if !ok {
		log.Printf("zip repack: could not check free space on %s", destDir)
		return nil
	}
	required := ZipRepackSpaceRequired(peakBytes, overhead, minFree)
	if free < required {
		return convertErrorf("check_zip_space",
			"insufficient disk space for zip repack: need %d bytes, have %d bytes free on %s",
			required, free, destDir)
	}
	return nil
}
