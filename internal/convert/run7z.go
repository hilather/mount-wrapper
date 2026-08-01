package convert

import (
	"fmt"
	"os/exec"
	"strings"
)

// Run7zFunc executes a 7z CLI invocation.
// bin is the binary path/name; args do not include bin; cwd may be empty.
// Injectable for tests (fake 7z scripts or in-process stubs).
type Run7zFunc func(bin string, args []string, cwd string) error

// DefaultRun7z runs 7z via os/exec and surfaces stderr/stdout on failure.
func DefaultRun7z(bin string, args []string, cwd string) error {
	if strings.TrimSpace(bin) == "" {
		bin = "7z"
	}
	cmd := exec.Command(bin, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(string(out))
	msg := fmt.Sprintf("7z failed: %s %s", bin, strings.Join(args, " "))
	if detail != "" {
		msg += ": " + detail
	}
	return convertErrorf("run_7z", "%s", msg)
}

func run7zOf(fn Run7zFunc) Run7zFunc {
	if fn != nil {
		return fn
	}
	return DefaultRun7z
}

// SevenZipExcludeArgs returns 7z -xr! pattern flags (parity with
// ratarmountcore.nonsolid_convert._7z_exclude_args).
func SevenZipExcludeArgs(excludePatterns []string) []string {
	if len(excludePatterns) == 0 {
		return nil
	}
	out := make([]string, 0, len(excludePatterns))
	for _, p := range excludePatterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, "-xr!"+p)
	}
	return out
}
