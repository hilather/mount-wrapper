package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/metrics"
	"github.com/hilather/mount-wrapper/internal/scanner"
	"github.com/hilather/mount-wrapper/internal/state"
)

// toJSONMap encodes v as JSON then unmarshals into map/slice for control responses.
func toJSONMap(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// Control request/response helpers (parity with control.ok_response / err_response).

// OKResponse wraps data as {"ok":true,"data":...}.
func OKResponse(data any) map[string]any {
	return map[string]any{"ok": true, "data": data}
}

// ErrResponse wraps an error as {"ok":false,"error":"...","code":"..."}.
func ErrResponse(msg, code string) map[string]any {
	if code == "" {
		code = "ERROR"
	}
	return map[string]any{"ok": false, "error": msg, "code": code}
}

// HandleRequest dispatches a control-plane op (JSON-friendly map).
// Acquires opMu so concurrent HTTP/direct callers cannot race Tick mutations
// of Config/engine/scanner. Control socket ServeReady uses handleRequestLocked
// instead (already under Tick's opMu).
//
// Supported ops: status, metrics, config_get, config_set, rescan, retry,
// unmount, purge, stop, reload, hooks_list, hooks_status, mount.
// Unknown ops return BAD_REQUEST.
func (s *Service) HandleRequest(req map[string]any) map[string]any {
	if s == nil {
		return ErrResponse("service not available", "UNAVAILABLE")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.handleRequestLocked(req)
}

// handleRequestLocked is the op dispatcher. Caller must hold opMu (or be the
// sole owner of Service in a test that never races Tick).
func (s *Service) handleRequestLocked(req map[string]any) map[string]any {
	if s == nil {
		return ErrResponse("service not available", "UNAVAILABLE")
	}
	op, _ := req["op"].(string)
	switch op {
	case "status":
		includeSizes := boolField(req, "include_sizes", false)
		return OKResponse(s.StatusMap(includeSizes))
	case "metrics":
		return s.opMetrics(req)
	case "config_get":
		return s.opConfigGet()
	case "config_set":
		resp := s.opConfigSet(req)
		if respOK(resp) {
			s.NotifyChange()
		}
		return resp
	case "rescan":
		assume, _ := req["assume_stable"].(bool)
		summary := s.doScan(assume)
		s.mu.Lock()
		s.lastScanAt = s.now()
		s.lastScanResult = summary
		if iso, ok := summary["finished_at"].(string); ok && iso != "" {
			s.lastScanAtISO = iso
		} else {
			s.lastScanAtISO = state.UTCNowISO()
		}
		s.mu.Unlock()
		s.NotifyChange()
		return OKResponse(summary)
	case "retry":
		resp := s.opRetry(req)
		if respOK(resp) {
			s.NotifyChange()
		}
		return resp
	case "unmount":
		resp := s.opUnmount(req)
		if respOK(resp) {
			s.NotifyChange()
		}
		return resp
	case "purge":
		resp := s.opPurge(req)
		if respOK(resp) {
			s.NotifyChange()
		}
		return resp
	case "stop":
		s.Stop()
		s.NotifyChange()
		return OKResponse(map[string]any{"stop": "scheduled"})
	case "reload":
		s.RequestReload()
		s.NotifyChange()
		return OKResponse(map[string]any{"reload": "scheduled"})
	case "hooks_list":
		return s.opHooksList()
	case "hooks_status":
		return s.opHooksStatus(req)
	case "mount":
		resp := s.opMount(req)
		if respOK(resp) {
			s.NotifyChange()
		}
		return resp
	default:
		return ErrResponse(fmt.Sprintf("unknown op %q", op), "BAD_REQUEST")
	}
}

func boolField(req map[string]any, key string, def bool) bool {
	v, ok := req[key]
	if !ok || v == nil {
		return def
	}
	b, ok := v.(bool)
	if ok {
		return b
	}
	return def
}

func respOK(resp map[string]any) bool {
	if resp == nil {
		return false
	}
	ok, _ := resp["ok"].(bool)
	return ok
}

func (s *Service) opMetrics(req map[string]any) map[string]any {
	if s.Metrics == nil {
		return ErrResponse("metrics not available", "UNAVAILABLE")
	}
	preferMount := boolField(req, "prefer_mount", false)
	noCache := boolField(req, "no_cache", false)
	useCache := !noCache
	opts := metrics.QueryOptions{
		PreferMount: preferMount,
		UseCache:    metrics.BoolPtr(useCache),
	}
	archiveID, _ := req["archive_id"].(string)
	if archiveID != "" {
		m, err := s.Metrics.GetOne(archiveID, opts)
		if err != nil {
			return ErrResponse(err.Error(), "ERROR")
		}
		if m == nil {
			return ErrResponse("archive not found", "NOT_FOUND")
		}
		return OKResponse(map[string]any{"metrics": toJSONMap(m)})
	}
	items, err := s.Metrics.GetAll(opts, nil)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	list := make([]any, 0, len(items))
	for i := range items {
		list = append(list, toJSONMap(&items[i]))
	}
	sum, err := s.Metrics.Summary(items, opts)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	return OKResponse(map[string]any{
		"metrics": list,
		"summary": toJSONMap(sum),
	})
}

func (s *Service) opConfigGet() map[string]any {
	if s.Config == nil {
		return ErrResponse("no config", "CONFIG_ERROR")
	}
	data := config.Snapshot(s.Config)
	// Prefer in-memory effective config.
	data["config"] = config.ToPublicMap(s.Config)
	if s.Config.ConfigPath != "" {
		data["config_path"] = s.Config.ConfigPath
	}
	return OKResponse(data)
}

func (s *Service) opConfigSet(req map[string]any) map[string]any {
	if s.Config == nil {
		return ErrResponse("no config", "CONFIG_ERROR")
	}
	apply := boolField(req, "apply", true)
	if _, ok := req["apply"]; !ok {
		apply = true
	}
	var full, patch map[string]any
	if c, ok := req["config"].(map[string]any); ok {
		full = c
	}
	if p, ok := req["patch"].(map[string]any); ok {
		patch = p
	}
	result, err := config.ApplyUpdate(s.Config, patch, full, apply, s.Config.ConfigPath)
	if err != nil {
		return ErrResponse(err.Error(), "CONFIG_ERROR")
	}
	if apply {
		if written, _ := result["written"].(bool); written {
			// Keep pointer identity: load new config into the same *Config so
			// Engine/Scanner/Cleaner see updated values.
			path := s.Config.ConfigPath
			if path != "" {
				if newCfg, err := config.Load(path); err == nil {
					*s.Config = *newCfg
					result["reloaded"] = true
				} else {
					result["reloaded"] = false
					result["reload_error"] = err.Error()
				}
			}
			// Live-apply subsystems once here. Do not also RequestReload —
			// that would double-run doReload on the next Tick.
			s.doReload()
			result["reload_scheduled"] = false
		}
	}
	return OKResponse(result)
}

func (s *Service) opRetry(req map[string]any) map[string]any {
	id, _ := req["archive_id"].(string)
	if id == "" {
		return ErrResponse("archive_id required", "BAD_REQUEST")
	}
	rec, err := s.Store.GetArchive(id)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	if rec == nil {
		return ErrResponse("archive not found", "NOT_FOUND")
	}
	updated, err := s.Store.ResetMountAttempts(id, true, "")
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	return OKResponse(archiveDict(updated))
}

func (s *Service) opUnmount(req map[string]any) map[string]any {
	if all, _ := req["all"].(bool); all {
		results := []map[string]any{}
		recs, err := s.Store.ListArchives(nil)
		if err != nil {
			return ErrResponse(err.Error(), "ERROR")
		}
		for _, rec := range recs {
			switch rec.Status {
			case state.StatusMounted, state.StatusHooksRunning, state.StatusIndexing, state.StatusMounting:
				out, err := s.Engine.Unmount(rec.ArchiveID, false)
				if err != nil {
					results = append(results, map[string]any{"archive_id": rec.ArchiveID, "error": err.Error()})
				} else {
					results = append(results, archiveDict(out))
				}
			}
		}
		return OKResponse(map[string]any{"unmounted": results})
	}
	target, _ := req["target"].(string)
	if target == "" {
		return ErrResponse("target or all required", "BAD_REQUEST")
	}
	rec := s.resolveTarget(target)
	if rec == nil {
		return ErrResponse(fmt.Sprintf("not found: %s", target), "NOT_FOUND")
	}
	out, err := s.Engine.Unmount(rec.ArchiveID, false)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	return OKResponse(archiveDict(out))
}

func (s *Service) opPurge(req map[string]any) map[string]any {
	id, _ := req["archive_id"].(string)
	if id == "" {
		return ErrResponse("archive_id required", "BAD_REQUEST")
	}
	// Require explicit confirmation when present.
	if yes, ok := req["yes"].(bool); ok && !yes {
		return ErrResponse("purge requires yes=true", "BAD_REQUEST")
	}
	result := s.Cleaner.PurgeArchive(id, true)
	if !result.OK {
		code := "ERROR"
		if result.Error == "" {
			result.Error = "purge failed"
		}
		return ErrResponse(result.Error, code)
	}
	return OKResponse(map[string]any{
		"archive_id":     result.ArchiveID,
		"index_deleted":  result.IndexDeleted,
		"overlay_action": result.OverlayAction,
		"mount_cleaned":  result.MountCleaned,
	})
}

func (s *Service) opHooksList() map[string]any {
	list := []map[string]any{}
	if s.Hooks != nil {
		for _, h := range s.Hooks.ListHooks() {
			list = append(list, map[string]any{"name": h.Name, "path": h.Path})
		}
	}
	return OKResponse(map[string]any{"hooks": list})
}

func (s *Service) opHooksStatus(req map[string]any) map[string]any {
	id, _ := req["archive_id"].(string)
	if id == "" {
		return ErrResponse("archive_id required", "BAD_REQUEST")
	}
	rec, err := s.Store.GetArchive(id)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	if rec == nil {
		return ErrResponse("archive not found", "NOT_FOUND")
	}
	hooks, err := s.Store.ListHooks(id)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	rows := make([]map[string]any, 0, len(hooks))
	for _, h := range hooks {
		row := map[string]any{
			"hook_name": h.HookName,
			"status":    h.Status,
			"attempts":  h.Attempts,
		}
		if h.LastExitCode != nil {
			row["last_exit_code"] = *h.LastExitCode
		}
		if h.LastError != nil {
			row["last_error"] = *h.LastError
		}
		rows = append(rows, row)
	}
	return OKResponse(map[string]any{
		"archive_id":   id,
		"hooks_status": rec.HooksStatus,
		"hooks":        rows,
	})
}

func (s *Service) opMount(req map[string]any) map[string]any {
	path, _ := req["path"].(string)
	if path == "" {
		return ErrResponse("path required", "BAD_REQUEST")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	st, err := os.Stat(abs)
	if err != nil || !st.Mode().IsRegular() {
		return ErrResponse(fmt.Sprintf("not a file: %s", abs), "NOT_FOUND")
	}
	rec, err := s.Store.GetArchiveByPath(abs)
	if err != nil {
		return ErrResponse(err.Error(), "ERROR")
	}
	if rec == nil {
		fp, err := scanner.ComputeFingerprint(abs, st.Size(), st.ModTime().UnixNano(), s.Config.ContentFingerprint)
		if err != nil {
			return ErrResponse(err.Error(), "ERROR")
		}
		rec, err = s.Store.InsertDiscovered(state.InsertDiscoveredParams{
			SourceDir:       filepath.Dir(abs),
			ArchivePath:     abs,
			ArchiveBasename: filepath.Base(abs),
			SizeBytes:       st.Size(),
			MtimeNs:         st.ModTime().UnixNano(),
			Fingerprint:     fp,
		})
		if err != nil {
			return ErrResponse(err.Error(), "ERROR")
		}
		if s.Scanner != nil && s.Scanner.Gate != nil {
			s.Scanner.Gate.Check(abs, st.Size(), st.ModTime().UnixNano(), s.now(), true)
		}
	}
	first := rec.FirstMountedAt == nil
	managed, err := s.Engine.BeginMount(rec, &first)
	if err != nil {
		return ErrResponse(err.Error(), "MOUNT_FAILED")
	}
	rec, _ = s.Store.GetArchive(rec.ArchiveID)
	if managed == nil {
		status := ""
		if rec != nil {
			status = rec.Status
		}
		return OKResponse(map[string]any{
			"archive_id": rec.ArchiveID,
			"status":     status,
			"queued":     true,
		})
	}
	status := "indexing"
	if managed.Phase == "mount" {
		status = "mounting"
	}
	return OKResponse(map[string]any{
		"archive_id": managed.ArchiveID,
		"pid":        managed.PID,
		"mount_path": managed.Request.MountPath,
		"status":     status,
	})
}
