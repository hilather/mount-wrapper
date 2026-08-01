package convert

import (
	"os/exec"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// List7zFunc runs a 7z list command and returns combined output.
// Injectable for unit tests (no real 7z required).
// bin is the 7z path/name; args do not include bin; cwd may be empty.
type List7zFunc func(bin string, args []string, cwd string) (output string, err error)

// DefaultList7z runs 7z via os/exec and returns combined stdout+stderr.
func DefaultList7z(bin string, args []string, cwd string) (string, error) {
	if strings.TrimSpace(bin) == "" {
		bin = "7z"
	}
	cmd := exec.Command(bin, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func list7zOf(fn List7zFunc) List7zFunc {
	if fn != nil {
		return fn
	}
	return DefaultList7z
}

// Parse7zListEncrypted reports whether `7z l -slt` (or stderr mixed into
// combined output) indicates an encrypted archive that cannot be auto-flattened
// without a password. Best-effort CLI signals:
//
//   - phrases: "Wrong password", "Enter password", "encrypted archive", …
//   - technical listing: Encrypted = + (headers or any member)
//
// Not a full py7zr folder.is_encrypted() parse; conservative for false
// positives on clear password/encryption markers only.
func Parse7zListEncrypted(listOutput string) bool {
	if strings.TrimSpace(listOutput) == "" {
		return false
	}
	text := strings.ReplaceAll(listOutput, "\r\n", "\n")
	lower := strings.ToLower(text)
	for _, phrase := range encryptedListPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Encrypted = +  (archive headers or member content)
		if isKeyValFold(line, "Encrypted", "+") {
			return true
		}
	}
	return false
}

// Phrases commonly emitted by 7-Zip when headers/content need a password.
// Matched case-insensitively against combined stdout+stderr.
var encryptedListPhrases = []string{
	"wrong password",
	"enter password",
	"encrypted archive",
	"can not open encrypted",
	"cannot open encrypted",
	"data error in encrypted file",
	"encrypted header",
	"headers encrypted",
}

// Parse7zListIsSolid reports archive-level Solid = + in `7z l -slt` text.
// False when absent/ambiguous (not a full solid-folder parser).
func Parse7zListIsSolid(listOutput string) bool {
	if strings.TrimSpace(listOutput) == "" {
		return false
	}
	text := strings.ReplaceAll(listOutput, "\r\n", "\n")
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if isKeyValFold(line, "Solid", "+") {
			return true
		}
	}
	return false
}

// Parse7zListNeedsFlatten inspects `7z l -slt` (or similar) text for clear
// signals that a flatten conversion would help:
//
//   - archive-level Solid = +
//   - a member Path ending in .7z (nested archive)
//
// Encrypted archives (Parse7zListEncrypted) never need auto-flatten — returns
// false so callers skip convert; runners still surface a clear error if invoked.
//
// Conservative: returns false when output is empty, ambiguous, encrypted, or
// lacks those signals. Best-effort CLI heuristic — not full ratarmountcore /
// py7zr solid-folder parity (no stream-flatten decision, no multi-block solid
// inference beyond the Solid flag).
func Parse7zListNeedsFlatten(listOutput string) bool {
	if strings.TrimSpace(listOutput) == "" {
		return false
	}
	// Cannot auto-flatten password-protected archives.
	if Parse7zListEncrypted(listOutput) {
		return false
	}
	// Normalize newlines for portable matching.
	text := strings.ReplaceAll(listOutput, "\r\n", "\n")
	lines := strings.Split(text, "\n")

	// Technical listing splits archive props and per-file props with ----------.
	// Nested .7z members appear only in the file section; archive Path= itself
	// ends in .7z and must not trigger nested detection.
	inFileSection := false
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "----------") {
			inFileSection = true
			continue
		}
		// Solid = +  (7-Zip technical listing; case-insensitive key)
		if isKeyValFold(line, "Solid", "+") {
			return true
		}
		if !inFileSection {
			continue
		}
		// Member Path = nested.7z
		if path, ok := keyVal(line, "Path"); ok {
			if isNested7zMember(path) {
				return true
			}
		}
	}
	return false
}

// Encrypted7zMessage is the stable error text for unsupported encrypted 7z.
const Encrypted7zMessage = "encrypted 7z not supported"

// RefuseIfEncrypted7z runs `7z l -slt` and returns a clear convert error when
// encryption is indicated. Nil when not encrypted or listing is empty/unknown
// (conservative: empty listing does not refuse — probe already skipped).
func RefuseIfEncrypted7z(bin, archivePath string, list List7zFunc) error {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" {
		return nil
	}
	if strings.TrimSpace(bin) == "" {
		bin = "7z"
	}
	out, _ := list7zOf(list)(bin, []string{"l", "-slt", archivePath}, "")
	if Parse7zListEncrypted(out) {
		return convertErrorf("encrypted", "%s: %s", Encrypted7zMessage, archivePath)
	}
	return nil
}

// isNested7zMember reports whether a listed member path looks like an embedded
// 7z (not a directory). Basename ends with .7z (case-insensitive).
func isNested7zMember(memberPath string) bool {
	p := strings.TrimSpace(memberPath)
	if p == "" || strings.HasSuffix(p, "/") || strings.HasSuffix(p, `\`) {
		return false
	}
	// Strip trailing path separators and take basename-ish.
	p = strings.ReplaceAll(p, `\`, "/")
	base := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		base = p[i+1:]
	}
	return strings.HasSuffix(strings.ToLower(base), ".7z")
}

func isKeyValFold(line, key, want string) bool {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return false
	}
	k := strings.TrimSpace(line[:eq])
	v := strings.TrimSpace(line[eq+1:])
	return strings.EqualFold(k, key) && v == want
}

func keyVal(line, key string) (string, bool) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", false
	}
	k := strings.TrimSpace(line[:eq])
	if !strings.EqualFold(k, key) {
		return "", false
	}
	return strings.TrimSpace(line[eq+1:]), true
}

// Probe7zNeedsFlatten runs `7z l -slt` on archivePath and returns whether
// flatten is clearly indicated. On any error, missing output, uncertainty, or
// encryption markers returns false (conservative — prefer skip over
// false-positive convert; encrypted archives are never auto-flattened).
//
// Does not claim full solid/folder parser parity with ratarmountcore.
func Probe7zNeedsFlatten(bin, archivePath string, list List7zFunc) bool {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" || !IsSevenzPath(archivePath) {
		return false
	}
	if strings.TrimSpace(bin) == "" {
		bin = "7z"
	}
	out, err := list7zOf(list)(bin, []string{"l", "-slt", archivePath}, "")
	// 7z may exit non-zero for warnings while still printing useful listing;
	// still parse whatever we got. Empty → false (conservative).
	if strings.TrimSpace(out) == "" {
		_ = err
		return false
	}
	return Parse7zListNeedsFlatten(out)
}

// Probe7zEncrypted runs `7z l -slt` and reports encryption markers.
// False on empty/missing output (conservative).
func Probe7zEncrypted(bin, archivePath string, list List7zFunc) bool {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" || !IsSevenzPath(archivePath) {
		return false
	}
	if strings.TrimSpace(bin) == "" {
		bin = "7z"
	}
	out, _ := list7zOf(list)(bin, []string{"l", "-slt", archivePath}, "")
	return Parse7zListEncrypted(out)
}

// CLIFlattenNeeded builds a FlattenNeededFunc using `7z l -slt` heuristics.
//
// When 7z is not available (ResolveOptions / PATH), the returned func always
// returns false. When available, probes each path conservatively (false on
// uncertainty). list may be nil (uses DefaultList7z).
//
// Callers should still gate with ShouldFlattenConvert (nonsolid + flatten scope
// + no metadata). This probe only answers "does structure look like it needs
// flatten?" and does not re-check config.
func CLIFlattenNeeded(cfg *config.Config, opts ResolveOptions, list List7zFunc) FlattenNeededFunc {
	// Resolve once at construction; bin path may still disappear later.
	if !SevenZipAvailable(cfg, opts) {
		return func(string) bool { return false }
	}
	bin := EffectiveSevenZipBin(cfg, opts)
	listFn := list7zOf(list)
	return func(archivePath string) bool {
		return Probe7zNeedsFlatten(bin, archivePath, listFn)
	}
}

// DefaultFlattenNeeded returns CLIFlattenNeeded when convert_7z_nonsolid is
// enabled and convert_7z_scope is flatten; otherwise nil (no auto probe).
//
// Wire as Engine.NeedsFlatten when the field is nil so production gets a
// best-effort probe without claiming full ratarmountcore parity.
func DefaultFlattenNeeded(cfg *config.Config, opts ResolveOptions, list List7zFunc) FlattenNeededFunc {
	if cfg == nil || !cfg.Convert7zNonsolid || !ScopeIsFlatten(cfg.Convert7zScope) {
		return nil
	}
	return CLIFlattenNeeded(cfg, opts, list)
}
