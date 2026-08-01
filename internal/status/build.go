package status

import (
	"sort"
	"time"

	"github.com/hilather/mount-wrapper/internal/metrics"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Build constructs the full status document from Options.
// Pure given inputs: no process-wide state except optional defaults for clock
// and generated_at when those fields are unset.
func Build(opts Options) *Payload {
	nowTS := opts.Now
	if nowTS == 0 {
		if opts.Clock != nil {
			nowTS = opts.Clock()
		} else {
			nowTS = float64(time.Now().UnixNano()) / 1e9
		}
	}
	live := opts.Live
	if live == nil {
		live = map[string]LiveMount{}
	}
	archives := opts.Archives
	if archives == nil {
		archives = []*ArchiveInput{}
	}

	counts := make(map[string]int, len(countStatusKeys))
	for _, k := range countStatusKeys {
		counts[k] = 0
	}
	for _, a := range archives {
		if a == nil {
			continue
		}
		if _, ok := counts[a.Status]; ok {
			counts[a.Status]++
		} else {
			// Unknown status still counted for visibility.
			counts[a.Status]++
		}
	}

	var diskFree *int64
	if opts.FreeBytes != nil {
		free, ok := opts.FreeBytes(opts.OverlayDir)
		if !ok || free < 0 {
			free, ok = opts.FreeBytes(opts.IndexDir)
		}
		if ok {
			v := free
			diskFree = &v
		}
	}

	archiveDicts := make([]ArchiveDict, 0, len(archives))
	for _, a := range archives {
		archiveDicts = append(archiveDicts, ArchiveToDict(a, nowTS, live, opts.PIDAlive, opts.IsMount))
	}
	indexing := BuildIndexingArchives(archives, nowTS, live, opts.PIDAlive)
	errors := BuildErrorsRecent(archives, opts.MaxMountAttempts, 20)

	var scanDuration *float64
	if opts.LastScan != nil {
		if v, ok := opts.LastScan["duration_ms"]; ok {
			switch n := v.(type) {
			case float64:
				scanDuration = &n
			case float32:
				f := float64(n)
				scanDuration = &f
			case int:
				f := float64(n)
				scanDuration = &f
			case int64:
				f := float64(n)
				scanDuration = &f
			}
		}
	}

	liveKeys := make([]string, 0, len(live))
	for k := range live {
		liveKeys = append(liveKeys, k)
	}
	// Stable order for JSON.
	if len(liveKeys) > 1 {
		sortStrings(liveKeys)
	}

	generated := opts.GeneratedAt
	if generated == "" {
		generated = state.UTCNowISO()
	}

	var lastScan map[string]any
	if opts.LastScan != nil {
		lastScan = opts.LastScan
	}
	var lastCleanup map[string]any
	if opts.LastCleanup != nil {
		lastCleanup = opts.LastCleanup
	}

	p := &Payload{
		Version:            opts.Version,
		PID:                opts.PID,
		ConfigPath:         opts.ConfigPath,
		Mounted:            counts["mounted"],
		Indexing:           counts["indexing"],
		Mounting:           counts["mounting"],
		Absent:             counts["absent"],
		MountFailed:        counts["mount_failed"],
		IndexFailed:        counts["index_failed"],
		Discovered:         counts["discovered"],
		HooksRunning:       counts["hooks_running"],
		Counts:             counts,
		IndexingArchives:   indexing,
		ErrorsRecent:       errors,
		LastScanAt:         opts.LastScanAt,
		LastScanDurationMs: scanDuration,
		LastScan:           lastScan,
		LastCleanup:        lastCleanup,
		DiskFreeBytes:      diskFree,
		LowDisk:            opts.LowDisk,
		MinFreeBytes:       opts.MinFreeBytes,
		LiveMounts:         liveKeys,
		Archives:           archiveDicts,
		GeneratedAt:        generated,
	}

	if opts.IncludeSizes && opts.Metrics != nil {
		mergeSizes(p, opts.Metrics)
	}
	return p
}

// FromStateRecord maps a state.ArchiveRecord into ArchiveInput.
func FromStateRecord(rec *state.ArchiveRecord) *ArchiveInput {
	if rec == nil {
		return nil
	}
	return &ArchiveInput{
		ArchiveID:              rec.ArchiveID,
		SourceDir:              rec.SourceDir,
		ArchivePath:            rec.ArchivePath,
		ArchiveBasename:        rec.ArchiveBasename,
		SizeBytes:              rec.SizeBytes,
		Status:                 rec.Status,
		HooksStatus:            rec.HooksStatus,
		MountPath:              rec.MountPath,
		IndexPath:              rec.IndexPath,
		OverlayPath:            rec.OverlayPath,
		MountPID:               rec.MountPID,
		MountAttempts:          rec.MountAttempts,
		MountRetryable:         rec.MountRetryable,
		Fingerprint:            rec.Fingerprint,
		LastError:              rec.LastError,
		LastSeenAt:             rec.LastSeenAt,
		RemovedAt:              rec.RemovedAt,
		FirstMountedAt:         rec.FirstMountedAt,
		HooksCompletedAt:       rec.HooksCompletedAt,
		IndexStartedAt:         rec.IndexStartedAt,
		IndexDurationSeconds:   rec.IndexDurationSeconds,
		MountDurationSeconds:   rec.MountDurationSeconds,
		ConvertSourceSizeBytes: rec.ConvertSourceSizeBytes,
		ConvertDurationSeconds: rec.ConvertDurationSeconds,
	}
}

// FromStateRecords maps a slice of state records.
func FromStateRecords(recs []*state.ArchiveRecord) []*ArchiveInput {
	out := make([]*ArchiveInput, 0, len(recs))
	for _, r := range recs {
		if in := FromStateRecord(r); in != nil {
			out = append(out, in)
		}
	}
	return out
}

func mergeSizes(p *Payload, provider MetricsProvider) {
	if p == nil || provider == nil {
		return
	}
	items, err := provider.GetAll(metrics.QueryOptions{}, nil)
	if err != nil || items == nil {
		return
	}
	byID := make(map[string]metrics.ArchiveMetrics, len(items))
	for i := range items {
		byID[items[i].ArchiveID] = items[i]
	}
	for i := range p.Archives {
		if m, ok := byID[p.Archives[i].ArchiveID]; ok {
			// Copy to avoid sharing loop variable addresses if callers mutate.
			cp := m
			p.Archives[i].Metrics = &cp
		}
	}
	sum, err := provider.Summary(items, metrics.QueryOptions{})
	if err != nil {
		return
	}
	p.MetricsSummary = &sum
}

func sortStrings(s []string) {
	sort.Strings(s)
}
