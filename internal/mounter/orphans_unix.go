//go:build unix

package mounter

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hilather/mount-wrapper/internal/state"
)

// RatarmountProc describes a running ratarmount child discovered from /proc.
type RatarmountProc struct {
	PID       int
	MountPath string
}

// OrphanMountReconcileResult summarizes boot-time orphan cleanup.
type OrphanMountReconcileResult struct {
	KilledPIDs []int
	Cleared    []string // mount paths fusermount attempted after killing orphans
}

// ratarmountProcLister lists ratarmount processes; tests may override.
var ratarmountProcLister = defaultListRatarmountProcs

// terminateOrphanPIDHook is replaceable in tests (see export_test.go).
var terminateOrphanPIDHook = terminateOrphanPID

func defaultListRatarmountProcs() ([]RatarmountProc, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []RatarmountProc
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(ent.Name())
		if err != nil || pid <= 0 {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", ent.Name(), "comm"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(comm)) != "ratarmount" {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", ent.Name(), "cmdline"))
		if err != nil {
			continue
		}
		mountPath, ok := parseRatarmountMountPath(cmdline)
		if !ok {
			continue
		}
		out = append(out, RatarmountProc{PID: pid, MountPath: mountPath})
	}
	return out, nil
}

func parseRatarmountMountPath(cmdline []byte) (string, bool) {
	parts := bytes.Split(cmdline, []byte{0})
	var args []string
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		args = append(args, string(p))
	}
	if len(args) < 2 {
		return "", false
	}
	base := filepath.Base(args[0])
	if base != "ratarmount" && base != "ratarmount-rs" {
		return "", false
	}
	mountPath := strings.TrimSpace(args[len(args)-1])
	if mountPath == "" || mountPath == args[len(args)-2] {
		return "", false
	}
	return filepath.Clean(mountPath), true
}

// FindRatarmountPIDsForMount returns PIDs whose argv ends with mountPath.
func FindRatarmountPIDsForMount(mountPath string) []int {
	mountPath = filepath.Clean(mountPath)
	procs, err := ratarmountProcLister()
	if err != nil {
		return nil
	}
	var pids []int
	for _, p := range procs {
		if p.MountPath == mountPath {
			pids = append(pids, p.PID)
		}
	}
	return pids
}

// ClearStaleMountHolders kills ratarmount processes bound to mountPath except
// keepPID, then unmounts the path when it is still a mountpoint.
func ClearStaleMountHolders(mountPath string, keepPID int, opts UnmountOptions) (killed []int, res UnmountResult) {
	mountPath = filepath.Clean(mountPath)
	if mountPath == "" {
		return nil, UnmountResult{}
	}
	procs, err := ratarmountProcLister()
	if err != nil {
		slog.Debug("list ratarmount procs failed", "err", err)
	}
	for _, p := range procs {
		if p.MountPath != mountPath || p.PID == keepPID {
			continue
		}
		terminateOrphanPIDHook(p.PID)
		killed = append(killed, p.PID)
	}
	isMount := opts.IsMount
	if isMount == nil {
		isMount = DefaultIsMount
	}
	if !isMount(mountPath) {
		return killed, res
	}
	// When keepPID still holds the mount, leave it alone.
	if keepPID > 0 && IsProcessAlive(keepPID) {
		stillHolder := false
		for _, p := range procs {
			if p.PID == keepPID && p.MountPath == mountPath {
				stillHolder = true
				break
			}
		}
		if stillHolder && len(killed) == 0 {
			return killed, res
		}
	}
	res = UnmountSequence(nil, 0, mountPath, opts)
	return killed, res
}

func terminateOrphanPID(pid int) {
	if pid <= 0 || !IsProcessAlive(pid) {
		return
	}
	_ = KillProcessGroup(pid, syscall.SIGTERM)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !IsProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = KillProcessGroup(pid, syscall.SIGKILL)
}

func activeMountStatus(status string) bool {
	switch status {
	case state.StatusIndexing, state.StatusMounting, state.StatusMounted, state.StatusHooksRunning:
		return true
	default:
		return false
	}
}

func mountUnderRoot(mountPath, root string) bool {
	root = filepath.Clean(root)
	mountPath = filepath.Clean(mountPath)
	if root == "" || mountPath == "" {
		return false
	}
	if mountPath == root {
		return true
	}
	return strings.HasPrefix(mountPath, root+string(filepath.Separator))
}

// ReconcileOrphanMounts kills ratarmount children under mountRoot that are not
// the tracked PID for an active archive row (Live registry or mount_pid).
func (e *Engine) ReconcileOrphanMounts(recs []*state.ArchiveRecord) OrphanMountReconcileResult {
	var result OrphanMountReconcileResult
	if e == nil || e.Config == nil {
		return result
	}
	root := filepath.Clean(e.Config.MountRoot)
	if root == "" {
		return result
	}

	expected := map[string]int{}
	for _, rec := range recs {
		if rec == nil || rec.MountPath == nil {
			continue
		}
		mp := filepath.Clean(*rec.MountPath)
		if !mountUnderRoot(mp, root) || !activeMountStatus(rec.Status) {
			continue
		}
		keep := 0
		if e.Live != nil {
			if live := e.Live.Get(rec.ArchiveID); live != nil && live.PID > 0 {
				keep = live.PID
			}
		}
		if keep == 0 && rec.MountPID != nil {
			keep = int(*rec.MountPID)
		}
		if keep > 0 {
			expected[mp] = keep
		}
	}

	procs, err := ratarmountProcLister()
	if err != nil {
		slog.Warn("orphan mount reconcile: list procs failed", "err", err)
		return result
	}

	killedPerMount := map[string]int{}
	for _, p := range procs {
		if !mountUnderRoot(p.MountPath, root) {
			continue
		}
		keep := expected[p.MountPath]
		if keep > 0 && p.PID == keep {
			continue
		}
		terminateOrphanPIDHook(p.PID)
		result.KilledPIDs = append(result.KilledPIDs, p.PID)
		killedPerMount[p.MountPath]++
	}

	opts := e.unmountOpts()
	isMount := opts.IsMount
	if isMount == nil {
		isMount = DefaultIsMount
	}
	for mp, n := range killedPerMount {
		if n == 0 || !isMount(mp) {
			continue
		}
		if keep := expected[mp]; keep > 0 && IsProcessAlive(keep) {
			continue
		}
		_ = UnmountSequence(nil, 0, mp, opts)
		result.Cleared = append(result.Cleared, mp)
	}
	if len(result.KilledPIDs) > 0 {
		slog.Info("orphan mount reconcile",
			"event", "orphan_mount_reconcile",
			"killed_pids", result.KilledPIDs,
			"cleared_mounts", result.Cleared,
		)
	}
	return result
}

func (e *Engine) unmountOpts() UnmountOptions {
	return UnmountOptions{
		Timeout: UnmountTimeout(e.Config.UnmountTimeoutSeconds),
		IsMount: e.isMount,
	}
}
