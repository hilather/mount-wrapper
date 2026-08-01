package doctor

// Severity values for CheckResult.Severity (parity with Python doctor).
const (
	SeverityInfo  = "info"
	SeverityWarn  = "warn"
	SeverityError = "error"
)

// CheckResult is one diagnostic check.
type CheckResult struct {
	Name     string         `json:"name"`
	OK       bool           `json:"ok"`
	Severity string         `json:"severity"` // info | warn | error
	Message  string         `json:"message"`
	Details  map[string]any `json:"details"`
}

// Report is the aggregate doctor output.
//
// OK is false when any check has SeverityError and OK=false (hard fail).
// Warn-only failures leave OK true so operators can still use the tool.
type Report struct {
	OK           bool          `json:"ok"`
	Checks       []CheckResult `json:"checks"`
	ConfigPath   string        `json:"config_path,omitempty"`
	Notes        []string      `json:"notes,omitempty"`
	FixesApplied []string      `json:"fixes_applied,omitempty"`
}

// ToMap returns a JSON-serializable map (parity with Python DoctorReport.to_dict).
func (r *Report) ToMap() map[string]any {
	if r == nil {
		return map[string]any{
			"ok":            false,
			"checks":        []any{},
			"notes":         []any{},
			"fixes_applied": []any{},
			"config_path":   nil,
		}
	}
	checks := make([]any, 0, len(r.Checks))
	for _, c := range r.Checks {
		details := c.Details
		if details == nil {
			details = map[string]any{}
		}
		checks = append(checks, map[string]any{
			"name":     c.Name,
			"ok":       c.OK,
			"severity": c.Severity,
			"message":  c.Message,
			"details":  details,
		})
	}
	var configPath any
	if r.ConfigPath != "" {
		configPath = r.ConfigPath
	}
	notes := r.Notes
	if notes == nil {
		notes = []string{}
	}
	fixes := r.FixesApplied
	if fixes == nil {
		fixes = []string{}
	}
	return map[string]any{
		"ok":            r.OK,
		"config_path":   configPath,
		"notes":         notes,
		"fixes_applied": fixes,
		"checks":        checks,
	}
}

// HardFail reports whether any check is a non-OK error-severity result.
func HardFail(checks []CheckResult) bool {
	for _, c := range checks {
		if !c.OK && c.Severity == SeverityError {
			return true
		}
	}
	return false
}

func infoCheck(name, message string, details map[string]any) CheckResult {
	return CheckResult{
		Name: name, OK: true, Severity: SeverityInfo,
		Message: message, Details: ensureDetails(details),
	}
}

func warnCheck(name string, ok bool, message string, details map[string]any) CheckResult {
	return CheckResult{
		Name: name, OK: ok, Severity: SeverityWarn,
		Message: message, Details: ensureDetails(details),
	}
}

func errorCheck(name string, ok bool, message string, details map[string]any) CheckResult {
	sev := SeverityError
	if ok {
		sev = SeverityInfo
	}
	return CheckResult{
		Name: name, OK: ok, Severity: sev,
		Message: message, Details: ensureDetails(details),
	}
}

func ensureDetails(d map[string]any) map[string]any {
	if d == nil {
		return map[string]any{}
	}
	return d
}
