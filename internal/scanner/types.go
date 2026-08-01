package scanner

// FileObservation is one matching archive file seen during a scan.
type FileObservation struct {
	Path        string
	SourceDir   string
	Basename    string
	SizeBytes   int64
	MtimeNs     int64
	Inode       *uint64
	Fingerprint string
	Stable      bool
}

// ScanResult is the outcome of a single scanner pass.
type ScanResult struct {
	DurationMs        float64
	Observations      []FileObservation
	InsertedIDs       []string
	ReappearedIDs     []string
	TouchedIDs        []string
	ContentChangedIDs []string
	AbsentIDs         []string
	StableArchiveIDs  []string
	SkippedSources    []string
	SkippedFiles      []string
	Errors            []string
	AssumeStable      bool
}

// SeenPaths returns the set of observation paths.
func (r *ScanResult) SeenPaths() map[string]struct{} {
	out := make(map[string]struct{}, len(r.Observations))
	for _, o := range r.Observations {
		out[o.Path] = struct{}{}
	}
	return out
}
