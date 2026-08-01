package mounter

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hilather/mount-wrapper/internal/archives"
	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/state"
)

// convertJob is a background convert/repack unit. Store writes happen only
// on the service tick via PollConvert.
type convertJob struct {
	archiveID  string
	sourcePath string
	sourceSize int64
	needsIndex bool
	startedAt  time.Time

	mu         sync.Mutex
	done       bool
	err        string
	resultPath string
	metadata   *convert.ConvertMetadata
}

func (j *convertJob) markDone(resultPath, errMsg string, meta *convert.ConvertMetadata) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.done {
		return
	}
	j.done = true
	j.err = errMsg
	j.resultPath = resultPath
	j.metadata = meta
}

func (j *convertJob) snapshot() (done bool, errMsg, resultPath string, meta *convert.ConvertMetadata) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.done, j.err, j.resultPath, j.metadata
}

// relocateJob is reserved for async relocate; v1 uses sync relocateSync.
type relocateJob struct {
	archiveID  string
	sourcePath string
	destPath   string
	mu         sync.Mutex
	done       bool
	err        string
}

func (e *Engine) beginConvertAsync(rec *state.ArchiveRecord, needsIndex bool) error {
	sourcePath := rec.ArchivePath
	st, err := os.Stat(sourcePath)
	if err != nil || !st.Mode().IsRegular() {
		return e.failConvertClaim(rec, "archive not found: "+sourcePath)
	}
	sourceSize := st.Size()

	fields := map[string]any{
		"mount_pid":                nil,
		"last_error":               nil,
		"convert_source_size_bytes": sourceSize,
	}
	rec, err = e.Store.Transition(rec.ArchiveID, state.StatusConverting, rec.Status, fields, "")
	if err != nil {
		return err
	}

	job := &convertJob{
		archiveID:  rec.ArchiveID,
		sourcePath: sourcePath,
		sourceSize: sourceSize,
		needsIndex: needsIndex,
		startedAt:  e.now(),
	}
	e.mu.Lock()
	e.convertJobs[rec.ArchiveID] = job
	e.mu.Unlock()

	slog.Info("archive convert start",
		"event", "archive_convert_start",
		"archive_id", rec.ArchiveID,
		"path", sourcePath,
		"size", sourceSize,
	)
	go e.runConvert(job)
	return nil
}

func (e *Engine) failConvertClaim(rec *state.ArchiveRecord, reason string) error {
	failStatus := state.StatusMountFailed
	if rec.FirstMountedAt == nil {
		failStatus = state.StatusIndexFailed
	}
	attempts, retryable := NextMountAttempt(rec.MountAttempts, e.Config.MaxMountAttempts)
	fields := map[string]any{
		"mount_pid":       nil,
		"mount_attempts":  attempts,
		"mount_retryable": retryable,
		"last_error":      reason,
	}
	// discovered cannot go directly to index_failed; step through indexing when needed.
	if err := state.ValidateTransition(rec.Status, failStatus); err != nil {
		if rec.Status == state.StatusDiscovered && failStatus == state.StatusIndexFailed {
			if mid, err2 := e.Store.Transition(rec.ArchiveID, state.StatusIndexing, rec.Status, map[string]any{
				"last_error": reason,
			}, ""); err2 == nil {
				rec = mid
			}
		}
	}
	if _, err := e.Store.Transition(rec.ArchiveID, failStatus, rec.Status, fields, ""); err != nil {
		// Last resort: field-only patch so the reason is recorded.
		_, _ = e.Store.Transition(rec.ArchiveID, rec.Status, rec.Status, fields, "")
	}
	return mounterErrorf("%s", reason)
}

func (e *Engine) runConvert(job *convertJob) {
	if job == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			job.markDone("", "convert panic", nil)
		}
	}()

	source := job.sourcePath
	cfg := e.Config

	// Prefer archiveconverter for solid .7z when available and not zip-repack.
	if convert.ShouldConvert(cfg, source, true, e.ConvertOpts) &&
		!convert.ShouldRepackZip(cfg, source) {
		outPath, meta, err := e.runArchiveconverter(job.archiveID, source)
		if err != nil {
			job.markDone("", err.Error(), nil)
			return
		}
		job.markDone(outPath, "", meta)
		return
	}

	// Order matches Python mounter._run_convert: archiveconverter → zip → flatten.
	if convert.ShouldRepackZip(cfg, source) {
		outPath, meta, err := e.runZipRepack(source)
		if err != nil {
			job.markDone("", err.Error(), nil)
			return
		}
		job.markDone(outPath, "", meta)
		return
	}

	if convert.ShouldFlattenConvert(cfg, source, e.NeedsFlatten) {
		outPath, meta, err := e.runFlatten(source)
		if err != nil {
			job.markDone("", err.Error(), nil)
			return
		}
		job.markDone(outPath, "", meta)
		return
	}

	// ShouldPreconvert was true but no runner matched — treat as no-op success
	// using the original path (e.g. already converted metadata).
	job.markDone(source, "", nil)
}

func (e *Engine) runArchiveconverter(archiveID, source string) (string, *convert.ConvertMetadata, error) {
	bin := convert.EffectiveArchiveconverterBin(e.Config, e.ConvertOpts)
	if bin == "" {
		return "", nil, mounterErrorf("archiveconverter binary not found")
	}
	outPath := convert.ConvertedFilePath(e.Config, archiveID)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", nil, err
	}
	// Reuse existing converted product when present.
	if existing := convert.ExistingConvertedPath(e.Config, archiveID); existing != "" {
		return existing, convert.ReadConvertMetadata(existing), nil
	}

	argv, err := convert.BuildConvertCmd(e.Config, bin, source, outPath)
	if err != nil {
		return "", nil, err
	}
	timeout := convert.ArchiveconverterTimeout(e.Config)
	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout*float64(time.Second)))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, mounterErrorf("archiveconverter failed: %v: %s", err, truncate(string(out), 512))
	}
	st, err := os.Stat(outPath)
	if err != nil || !st.Mode().IsRegular() || st.Size() <= 0 {
		return "", nil, mounterErrorf("archiveconverter produced no output at %s", outPath)
	}
	srcSt, _ := os.Stat(source)
	srcSize := int64(0)
	if srcSt != nil {
		srcSize = srcSt.Size()
	}
	dur := e.now().Sub(jobStarted(e, archiveID)).Seconds()
	if dur < 0 {
		dur = 0
	}
	d := float64(int(dur*1000)) / 1000
	meta := convert.BuildConvertMetadata(srcSize, st.Size(), "archiveconverter", &d)
	if _, err := convert.WriteConvertMetadata(outPath, meta); err != nil {
		slog.Warn("write convert metadata", "path", outPath, "err", err)
	}
	return outPath, &meta, nil
}

func jobStarted(e *Engine, archiveID string) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	if j := e.convertJobs[archiveID]; j != nil {
		return j.startedAt
	}
	return time.Now()
}

func (e *Engine) runZipRepack(source string) (string, *convert.ConvertMetadata, error) {
	p := convert.ZipRepackParamsFromConfig(e.Config, e.ConvertOpts)
	if e.Run7z != nil {
		p.Run7z = e.Run7z
	}
	if strings.TrimSpace(p.SevenZipBin) == "" {
		return "", nil, mounterErrorf("zip repack requires convert_7z_bin / 7z on PATH")
	}
	dest, meta, err := convert.RunZipRepack(source, p)
	if err != nil {
		return "", nil, err
	}
	return dest, &meta, nil
}

func (e *Engine) runFlatten(source string) (string, *convert.ConvertMetadata, error) {
	p := convert.FlattenParamsFromConfig(e.Config, e.ConvertOpts)
	if e.Run7z != nil {
		p.Run7z = e.Run7z
	}
	if strings.TrimSpace(p.SevenZipBin) == "" {
		return "", nil, mounterErrorf("flatten requires convert_7z_bin / 7z on PATH")
	}
	meta, err := convert.RunFlattenConvert(source, p, e.NeedsFlatten)
	if err != nil {
		return "", nil, err
	}
	// Flatten is in-place; mount the original path (now rewritten).
	return source, meta, nil
}

// PollConvert finishes completed convert jobs and may start mount.
func (e *Engine) PollConvert() {
	if e == nil || e.Store == nil {
		return
	}
	e.mu.Lock()
	ids := make([]string, 0, len(e.convertJobs))
	for id := range e.convertJobs {
		ids = append(ids, id)
	}
	e.mu.Unlock()

	for _, archiveID := range ids {
		e.mu.Lock()
		job := e.convertJobs[archiveID]
		e.mu.Unlock()
		if job == nil {
			continue
		}
		done, errMsg, resultPath, meta := job.snapshot()
		if !done {
			continue
		}
		e.mu.Lock()
		delete(e.convertJobs, archiveID)
		e.mu.Unlock()

		rec, err := e.Store.GetArchive(archiveID)
		if err != nil || rec == nil {
			continue
		}

		if errMsg != "" {
			failStatus := state.StatusMountFailed
			if rec.FirstMountedAt == nil {
				failStatus = state.StatusIndexFailed
			}
			attempts, retryable := NextMountAttempt(rec.MountAttempts, e.Config.MaxMountAttempts)
			_, _ = e.Store.Transition(archiveID, failStatus, state.StatusConverting, map[string]any{
				"mount_pid":       nil,
				"mount_attempts":  attempts,
				"mount_retryable": retryable,
				"last_error":      errMsg,
			}, "")
			slog.Error("archive convert failed",
				"event", "archive_convert_failed",
				"archive_id", archiveID,
				"path", job.sourcePath,
				"error", errMsg,
			)
			continue
		}

		if resultPath == "" {
			resultPath = job.sourcePath
		}
		_ = ApplyPartialIndexCleanup(rec.Status, rec.FirstMountedAt, indexPathOf(rec))
		if fresh, _ := e.Store.GetArchive(archiveID); fresh != nil {
			rec = fresh
		}

		fp, size, mtimeNs, err := fingerprintPath(resultPath, e.Config.ContentFingerprint)
		if err != nil {
			failStatus := state.StatusMountFailed
			if rec.FirstMountedAt == nil {
				failStatus = state.StatusIndexFailed
			}
			attempts, retryable := NextMountAttempt(rec.MountAttempts, e.Config.MaxMountAttempts)
			_, _ = e.Store.Transition(archiveID, failStatus, state.StatusConverting, map[string]any{
				"mount_pid":       nil,
				"mount_attempts":  attempts,
				"mount_retryable": retryable,
				"last_error":      err.Error(),
			}, "")
			continue
		}

		fields := map[string]any{
			"archive_path":     resultPath,
			"archive_basename": filepath.Base(resultPath),
			"size_bytes":       size,
			"mtime_ns":         mtimeNs,
			"fingerprint":      fp,
			"last_error":       nil,
		}
		if meta != nil {
			fields["convert_source_size_bytes"] = meta.OriginalSizeBytes
			if rec.ConvertDurationSeconds == nil && meta.ConvertDurationSeconds != nil {
				fields["convert_duration_seconds"] = *meta.ConvertDurationSeconds
			}
		} else if job.sourceSize > 0 {
			fields["convert_source_size_bytes"] = job.sourceSize
		}
		if rec.ConvertDurationSeconds == nil {
			if _, ok := fields["convert_duration_seconds"]; !ok {
				elapsed := e.now().Sub(job.startedAt).Seconds()
				if elapsed < 0 {
					elapsed = 0
				}
				fields["convert_duration_seconds"] = float64(int(elapsed*1000)) / 1000
			}
		}

		rec, err = e.Store.Transition(archiveID, state.StatusDiscovered, state.StatusConverting, fields, "")
		if err != nil {
			slog.Error("post-convert transition failed", "event", "post_convert_transition_failed", "archive_id", archiveID, "err", err)
			continue
		}
		slog.Info("archive convert done",
			"event", "archive_convert_done",
			"archive_id", archiveID,
			"path", resultPath,
			"size", size,
		)

		if e.Config.MoveArchivesToLinux && job.sourcePath != resultPath {
			e.mu.Lock()
			e.pendingSourceRemovals[archiveID] = job.sourcePath
			e.mu.Unlock()
		}

		fi := job.needsIndex
		if _, err := e.BeginMount(rec, &fi); err != nil {
			slog.Error("post-convert mount start failed", "event", "post_convert_mount_failed", "archive_id", archiveID, "err", err)
		} else {
			e.maybeFinalizeSuperseded(archiveID)
		}
	}
}

func (e *Engine) maybeFinalizeSuperseded(archiveID string) {
	e.mu.Lock()
	orig, ok := e.pendingSourceRemovals[archiveID]
	e.mu.Unlock()
	if !ok {
		return
	}
	rec, err := e.Store.GetArchive(archiveID)
	if err != nil || rec == nil {
		return
	}
	// Only remove when not still relocating.
	if e.HasRelocateJob(archiveID) {
		return
	}
	if archives.RemoveSupersededSource(e.Config, orig, rec.ArchivePath, archiveID) {
		e.mu.Lock()
		delete(e.pendingSourceRemovals, archiveID)
		e.mu.Unlock()
	}
}

// relocateSync moves the archive onto archives_dir and updates store paths.
func (e *Engine) relocateSync(rec *state.ArchiveRecord) (*state.ArchiveRecord, error) {
	source := rec.ArchivePath
	dest, err := archives.ArchiveFilePath(e.Config, rec, source)
	if err != nil {
		return nil, e.failConvertClaim(rec, err.Error())
	}
	// Already at dest with same size → just update path.
	if st, err := os.Stat(dest); err == nil && st.Mode().IsRegular() {
		if src, err2 := os.Stat(source); err2 == nil && src.Size() == st.Size() {
			updated, err := e.Store.Transition(rec.ArchiveID, rec.Status, rec.Status, map[string]any{
				"archive_path":     dest,
				"archive_basename": filepath.Base(dest),
			}, "")
			if err != nil {
				return nil, err
			}
			_ = archives.RemoveSupersededSource(e.Config, source, dest, rec.ArchiveID)
			return updated, nil
		}
	}
	srcInfo, err := os.Stat(source)
	if err != nil {
		return nil, e.failConvertClaim(rec, err.Error())
	}
	if err := archives.CheckRelocateSpace(e.Config, srcInfo.Size()); err != nil {
		return nil, e.failConvertClaim(rec, err.Error())
	}
	moved, err := archives.RelocateArchive(e.Config, rec, source)
	if err != nil {
		return nil, e.failConvertClaim(rec, err.Error())
	}
	fp, size, mtimeNs, err := fingerprintPath(moved, e.Config.ContentFingerprint)
	if err != nil {
		return nil, e.failConvertClaim(rec, err.Error())
	}
	updated, err := e.Store.Transition(rec.ArchiveID, rec.Status, rec.Status, map[string]any{
		"archive_path":     moved,
		"archive_basename": filepath.Base(moved),
		"size_bytes":       size,
		"mtime_ns":         mtimeNs,
		"fingerprint":      fp,
	}, "")
	if err != nil {
		return nil, err
	}
	_ = archives.RemoveSupersededSource(e.Config, source, moved, rec.ArchiveID)
	slog.Info("archive relocate done",
		"event", "archive_relocate_done",
		"archive_id", rec.ArchiveID,
		"src", source,
		"dest", moved,
	)
	return updated, nil
}

// PollRelocate advances async relocate jobs. v1 uses sync relocate; this is a
// no-op when no jobs are queued (API parity).
func (e *Engine) PollRelocate() {
	if e == nil || e.Store == nil {
		return
	}
	e.mu.Lock()
	ids := make([]string, 0, len(e.relocateJobs))
	for id := range e.relocateJobs {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	for _, archiveID := range ids {
		e.mu.Lock()
		job := e.relocateJobs[archiveID]
		e.mu.Unlock()
		if job == nil {
			continue
		}
		job.mu.Lock()
		done := job.done
		errMsg := job.err
		dest := job.destPath
		job.mu.Unlock()
		if !done {
			continue
		}
		e.mu.Lock()
		delete(e.relocateJobs, archiveID)
		e.mu.Unlock()

		rec, err := e.Store.GetArchive(archiveID)
		if err != nil || rec == nil {
			continue
		}
		if errMsg != "" {
			failStatus := state.StatusMountFailed
			if rec.FirstMountedAt == nil {
				failStatus = state.StatusIndexFailed
			}
			attempts, retryable := NextMountAttempt(rec.MountAttempts, e.Config.MaxMountAttempts)
			_, _ = e.Store.Transition(archiveID, failStatus, rec.Status, map[string]any{
				"mount_pid":       nil,
				"mount_attempts":  attempts,
				"mount_retryable": retryable,
				"last_error":      errMsg,
			}, "")
			continue
		}
		updated, err := e.Store.Transition(archiveID, rec.Status, rec.Status, map[string]any{
			"archive_path": dest,
		}, "")
		if err != nil {
			continue
		}
		fi := updated.FirstMountedAt == nil
		if _, err := e.BeginMount(updated, &fi); err != nil {
			slog.Error("relocated index start failed", "event", "relocated_index_start_failed", "archive_id", archiveID, "err", err)
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
