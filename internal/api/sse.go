package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleEvents serves GET /api/events as an SSE stream.
//
// On connect: event snapshot (full status).
// On each refresh tick (and optional Backend Notify): diff vs previous snapshot and emit
//   counts | archive | scan | metrics | low_disk when data changes.
// Every SSEFullSnapshotEvery refresh: full snapshot for client resync.
// Heartbeat: SSE comment lines + named heartbeat event so proxies keep the connection open.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed", "code": "BAD_REQUEST"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error": "streaming unsupported",
			"code":  "ERROR",
		})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Initial full snapshot (also seeds prev for diffs).
	prev := s.snapshotPayload()
	if err := s.writeSSEEvent(w, flusher, "snapshot", prev); err != nil {
		return
	}

	refresh := time.NewTicker(s.opts.SSEInterval)
	heartbeat := time.NewTicker(s.opts.HeartbeatInterval)
	defer refresh.Stop()
	defer heartbeat.Stop()

	var notify <-chan struct{}
	if n, ok := s.backend.(ChangeNotifier); ok {
		notify = n.Notify()
	}

	fullEvery := s.opts.SSEFullSnapshotEvery
	if fullEvery <= 0 {
		fullEvery = DefaultSSEFullSnapshotEvery
	}
	tick := 0

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			// Comment-style heartbeat (and a named event for clients that prefer it).
			if _, err := fmt.Fprintf(w, ": heartbeat %s\n\n", time.Now().UTC().Format(time.RFC3339)); err != nil {
				return
			}
			if err := s.writeSSEEvent(w, flusher, "heartbeat", map[string]any{
				"ts": time.Now().UTC().Format(time.RFC3339),
			}); err != nil {
				return
			}
		case <-notify:
			if err := s.emitSSERefresh(w, flusher, &prev, &tick, fullEvery, false); err != nil {
				return
			}
		case <-refresh.C:
			if err := s.emitSSERefresh(w, flusher, &prev, &tick, fullEvery, true); err != nil {
				return
			}
		}
	}
}

// emitSSERefresh polls status, emits delta events, and periodically a full snapshot.
// countTowardFull is true for ticker wakes (advance full-snapshot counter); Notify
// wakes emit deltas only so push noise does not delay the resync schedule.
func (s *Server) emitSSERefresh(w http.ResponseWriter, flusher http.Flusher, prev *map[string]any, tick *int, fullEvery int, countTowardFull bool) error {
	curr := s.snapshotPayload()
	delta := DiffSnapshots(*prev, curr)

	if delta.Counts != nil {
		if err := s.writeSSEEvent(w, flusher, "counts", delta.Counts); err != nil {
			return err
		}
	}
	if payload := delta.ArchiveEventPayload(); payload != nil {
		if err := s.writeSSEEvent(w, flusher, "archive", payload); err != nil {
			return err
		}
	}
	if delta.Scan != nil {
		if err := s.writeSSEEvent(w, flusher, "scan", delta.Scan); err != nil {
			return err
		}
	}
	if delta.Metrics != nil {
		if err := s.writeSSEEvent(w, flusher, "metrics", delta.Metrics); err != nil {
			return err
		}
	}
	if delta.LowDisk != nil {
		if err := s.writeSSEEvent(w, flusher, "low_disk", delta.LowDisk); err != nil {
			return err
		}
	}

	if countTowardFull {
		*tick++
		if *tick >= fullEvery {
			*tick = 0
			if err := s.writeSSEEvent(w, flusher, "snapshot", curr); err != nil {
				return err
			}
		}
	}

	// Advance baseline only when curr looks usable; keep prev on soft errors.
	if curr != nil {
		if ok, has := curr["ok"].(bool); !has || ok {
			*prev = curr
		}
	}
	return nil
}

func (s *Server) snapshotPayload() map[string]any {
	req := map[string]any{"op": "status"}
	if s.opts.SSEIncludeSizes {
		req["include_sizes"] = true
	}
	status, body := unwrapControl(s.backend.HandleRequest(req))
	if status != http.StatusOK {
		return map[string]any{
			"ok":    false,
			"error": body,
		}
	}
	if m := asMap(body); m != nil {
		m["ok"] = true
		return m
	}
	return map[string]any{"ok": true, "data": body}
}

func (s *Server) writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
