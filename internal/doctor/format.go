package doctor

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FormatText returns a human-readable multi-line summary of the report.
// Pure: does not touch the filesystem or network.
func FormatText(r *Report) string {
	if r == nil {
		return "doctor: no report\n"
	}
	var b strings.Builder
	status := "OK"
	if !r.OK {
		status = "FAIL"
	}
	fmt.Fprintf(&b, "mount-wrapper doctor: %s\n", status)
	if r.ConfigPath != "" {
		fmt.Fprintf(&b, "config: %s\n", r.ConfigPath)
	}
	b.WriteString("\n")

	// Summary counts
	var nOK, nWarn, nErr int
	for _, c := range r.Checks {
		switch {
		case !c.OK && c.Severity == SeverityError:
			nErr++
		case !c.OK && c.Severity == SeverityWarn:
			nWarn++
		case c.Severity == SeverityWarn && c.OK:
			// soft warn (e.g. systemd not PID 1)
			nWarn++
		default:
			if c.OK {
				nOK++
			} else {
				nWarn++
			}
		}
	}
	fmt.Fprintf(&b, "checks: %d ok, %d warn, %d error (total %d)\n\n",
		nOK, nWarn, nErr, len(r.Checks))

	for _, c := range r.Checks {
		mark := markFor(c)
		fmt.Fprintf(&b, "  %s %-22s %s\n", mark, c.Name, c.Message)
	}

	if len(r.Notes) > 0 {
		b.WriteString("\nnotes:\n")
		for _, n := range r.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	if len(r.FixesApplied) > 0 {
		b.WriteString("\nfixes applied:\n")
		for _, f := range r.FixesApplied {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	return b.String()
}

func markFor(c CheckResult) string {
	if !c.OK && c.Severity == SeverityError {
		return "[FAIL]"
	}
	if !c.OK || c.Severity == SeverityWarn {
		return "[WARN]"
	}
	return "[ OK ]"
}

// FormatJSON returns indented JSON for the report (CLI --json).
// Pure encoder; returns an error only if marshaling fails (should not).
func FormatJSON(r *Report) (string, error) {
	if r == nil {
		r = &Report{OK: false, Checks: nil}
	}
	// Use ToMap for stable null handling of empty config_path / details.
	data, err := json.MarshalIndent(r.ToMap(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}
