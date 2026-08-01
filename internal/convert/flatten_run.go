package convert

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MethodFlattenCLI is the convert metadata method for the 7z CLI flatten path.
const MethodFlattenCLI = "flatten-cli"

// FlattenPartialPath returns the partial output path for in-place flatten
// (parity: source.with_suffix(suffix + ".flatten.partial") → a.7z.flatten.partial).
func FlattenPartialPath(source string) string {
	return source + ".flatten.partial"
}

// FlattenWorkDir returns the extract work directory next to dest partial.
func FlattenWorkDir(dest string) string {
	return dest + ".work"
}

// EstimateFlattenPeakDiskBytes returns a conservative peak-disk estimate without
// a full 7z structure parse: source + extracted (~source) + output (~source).
func EstimateFlattenPeakDiskBytes(sourceSize int64) int64 {
	if sourceSize < 0 {
		sourceSize = 0
	}
	return sourceSize + sourceSize + sourceSize
}

// FlattenSpaceRequired returns peak + overhead + minFree.
func FlattenSpaceRequired(peakBytes, overhead, minFree int64) int64 {
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

// CheckFlattenSpace ensures destDir has room for flatten extract+repack.
// When free space cannot be determined, logs a warning and allows the convert.
func CheckFlattenSpace(destDir string, peakBytes, overhead, minFree int64) error {
	if destDir == "" {
		return convertErrorf("check_flatten_space", "empty dest dir")
	}
	free, ok := freeBytes(destDir)
	if !ok {
		// Match zip repack / Python: allow when unreadable.
		return nil
	}
	required := FlattenSpaceRequired(peakBytes, overhead, minFree)
	if free < required {
		return convertErrorf("check_flatten_space",
			"insufficient disk space for flatten: need %d bytes, have %d bytes free on %s",
			required, free, destDir)
	}
	return nil
}

// FlattenMinOKSize returns the minimum acceptable flatten output size
// (parity with nonsolid_convert._validate_flatten_output size floor only).
func FlattenMinOKSize(sourceSize int64) int64 {
	const hundredMiB = 100 * 1024 * 1024
	if sourceSize >= hundredMiB {
		minOK := sourceSize / 20
		const floor = 64 * 1024 * 1024
		if minOK < floor {
			return floor
		}
		return minOK
	}
	minOK := sourceSize / 2
	if minOK < 200 {
		return 200
	}
	return minOK
}

// BuildFlattenExtractCmd builds `7z x -y -o{workDir}/ [excludes…] {source}` argv.
func BuildFlattenExtractCmd(sevenZipBin, source, workDir string, excludePatterns []string) []string {
	bin := strings.TrimSpace(sevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	cmd := []string{bin, "x", "-y", "-o" + workDir + string(filepath.Separator)}
	cmd = append(cmd, SevenZipExcludeArgs(excludePatterns)...)
	cmd = append(cmd, source)
	return cmd
}

// BuildFlattenCreateCmd builds `7z a -t7z -ms=off -y {dest} *` argv.
// Call with cwd set to the extract work directory.
func BuildFlattenCreateCmd(sevenZipBin, dest string) []string {
	bin := strings.TrimSpace(sevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	return []string{bin, "a", "-t7z", "-ms=off", "-y", dest, "*"}
}

// BuildFlattenTestCmd builds `7z t -y {archive}` argv.
func BuildFlattenTestCmd(sevenZipBin, archive string) []string {
	bin := strings.TrimSpace(sevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	return []string{bin, "t", "-y", archive}
}

// StripInnerNamePrefix strips stripPrefix from the start of name when present
// (parity with nonsolid_convert._strip_inner_name_prefix).
func StripInnerNamePrefix(name, stripPrefix string) string {
	normalized := strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")
	prefix := strings.TrimSpace(stripPrefix)
	if prefix != "" && strings.HasPrefix(normalized, prefix) {
		return normalized[len(prefix):]
	}
	return normalized
}

// NestedFlattenPrefix returns the extract directory name for a nested 7z member
// path (parity with _nested_flatten_prefix using the member relative path).
func NestedFlattenPrefix(memberRel, stripPrefix string) string {
	arcname := strings.TrimLeft(strings.ReplaceAll(memberRel, "\\", "/"), "/")
	if strings.HasSuffix(strings.ToLower(arcname), ".7z") {
		arcname = arcname[:len(arcname)-3]
	}
	return StripInnerNamePrefix(arcname, stripPrefix)
}

// RunFlattenConvert flattens archivePath in place when needsFlatten reports true,
// writes convert metadata, and returns it. When needsFlatten is nil or returns
// false, returns existing metadata (may be nil) without error.
//
// Best-effort CLI path (parity with cli_flatten_7z_nonsolid + convert_7z_to_flattened_inplace):
//  1. free-space gate (conservative 3× source estimate)
//  2. 7z extract outer archive
//  3. walk for nested *.7z, test+extract each, remove nested archives
//  4. 7z a -t7z -ms=off rebuild
//  5. size-floor validation; replace source
//
// Residual gaps vs ratarmountcore:
//   - no solid/folder structure parse; needsFlatten must be injected
//   - nested discovery is post-extract filesystem walk (not 7z header listing)
//   - no stream-flatten fallback / encrypted-folder detection
//   - no post-rebuild nested-7z header validation (would need a 7z parser)
func RunFlattenConvert(archivePath string, p NonsolidFlattenParams, needsFlatten FlattenNeededFunc) (*ConvertMetadata, error) {
	archivePath = filepath.Clean(archivePath)
	st, err := os.Stat(archivePath)
	if err != nil || !st.Mode().IsRegular() {
		return nil, convertErrorf("flatten", "archive not found: %s", archivePath)
	}
	if !IsSevenzPath(archivePath) {
		return nil, convertErrorf("flatten", "not a 7z archive: %s", archivePath)
	}

	existing := ReadConvertMetadata(archivePath)
	if needsFlatten == nil || !needsFlatten(archivePath) {
		return existing, nil
	}

	sourceSize := st.Size()
	started := time.Now()
	if err := flatten7zInplace(archivePath, p); err != nil {
		return nil, err
	}
	dstSt, err := os.Stat(archivePath)
	if err != nil {
		return nil, convertErrorf("flatten", "stat after convert: %v", err)
	}
	dur := time.Since(started).Seconds()
	if dur < 0 {
		dur = 0
	}
	d := float64(int(dur*1000)) / 1000
	meta := BuildConvertMetadata(sourceSize, dstSt.Size(), MethodFlattenCLI, &d)
	if _, err := WriteConvertMetadata(archivePath, meta); err != nil {
		return &meta, err
	}
	return &meta, nil
}

func flatten7zInplace(source string, p NonsolidFlattenParams) error {
	bin := strings.TrimSpace(p.SevenZipBin)
	if bin == "" {
		bin = "7z"
	}
	run := run7zOf(p.Run7z)

	partial := FlattenPartialPath(source)
	workDir := FlattenWorkDir(partial)
	_ = os.Remove(partial)
	_ = os.RemoveAll(workDir)

	srcSt, err := os.Stat(source)
	if err != nil {
		return convertErrorf("flatten", "stat source: %v", err)
	}
	peak := EstimateFlattenPeakDiskBytes(srcSt.Size())
	if err := CheckFlattenSpace(filepath.Dir(source), peak, int64(p.OverheadBytes), int64(p.MinFreeBytes)); err != nil {
		return err
	}

	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return convertErrorf("flatten", "create work dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

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

	extractArgv := BuildFlattenExtractCmd(bin, srcAbs, workAbs, p.ExcludePatterns)
	if err := run(extractArgv[0], extractArgv[1:], ""); err != nil {
		return err
	}

	if err := expandNestedSevenZ(workAbs, bin, p.InnerPrefixStrip, p.ExcludePatterns, run); err != nil {
		_ = os.Remove(partial)
		return err
	}

	createArgv := BuildFlattenCreateCmd(bin, partialAbs)
	if err := run(createArgv[0], createArgv[1:], workAbs); err != nil {
		_ = os.Remove(partial)
		return err
	}

	pst, err := os.Stat(partial)
	if err != nil || !pst.Mode().IsRegular() {
		_ = os.Remove(partial)
		return convertErrorf("flatten", "flatten produced no output: %s", partial)
	}
	minOK := FlattenMinOKSize(srcSt.Size())
	if pst.Size() < minOK {
		_ = os.Remove(partial)
		return convertErrorf("flatten",
			"flatten output too small: %d bytes (source=%d, minimum=%d)",
			pst.Size(), srcSt.Size(), minOK)
	}

	if err := os.Remove(source); err != nil {
		_ = os.Remove(partial)
		return convertErrorf("flatten", "remove source: %v", err)
	}
	if err := os.Rename(partial, source); err != nil {
		return convertErrorf("flatten", "rename partial onto source: %v", err)
	}
	return nil
}

// expandNestedSevenZ finds *.7z under workDir (filesystem walk), extracts each
// into a prefix directory, and removes the nested archive. Corrupt nested
// archives are skipped (best-effort; parity with Python skip-on-test-fail).
func expandNestedSevenZ(workDir, bin, stripPrefix string, excludes []string, run Run7zFunc) error {
	var nested []string
	err := filepath.Walk(workDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".7z") {
			nested = append(nested, path)
		}
		return nil
	})
	if err != nil {
		return convertErrorf("flatten", "walk work dir: %v", err)
	}

	// Deepest paths first so nested-of-nested expand before parents are removed.
	for i := 0; i < len(nested); i++ {
		for j := i + 1; j < len(nested); j++ {
			if len(nested[j]) > len(nested[i]) {
				nested[i], nested[j] = nested[j], nested[i]
			}
		}
	}

	for _, nestedPath := range nested {
		// May already be removed by a deeper expand.
		if st, err := os.Stat(nestedPath); err != nil || !st.Mode().IsRegular() {
			continue
		}
		rel, err := filepath.Rel(workDir, nestedPath)
		if err != nil {
			rel = filepath.Base(nestedPath)
		}
		// 7z t — skip corrupt
		testArgv := BuildFlattenTestCmd(bin, nestedPath)
		if err := run(testArgv[0], testArgv[1:], ""); err != nil {
			_ = os.Remove(nestedPath)
			continue
		}
		outRel := NestedFlattenPrefix(rel, stripPrefix)
		outDir := filepath.Join(workDir, filepath.FromSlash(outRel))
		// Refuse path escape outside the work tree.
		if relCheck, err := filepath.Rel(workDir, outDir); err != nil ||
			relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
			_ = os.Remove(nestedPath)
			continue
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return convertErrorf("flatten", "mkdir nested out: %v", err)
		}
		outAbs, err := filepath.Abs(outDir)
		if err != nil {
			outAbs = outDir
		}
		nestedAbs, err := filepath.Abs(nestedPath)
		if err != nil {
			nestedAbs = nestedPath
		}
		extractArgv := BuildFlattenExtractCmd(bin, nestedAbs, outAbs, excludes)
		if err := run(extractArgv[0], extractArgv[1:], ""); err != nil {
			_ = os.RemoveAll(outDir)
			_ = os.Remove(nestedPath)
			continue
		}
		_ = os.Remove(nestedPath)
	}
	return nil
}
