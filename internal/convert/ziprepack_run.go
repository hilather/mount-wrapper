package convert

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hilather/mount-wrapper/internal/config"
)

// ZipRepackParams holds knobs for the zip→stored-7z repack runner.
type ZipRepackParams struct {
	SevenZipBin   string
	OverheadBytes int64
	MinFreeBytes  int64
	// KeepSource renames the .zip to *.pre-repack.bak when true (Python default).
	// When false, the source zip is removed after a successful repack.
	KeepSource bool
	// Run7z optional process runner; nil uses DefaultRun7z.
	Run7z Run7zFunc
}

// ZipRepackParamsFromConfig builds ZipRepackParams from cfg.
func ZipRepackParamsFromConfig(cfg *config.Config, opts ResolveOptions) ZipRepackParams {
	p := ZipRepackParams{
		SevenZipBin:   "7z",
		OverheadBytes: 64 * 1024 * 1024,
		KeepSource:    true,
	}
	if cfg != nil {
		p.SevenZipBin = EffectiveSevenZipBin(cfg, opts)
		p.OverheadBytes = int64(cfg.Convert7zOverheadBytes)
		p.MinFreeBytes = int64(cfg.MinFreeBytes)
	}
	return p
}

// RepackZipTo7zInplace extracts source (.zip) and rebuilds a stored non-solid
// .7z beside it. Returns the destination path and method string.
// Parity with zip_repack.repack_zip_to_7z_inplace.
func RepackZipTo7zInplace(source string, p ZipRepackParams) (dest string, method string, err error) {
	source = filepath.Clean(source)
	if !IsZipPath(source) {
		return "", "", convertErrorf("zip_repack", "not a zip archive: %s", source)
	}
	st, err := os.Stat(source)
	if err != nil || !st.Mode().IsRegular() {
		return "", "", convertErrorf("zip_repack", "not a zip archive: %s", source)
	}
	if !ZipHasEmbeddedArchives(source) {
		return "", "", convertErrorf("zip_repack", "zip has no embedded archive members: %s", source)
	}

	bin := strings.TrimSpace(p.SevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	run := run7zOf(p.Run7z)
	dest = ZipRepackDestPath(source)
	partial := ZipRepackPartialPath(dest)
	workDir := ZipRepackWorkDir(source)

	est, err := EstimateZipRepackPeakDiskBytes(source)
	if err != nil {
		return "", "", err
	}
	if err := CheckZipRepackSpace(filepath.Dir(dest), est.PeakBytes, p.OverheadBytes, p.MinFreeBytes); err != nil {
		return "", "", err
	}

	// Fresh work tree.
	_ = os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", "", convertErrorf("zip_repack", "create work dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	_ = os.Remove(partial)

	srcAbs, err := filepath.Abs(source)
	if err != nil {
		srcAbs = source
	}
	workAbs, err := filepath.Abs(workDir)
	if err != nil {
		workAbs = workDir
	}
	partialAbs, err := filepath.Abs(partial)
	if err != nil {
		partialAbs = partial
	}

	extractArgv := BuildZipExtractCmd(bin, srcAbs, workAbs)
	if err := run(extractArgv[0], extractArgv[1:], ""); err != nil {
		return "", "", err
	}

	createArgv := BuildZipCreate7zCmd(bin, partialAbs)
	if err := run(createArgv[0], createArgv[1:], workAbs); err != nil {
		_ = os.Remove(partial)
		return "", "", err
	}

	pst, err := os.Stat(partial)
	if err != nil || !pst.Mode().IsRegular() {
		_ = os.Remove(partial)
		return "", "", convertErrorf("zip_repack", "zip repack produced no output: %s", partial)
	}
	minOK := ZipRepackMinOKSize(st.Size())
	if pst.Size() < minOK {
		_ = os.Remove(partial)
		return "", "", convertErrorf("zip_repack",
			"zip repack output too small: %d bytes (minimum %d)", pst.Size(), minOK)
	}

	if p.KeepSource {
		backup := ZipRepackBackupPath(source)
		_ = os.Remove(backup)
		if err := os.Rename(source, backup); err != nil {
			_ = os.Remove(partial)
			return "", "", convertErrorf("zip_repack", "backup source: %v", err)
		}
	} else {
		if err := os.Remove(source); err != nil {
			_ = os.Remove(partial)
			return "", "", convertErrorf("zip_repack", "remove source: %v", err)
		}
	}

	if _, err := os.Stat(dest); err == nil {
		_ = os.Remove(dest)
	}
	if err := os.Rename(partial, dest); err != nil {
		return "", "", convertErrorf("zip_repack", "rename partial: %v", err)
	}
	return dest, MethodZipRepack, nil
}

// RunZipRepack repacks source (.zip) to .7z and writes convert metadata on the result.
// Parity with zip_repack.run_zip_repack_convert.
func RunZipRepack(source string, p ZipRepackParams) (string, ConvertMetadata, error) {
	source = filepath.Clean(source)
	st, err := os.Stat(source)
	if err != nil || !st.Mode().IsRegular() {
		return "", ConvertMetadata{}, convertErrorf("zip_repack", "archive not found: %s", source)
	}

	dest := ZipRepackDestPath(source)
	if existing := ReadConvertMetadata(dest); existing != nil {
		if dst, err := os.Stat(dest); err == nil && dst.Mode().IsRegular() {
			return dest, *existing, nil
		}
	}

	sourceSize := st.Size()
	started := time.Now()
	resultPath, method, err := RepackZipTo7zInplace(source, p)
	if err != nil {
		return "", ConvertMetadata{}, err
	}
	dstSt, err := os.Stat(resultPath)
	if err != nil {
		return "", ConvertMetadata{}, convertErrorf("zip_repack", "stat result: %v", err)
	}
	dur := time.Since(started).Seconds()
	if dur < 0 {
		dur = 0
	}
	d := float64(int(dur*1000)) / 1000
	meta := BuildConvertMetadata(sourceSize, dstSt.Size(), method, &d)
	if _, err := WriteConvertMetadata(resultPath, meta); err != nil {
		return resultPath, meta, err
	}
	return resultPath, meta, nil
}
