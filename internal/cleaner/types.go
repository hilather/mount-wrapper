package cleaner

// Overlay actions returned by HandleOverlay / PurgeResult.
const (
	OverlayNone         = "none"
	OverlayDeleted      = "deleted"
	OverlayQuarantined  = "quarantined"
	OverlayRetained     = "retained"
	OverlayMissing      = "missing"
	OverlayRefused      = "refused" // path not under overlay_dir
)

// PurgeResult is the outcome of purging one archive's state and on-disk artifacts.
type PurgeResult struct {
	ArchiveID      string
	OK             bool
	IndexDeleted   bool
	OverlayAction  string  // none | deleted | quarantined | retained | missing | refused
	OverlayDest    string  // quarantine destination when quarantined
	MountCleaned   bool
	Error          string
}

// CleanerRunResult summarizes one cleaner pass.
type CleanerRunResult struct {
	Purged                 []PurgeResult
	QuarantinePruned       int
	QuarantineBytesFreed   int64
	MountDirsRemoved       []string
	RatarmountTempsRemoved int
	RatarmountTempsFreed   int64
	LowDisk                bool
	FreeBytes              *int64
	Errors                 []string
}

// PurgedIDs returns archive IDs that purged successfully.
func (r *CleanerRunResult) PurgedIDs() []string {
	if r == nil {
		return nil
	}
	var ids []string
	for _, p := range r.Purged {
		if p.OK {
			ids = append(ids, p.ArchiveID)
		}
	}
	if ids == nil {
		ids = []string{}
	}
	return ids
}
