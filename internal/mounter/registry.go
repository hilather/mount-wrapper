package mounter

import (
	"os/exec"
	"sync"
	"time"
)

// Phase is the live-mount supervision phase.
type Phase string

const (
	// PhaseIndexOnly is a --no-mount index build (status indexing).
	PhaseIndexOnly Phase = "index_only"
	// PhaseMount is a FUSE mount (status mounting / mounted).
	PhaseMount Phase = "mount"
)

// ManagedMount is an in-memory handle for a running (or recently started) child.
type ManagedMount struct {
	ArchiveID     string
	PID           int
	Cmd           *exec.Cmd // may be nil in tests that only track PID
	Request       MountRequest
	StartedAt     time.Time
	IsFirstIndex  bool
	Phase         Phase
	// SkippedNested holds nested automount paths that failed (ratarmount skip).
	// Prefer NestedSkips / NoteNestedSkip under concurrent stderr drain.
	SkippedNested []string

	skipMu     sync.Mutex
	stderrDone chan struct{} // closed when stderr drain finishes; nil if no drain
}

// Registry is a concurrent live-mount map keyed by archive_id.
type Registry struct {
	mu   sync.Mutex
	live map[string]*ManagedMount
}

// NewRegistry returns an empty live-mount registry.
func NewRegistry() *Registry {
	return &Registry{live: make(map[string]*ManagedMount)}
}

// Put registers or replaces a managed mount.
func (r *Registry) Put(m *ManagedMount) {
	if r == nil || m == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		r.live = make(map[string]*ManagedMount)
	}
	r.live[m.ArchiveID] = m
}

// Get returns a managed mount by archive_id, or nil.
func (r *Registry) Get(archiveID string) *ManagedMount {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.live[archiveID]
}

// Drop removes and returns a managed mount, if any.
func (r *Registry) Drop(archiveID string) *ManagedMount {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.live[archiveID]
	delete(r.live, archiveID)
	return m
}

// Len returns the number of live entries.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.live)
}

// Snapshot returns a copy of archive_id → ManagedMount pointers (shallow).
func (r *Registry) Snapshot() map[string]*ManagedMount {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]*ManagedMount, len(r.live))
	for k, v := range r.live {
		out[k] = v
	}
	return out
}

// CountPhase returns how many live mounts are in the given phase.
func (r *Registry) CountPhase(phase Phase) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.live {
		if m != nil && m.Phase == phase {
			n++
		}
	}
	return n
}

// HoldsIndexSlot reports whether a live entry for archiveID is in index_only phase.
func (r *Registry) HoldsIndexSlot(archiveID string) bool {
	m := r.Get(archiveID)
	return m != nil && m.Phase == PhaseIndexOnly
}

// HoldsMountSlot reports whether a live entry for archiveID is in mount phase.
func (r *Registry) HoldsMountSlot(archiveID string) bool {
	m := r.Get(archiveID)
	return m != nil && m.Phase == PhaseMount
}
