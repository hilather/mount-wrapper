package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Windows drive-letter form: D:\foo, D:/foo, D:, D:\
	winDrive = regexp.MustCompile(`^([A-Za-z]):[\\/]?(.*)$`)
	// \\wsl.localhost\Distro\... or \\wsl$\Distro\... (after \ → / normalize)
	uncWSL = regexp.MustCompile(`(?i)^//(wsl\.localhost|wsl\$)(?:/|$)`)
	// Any UNC-style path (//server/share) after normalize
	uncAny = regexp.MustCompile(`^//[^/]+/`)
	// DrvFs under /mnt/<drive-letter>
	drvFS = regexp.MustCompile(`(?i)^/mnt/([a-z])(/.*)?$`)
)

// WSLPathRunner maps a Windows/relative path to a Linux absolute path.
// Used to inject wslpath behavior in tests without requiring a real binary.
// Returning a non-absolute path or an error is treated as mapping failure.
type WSLPathRunner func(path string) (string, error)

// ToWSLOpts configures ToWSLPath and MapSourceDirs.
//
// When opts is nil, the default wslpath binary name "wslpath" is used when
// pure Go mapping is insufficient (relative / generic UNC).
// When opts is non-nil and both WSLPathBin and WSLPathRunner are empty/nil,
// wslpath is not invoked (useful in tests).
type ToWSLOpts struct {
	// WSLPathBin is the wslpath binary name or absolute path.
	// Ignored when WSLPathRunner is non-nil.
	WSLPathBin string
	// WSLPathRunner if set is used instead of exec'ing WSLPathBin.
	WSLPathRunner WSLPathRunner
}

// IsWSLUNCPath reports whether path is a \\wsl.localhost\... or \\wsl$\... form.
func IsWSLUNCPath(path string) bool {
	cleaned := cleanPathInput(path)
	if cleaned == "" {
		return false
	}
	normalized := normalizeForUNC(cleaned)
	if uncWSL.MatchString(normalized) {
		return true
	}
	lower := strings.ToLower(normalized)
	return strings.HasPrefix(lower, "//wsl.localhost") || strings.HasPrefix(lower, "//wsl$")
}

// IsUNCPath reports whether path is any UNC-style path (\\server\share / //server/share).
func IsUNCPath(path string) bool {
	cleaned := cleanPathInput(path)
	if cleaned == "" {
		return false
	}
	return uncAny.MatchString(normalizeForUNC(cleaned))
}

// IsDrvFsPath reports whether path is under /mnt/<drive-letter> (WSL DrvFs).
func IsDrvFsPath(path string) bool {
	text := filepath.ToSlash(path)
	if !strings.HasPrefix(text, "/") {
		return false
	}
	for strings.Contains(text, "//") {
		text = strings.ReplaceAll(text, "//", "/")
	}
	return drvFS.MatchString(text)
}

// ToWSLPath maps a Windows drive-letter or Linux path to a WSL absolute path.
//
// Supported inputs:
//   - D:\Archives / D:/Archives → /mnt/d/Archives
//   - Absolute Linux paths (/mnt/d/..., /var/lib/...) → unchanged
//   - Other Windows forms / generic UNC via wslpath when available
//
// Rejected:
//   - \\wsl.localhost\... / \\wsl$\... (use the in-distro path instead)
//   - Empty / whitespace-only strings
//   - Unmappable relative or UNC paths when wslpath is unavailable
func ToWSLPath(path string, opts *ToWSLOpts) (string, error) {
	cleaned := cleanPathInput(path)
	if cleaned == "" {
		return "", pathMapError("path is empty")
	}

	if IsWSLUNCPath(cleaned) {
		return "", pathMapErrorf(
			"UNC WSL paths are not supported as source_dirs: %q. "+
				"Use the Linux path inside the distro "+
				"(e.g. /mnt/d/Archives or /home/you/archives).",
			path,
		)
	}

	// Absolute Linux / WSL path — pass through.
	if strings.HasPrefix(cleaned, "/") {
		return cleaned, nil
	}

	// Windows drive-letter form: D:\foo, D:/foo, D:, D:\
	if m := winDrive.FindStringSubmatch(cleaned); m != nil {
		drive := strings.ToLower(m[1])
		rest := strings.ReplaceAll(m[2], `\`, `/`)
		rest = strings.Trim(rest, "/")
		if rest != "" {
			return "/mnt/" + drive + "/" + rest, nil
		}
		return "/mnt/" + drive, nil
	}

	// Other UNC (\\server\share) — only via wslpath when available.
	if IsUNCPath(cleaned) {
		mapped, ok := tryWSLPath(cleaned, opts)
		if ok {
			return mapped, nil
		}
		return "", pathMapErrorf(
			"UNC path %q cannot be mapped without wslpath "+
				"(available only inside WSL). Prefer a drive-letter path (D:\\...) "+
				"or an absolute Linux path (/mnt/...).",
			path,
		)
	}

	// Relative or other Windows form: try wslpath, else fail clearly.
	mapped, ok := tryWSLPath(cleaned, opts)
	if ok {
		return mapped, nil
	}
	return "", pathMapErrorf(
		"Cannot map path on this host: %q. "+
			"Use a Windows drive-letter path (D:\\\\Archives) or an absolute Linux path.",
		path,
	)
}

// MapSourceDirs maps configured source_dirs to WSL paths.
// Returns (original, mapped) pairs. On the first unmappable entry, returns a
// PathMapError with an index-prefixed message: source_dirs[i]: ...
func MapSourceDirs(sourceDirs []string, opts *ToWSLOpts) ([][2]string, error) {
	result := make([][2]string, 0, len(sourceDirs))
	for i, entry := range sourceDirs {
		mapped, err := ToWSLPath(entry, opts)
		if err != nil {
			if pe, ok := err.(*PathMapError); ok {
				return nil, pathMapErrorf("source_dirs[%d]: %s", i, pe.Message)
			}
			return nil, pathMapErrorf("source_dirs[%d]: %v", i, err)
		}
		result = append(result, [2]string{entry, mapped})
	}
	return result, nil
}

func cleanPathInput(path string) string {
	s := strings.TrimSpace(path)
	s = strings.Trim(s, `"'`)
	return s
}

func normalizeForUNC(path string) string {
	return strings.ReplaceAll(path, `\`, `/`)
}

func tryWSLPath(path string, opts *ToWSLOpts) (string, bool) {
	var runner WSLPathRunner
	bin := ""

	if opts == nil {
		bin = "wslpath"
	} else {
		runner = opts.WSLPathRunner
		bin = opts.WSLPathBin
	}

	if runner == nil && bin == "" {
		return "", false
	}

	if runner == nil {
		resolved, ok := resolveWSLPathBin(bin)
		if !ok {
			return "", false
		}
		runner = defaultWSLPathRunner(resolved)
	}

	result, err := runner(path)
	if err != nil {
		return "", false
	}
	result = strings.TrimSpace(result)
	if result == "" || !strings.HasPrefix(result, "/") {
		return "", false
	}
	return result, true
}

func resolveWSLPathBin(bin string) (string, bool) {
	if bin == "wslpath" {
		resolved, err := exec.LookPath("wslpath")
		if err != nil {
			return "", false
		}
		return resolved, true
	}
	// Explicit path or name
	if fi, err := os.Stat(bin); err == nil && !fi.IsDir() {
		return bin, true
	}
	if resolved, err := exec.LookPath(bin); err == nil {
		return resolved, true
	}
	return "", false
}

func defaultWSLPathRunner(bin string) WSLPathRunner {
	return func(p string) (string, error) {
		out, err := exec.Command(bin, "-a", p).Output()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(out)), nil
	}
}
