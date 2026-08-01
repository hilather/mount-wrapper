package scanner

import "fmt"

// PriorStat is per-path size/mtime history for the stable-file gate.
type PriorStat struct {
	SizeBytes       int64
	MtimeNs         int64
	ConsecutiveSame int
}

// StableFileGate tracks per-path size/mtime history for the stable-file gate.
//
// Modes:
//   - two_scans: size+mtime identical on two consecutive successful scans
//   - min_age: age >= min_file_age_seconds AND size unchanged vs previous scan
//   - both: AND of the two modes (strictest)
type StableFileGate struct {
	Mode               string
	MinFileAgeSeconds  float64
	prior              map[string]*PriorStat
}

// NewStableFileGate creates a gate. mode must be two_scans, min_age, or both.
func NewStableFileGate(mode string, minFileAgeSeconds float64) (*StableFileGate, error) {
	switch mode {
	case "two_scans", "min_age", "both":
	default:
		return nil, fmt.Errorf("invalid stable_file_mode: %q", mode)
	}
	return &StableFileGate{
		Mode:              mode,
		MinFileAgeSeconds: minFileAgeSeconds,
		prior:             make(map[string]*PriorStat),
	}, nil
}

// Reset clears history for path, or all paths when path is empty.
func (g *StableFileGate) Reset(path string) {
	if path == "" {
		g.prior = make(map[string]*PriorStat)
		return
	}
	delete(g.prior, path)
}

// ForgetMissing drops history for paths not in present.
func (g *StableFileGate) ForgetMissing(present []string) {
	set := make(map[string]struct{}, len(present))
	for _, p := range present {
		set[p] = struct{}{}
	}
	for path := range g.prior {
		if _, ok := set[path]; !ok {
			delete(g.prior, path)
		}
	}
}

// Peek returns whether path would be considered stable without updating history.
// If size/mtime are nil, uses the last recorded observation only.
func (g *StableFileGate) Peek(path string, sizeBytes, mtimeNs *int64, now *float64) bool {
	prior := g.prior[path]
	if prior == nil {
		return false
	}
	size := prior.SizeBytes
	if sizeBytes != nil {
		size = *sizeBytes
	}
	mtime := prior.MtimeNs
	if mtimeNs != nil {
		mtime = *mtimeNs
	}
	if size != prior.SizeBytes || mtime != prior.MtimeNs {
		return false
	}

	twoOK := prior.ConsecutiveSame >= 2
	var minAgeOK bool
	if now == nil {
		minAgeOK = twoOK // insufficient to evaluate age alone
	} else {
		ageOK := ageSeconds(mtime, *now) >= g.MinFileAgeSeconds
		minAgeOK = ageOK && prior.ConsecutiveSame >= 1 && prior.ConsecutiveSame >= 2
	}

	switch g.Mode {
	case "two_scans":
		return twoOK
	case "min_age":
		if now == nil {
			return false
		}
		return minAgeOK
	default: // both
		return twoOK && minAgeOK
	}
}

// Check updates history for path and returns whether it is stable this scan.
func (g *StableFileGate) Check(path string, sizeBytes, mtimeNs int64, now float64, assumeStable bool) bool {
	if assumeStable {
		g.prior[path] = &PriorStat{SizeBytes: sizeBytes, MtimeNs: mtimeNs, ConsecutiveSame: 2}
		return true
	}

	prior := g.prior[path]
	var twoOK, sizeUnchanged bool
	if prior == nil || prior.SizeBytes != sizeBytes || prior.MtimeNs != mtimeNs {
		g.prior[path] = &PriorStat{SizeBytes: sizeBytes, MtimeNs: mtimeNs, ConsecutiveSame: 1}
		twoOK = false
		sizeUnchanged = false
	} else {
		prior.ConsecutiveSame++
		twoOK = prior.ConsecutiveSame >= 2
		sizeUnchanged = true
	}

	ageOK := ageSeconds(mtimeNs, now) >= g.MinFileAgeSeconds
	minAgeOK := ageOK && sizeUnchanged

	switch g.Mode {
	case "two_scans":
		return twoOK
	case "min_age":
		return minAgeOK
	default: // both
		return twoOK && minAgeOK
	}
}

func ageSeconds(mtimeNs int64, now float64) float64 {
	age := now - (float64(mtimeNs) / 1_000_000_000.0)
	if age < 0 {
		return 0
	}
	return age
}
