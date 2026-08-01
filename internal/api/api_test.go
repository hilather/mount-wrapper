package api_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/api"
	"github.com/hilather/mount-wrapper/internal/config"
)

// fakeBackend is a minimal Backend for API unit tests.
type fakeBackend struct {
	mu      sync.Mutex
	version string
	cfg     *config.Config
	// ops records HandleRequest ops for assertions.
	ops []string
	// status is returned for op=status.
	status map[string]any
	// metrics is returned for op=metrics.
	metrics map[string]any
	// failOp returns UNAVAILABLE for this op when set.
	failOp string
	// statusFn if set is called under lock to produce the status map
	// (lets tests mutate between SSE polls).
	statusFn func() map[string]any
	// notify is an optional ChangeNotifier channel.
	notify chan struct{}
}

func (f *fakeBackend) HandleRequest(req map[string]any) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	op, _ := req["op"].(string)
	f.ops = append(f.ops, op)
	if f.failOp != "" && op == f.failOp {
		return map[string]any{"ok": false, "error": "down", "code": "UNAVAILABLE"}
	}
	switch op {
	case "status":
		var st map[string]any
		if f.statusFn != nil {
			st = f.statusFn()
		} else {
			st = f.status
		}
		if st == nil {
			st = map[string]any{
				"version":  f.version,
				"pid":      42,
				"counts":   map[string]any{"mounted": 1, "discovered": 0},
				"mounted":  1,
				"archives": []any{},
				"low_disk": false,
			}
		}
		// Shallow-copy top-level so SSE prev/curr diffs stay independent of later mutations.
		cp := make(map[string]any, len(st))
		for k, v := range st {
			cp[k] = v
		}
		return map[string]any{"ok": true, "data": cp}
	case "metrics":
		m := f.metrics
		if m == nil {
			m = map[string]any{
				"metrics": []any{},
				"summary": map[string]any{"archive_count": 0},
			}
		}
		return map[string]any{"ok": true, "data": m}
	case "config_get":
		return map[string]any{"ok": true, "data": map[string]any{
			"config": map[string]any{"web_enabled": true},
		}}
	case "rescan":
		return map[string]any{"ok": true, "data": map[string]any{"discovered": 0}}
	case "retry":
		return map[string]any{"ok": true, "data": map[string]any{"archive_id": req["archive_id"]}}
	case "unmount":
		return map[string]any{"ok": true, "data": map[string]any{"unmounted": true}}
	case "purge":
		return map[string]any{"ok": true, "data": map[string]any{"archive_id": req["archive_id"]}}
	case "config_set":
		return map[string]any{"ok": true, "data": map[string]any{"valid": true, "apply": req["apply"]}}
	case "hooks_list":
		return map[string]any{"ok": true, "data": map[string]any{
			"hooks": []any{
				map[string]any{"name": "notify.sh", "path": "/etc/mount-wrapper/hooks.d/notify.sh"},
			},
		}}
	case "hooks_status":
		id, _ := req["archive_id"].(string)
		if id == "" {
			return map[string]any{"ok": false, "error": "archive_id required", "code": "BAD_REQUEST"}
		}
		if id == "missing" {
			return map[string]any{"ok": false, "error": "archive not found", "code": "NOT_FOUND"}
		}
		return map[string]any{"ok": true, "data": map[string]any{
			"archive_id":   id,
			"hooks_status": "success",
			"hooks": []any{
				map[string]any{
					"hook_name":      "notify.sh",
					"status":         "success",
					"attempts":       1,
					"last_exit_code": 0,
				},
			},
		}}
	default:
		return map[string]any{"ok": false, "error": "unknown op", "code": "BAD_REQUEST"}
	}
}

func (f *fakeBackend) Version() string {
	if f.version == "" {
		return "test"
	}
	return f.version
}

func (f *fakeBackend) Config() *config.Config {
	if f.cfg != nil {
		return f.cfg
	}
	return &config.Config{
		WebHost:       "127.0.0.1",
		WebPort:       8787,
		MountRoot:     "/var/lib/mount-wrapper/mounts",
		ControlSocket: "/run/mount-wrapper/control.sock",
	}
}

// Notify implements api.ChangeNotifier when notify is non-nil.
func (f *fakeBackend) Notify() <-chan struct{} {
	if f.notify == nil {
		return nil
	}
	return f.notify
}

func newTestServer(t *testing.T, token string, be *fakeBackend) *api.Server {
	t.Helper()
	if be == nil {
		be = &fakeBackend{version: "test-1"}
	}
	return api.New(be, api.ServerOptions{
		Bind:                   "127.0.0.1:0",
		Token:                  token,
		Version:                "web-test",
		SSEInterval:            50 * time.Millisecond,
		HeartbeatInterval:      200 * time.Millisecond,
		SSEFullSnapshotEvery:   4,
		DestructiveMinInterval: -1, // disable; ratelimit_test.go covers limits
	})
}

func TestAuth401WithoutToken(t *testing.T) {
	srv := newTestServer(t, "secret-token", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", res.StatusCode)
	}
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	if body["code"] != "UNAUTHORIZED" {
		t.Fatalf("body=%v", body)
	}
}

func TestAuthBearerAndQueryToken(t *testing.T) {
	srv := newTestServer(t, "secret-token", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Bearer
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("bearer status=%d", res.StatusCode)
	}

	// Query token
	res, err = http.Get(ts.URL + "/api/health?token=secret-token")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("query token status=%d", res.StatusCode)
	}

	// Wrong token
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/health", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d", res.StatusCode)
	}
}

func TestAuthDisabledWhenTokenEmpty(t *testing.T) {
	srv := newTestServer(t, "", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestHealthAndStatus(t *testing.T) {
	be := &fakeBackend{version: "svc-9"}
	srv := newTestServer(t, "", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health status=%d", res.StatusCode)
	}
	var health map[string]any
	if err := json.NewDecoder(res.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health["ok"] != true {
		t.Fatalf("health=%v", health)
	}
	if health["service_reachable"] != true {
		t.Fatalf("expected reachable: %v", health)
	}
	if health["web_version"] != "web-test" {
		t.Fatalf("web_version=%v", health["web_version"])
	}
	if health["service_version"] != "svc-9" {
		t.Fatalf("service_version=%v", health["service_version"])
	}

	res2, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res2.StatusCode)
	}
	var st map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st["version"] != "svc-9" {
		t.Fatalf("status version=%v", st["version"])
	}
	if st["pid"] == nil {
		t.Fatal("missing pid")
	}
}

func TestStatusSizesAndArchivesMerge(t *testing.T) {
	be := &fakeBackend{
		version: "v",
		status: map[string]any{
			"version": "v",
			"pid":     1,
			"counts":  map[string]any{"mounted": 1},
			"mounted": 1,
			"archives": []any{
				map[string]any{"archive_id": "a1", "status": "mounted"},
			},
		},
		metrics: map[string]any{
			"metrics": []any{
				map[string]any{"archive_id": "a1", "archive_size_bytes": 100},
			},
			"summary": map[string]any{"archive_count": 1},
		},
	}
	srv := newTestServer(t, "", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/api/status/sizes")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status/sizes=%d", res.StatusCode)
	}

	res, err = http.Get(ts.URL + "/api/archives")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	archives, ok := body["archives"].([]any)
	if !ok || len(archives) != 1 {
		t.Fatalf("archives=%v", body["archives"])
	}
	row, _ := archives[0].(map[string]any)
	if row["metrics"] == nil {
		t.Fatalf("expected metrics merge: %v", row)
	}
	if body["summary"] == nil {
		t.Fatal("expected summary")
	}
}

func TestActionPosts(t *testing.T) {
	be := &fakeBackend{version: "v"}
	srv := newTestServer(t, "", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	post := func(path, body string) int {
		t.Helper()
		res, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}

	if c := post("/api/rescan", `{"assume_stable":true}`); c != 200 {
		t.Fatalf("rescan %d", c)
	}
	if c := post("/api/retry", `{"archive_id":"x"}`); c != 200 {
		t.Fatalf("retry %d", c)
	}
	if c := post("/api/unmount", `{"all":true}`); c != 200 {
		t.Fatalf("unmount %d", c)
	}
	// Validate missing yes before a successful purge (rate-limit is per admit).
	if c := post("/api/purge", `{"archive_id":"x"}`); c != 400 {
		t.Fatalf("purge without yes want 400 got %d", c)
	}
	if c := post("/api/purge", `{"archive_id":"x","yes":true}`); c != 200 {
		t.Fatalf("purge %d", c)
	}
	if c := post("/api/config", `{"patch":{"log_level":"DEBUG"},"apply":false}`); c != 200 {
		t.Fatalf("config %d", c)
	}
}

func TestHooksListAndStatus(t *testing.T) {
	be := &fakeBackend{version: "v"}
	srv := newTestServer(t, "", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// List discovered hooks when archive_id omitted.
	res, err := http.Get(ts.URL + "/api/hooks")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("hooks list status=%d", res.StatusCode)
	}
	var listBody map[string]any
	if err := json.NewDecoder(res.Body).Decode(&listBody); err != nil {
		t.Fatal(err)
	}
	hooks, ok := listBody["hooks"].([]any)
	if !ok || len(hooks) != 1 {
		t.Fatalf("hooks list body=%v", listBody)
	}

	// Per-archive status.
	res2, err := http.Get(ts.URL + "/api/hooks?archive_id=abc123")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("hooks status=%d", res2.StatusCode)
	}
	var statusBody map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody["archive_id"] != "abc123" {
		t.Fatalf("archive_id=%v", statusBody["archive_id"])
	}
	if statusBody["hooks_status"] != "success" {
		t.Fatalf("hooks_status=%v", statusBody["hooks_status"])
	}
	rows, ok := statusBody["hooks"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("hooks rows=%v", statusBody["hooks"])
	}
	row, _ := rows[0].(map[string]any)
	if row["hook_name"] != "notify.sh" || row["status"] != "success" {
		t.Fatalf("row=%v", row)
	}

	// Missing archive → 404
	res3, err := http.Get(ts.URL + "/api/hooks?archive_id=missing")
	if err != nil {
		t.Fatal(err)
	}
	defer res3.Body.Close()
	if res3.StatusCode != http.StatusNotFound {
		t.Fatalf("missing archive status=%d want 404", res3.StatusCode)
	}

	// Method not allowed
	res4, err := http.Post(ts.URL+"/api/hooks?archive_id=x", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res4.Body.Close()
	if res4.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST hooks status=%d want 405", res4.StatusCode)
	}

	be.mu.Lock()
	ops := append([]string(nil), be.ops...)
	be.mu.Unlock()
	wantOps := map[string]bool{"hooks_list": false, "hooks_status": false}
	for _, op := range ops {
		if _, ok := wantOps[op]; ok {
			wantOps[op] = true
		}
	}
	if !wantOps["hooks_list"] || !wantOps["hooks_status"] {
		t.Fatalf("expected hooks_list and hooks_status ops, got %v", ops)
	}
}

func TestDoctorAndWSLInfo(t *testing.T) {
	be := &fakeBackend{
		version: "v",
		cfg: &config.Config{
			MountRoot: "/var/lib/mount-wrapper/mounts",
			WebHost:   "127.0.0.1",
			WebPort:   8787,
		},
	}
	srv := newTestServer(t, "", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/api/doctor")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("doctor %d", res.StatusCode)
	}
	var report map[string]any
	if err := json.NewDecoder(res.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if _, ok := report["checks"]; !ok {
		t.Fatalf("doctor missing checks: %v", report)
	}

	t.Setenv("WSL_DISTRO_NAME", "Ubuntu-22.04")
	res2, err := http.Get(ts.URL + "/api/wsl-info")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	var wsl map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&wsl); err != nil {
		t.Fatal(err)
	}
	if wsl["distro_name"] != "Ubuntu-22.04" {
		t.Fatalf("distro=%v", wsl["distro_name"])
	}
	unc, _ := wsl["unc_mounts"].(string)
	if !strings.Contains(unc, `\\wsl.localhost\Ubuntu-22.04`) {
		t.Fatalf("unc=%v", unc)
	}
}

func TestSSESnapshotOnConnect(t *testing.T) {
	be := &fakeBackend{version: "sse-v"}
	srv := newTestServer(t, "", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("events status=%d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type=%s", ct)
	}

	// Read until we see the snapshot event.
	reader := bufio.NewReader(res.Body)
	deadline := time.Now().Add(2 * time.Second)
	var sawSnapshot bool
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			// timeout via client not set; break on other errors
			break
		}
		if strings.HasPrefix(line, "event: snapshot") {
			sawSnapshot = true
			// next data line
			dataLine, _ := reader.ReadString('\n')
			if !strings.HasPrefix(dataLine, "data: ") {
				t.Fatalf("expected data after snapshot, got %q", dataLine)
			}
			payload := strings.TrimPrefix(strings.TrimSpace(dataLine), "data: ")
			var snap map[string]any
			if err := json.Unmarshal([]byte(payload), &snap); err != nil {
				t.Fatal(err)
			}
			if snap["version"] != "sse-v" && snap["ok"] != true {
				t.Logf("snapshot=%v", snap)
			}
			break
		}
	}
	if !sawSnapshot {
		t.Fatal("did not receive snapshot event")
	}
}

// readSSEEvents collects named SSE events until deadline or maxEvents.
func readSSEEvents(t *testing.T, r io.Reader, maxEvents int, deadline time.Time) []sseEvent {
	t.Helper()
	reader := bufio.NewReader(r)
	var out []sseEvent
	var curEvent string
	for time.Now().Before(deadline) && (maxEvents <= 0 || len(out) < maxEvents) {
		// Use a short read timeout via deadline check; blocking ReadString
		// is interrupted when the test closes the body.
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "event: ") {
			curEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") && curEvent != "" {
			payload := strings.TrimPrefix(line, "data: ")
			var data map[string]any
			_ = json.Unmarshal([]byte(payload), &data)
			out = append(out, sseEvent{Event: curEvent, Data: data, Raw: payload})
			curEvent = ""
		}
	}
	return out
}

type sseEvent struct {
	Event string
	Data  map[string]any
	Raw   string
}

func TestSSEDeltasOnStatusChange(t *testing.T) {
	// Status evolves: first poll indexing, then mounted + low_disk + new scan.
	var phase int
	be := &fakeBackend{
		version: "sse-delta",
		statusFn: func() map[string]any {
			phase++
			if phase <= 1 {
				return map[string]any{
					"version":  "sse-delta",
					"pid":      1,
					"counts":   map[string]any{"mounted": 0, "indexing": 1},
					"mounted":  0,
					"indexing": 1,
					"low_disk": false,
					"archives": []any{
						map[string]any{
							"archive_id":     "a1",
							"status":         "indexing",
							"progress_label": "building index",
							"elapsed_s":      1.0,
						},
					},
					"last_scan_at": "2026-01-01T00:00:00Z",
				}
			}
			return map[string]any{
				"version":  "sse-delta",
				"pid":      1,
				"counts":   map[string]any{"mounted": 1, "indexing": 0},
				"mounted":  1,
				"indexing": 0,
				"low_disk": true,
				"archives": []any{
					map[string]any{
						"archive_id":     "a1",
						"status":         "mounted",
						"progress_label": "",
						"elapsed_s":      nil,
						"mount_path":     "/mnt/a1",
					},
				},
				"last_scan_at": "2026-01-01T00:00:05Z",
				"last_scan":    map[string]any{"seen": 1},
			}
		},
	}
	srv := api.New(be, api.ServerOptions{
		Bind:                 "127.0.0.1:0",
		Version:              "web-test",
		SSEInterval:          30 * time.Millisecond,
		HeartbeatInterval:    time.Hour, // avoid heartbeat noise
		SSEFullSnapshotEvery: 100,       // focus on deltas
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Client with timeout so the stream does not hang the test forever.
	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}

	// Collect enough events: initial snapshot + later deltas.
	events := readSSEEvents(t, res.Body, 12, time.Now().Add(2*time.Second))
	byType := map[string]int{}
	for _, e := range events {
		byType[e.Event]++
	}
	if byType["snapshot"] < 1 {
		t.Fatalf("expected initial snapshot, events=%v", eventNames(events))
	}
	if byType["archive"] < 1 {
		t.Fatalf("expected archive delta, events=%v types=%v", eventNames(events), byType)
	}
	if byType["counts"] < 1 {
		t.Fatalf("expected counts delta, events=%v", eventNames(events))
	}
	if byType["low_disk"] < 1 {
		t.Fatalf("expected low_disk delta, events=%v", eventNames(events))
	}
	if byType["scan"] < 1 {
		t.Fatalf("expected scan delta, events=%v", eventNames(events))
	}

	// Auth still required when token set.
	srvAuth := api.New(be, api.ServerOptions{
		Bind:        "127.0.0.1:0",
		Token:       "secret",
		SSEInterval: time.Hour,
	})
	tsAuth := httptest.NewServer(srvAuth.Handler())
	t.Cleanup(tsAuth.Close)
	res401, err := http.Get(tsAuth.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	res401.Body.Close()
	if res401.StatusCode != http.StatusUnauthorized {
		t.Fatalf("events without token status=%d", res401.StatusCode)
	}
}

func TestSSEFullSnapshotEveryNth(t *testing.T) {
	be := &fakeBackend{
		version: "n",
		// Unchanging status so only periodic full snapshots appear after connect.
		status: map[string]any{
			"version":  "n",
			"pid":      1,
			"counts":   map[string]any{"mounted": 0},
			"mounted":  0,
			"archives": []any{},
			"low_disk": false,
		},
	}
	srv := api.New(be, api.ServerOptions{
		Bind:                 "127.0.0.1:0",
		SSEInterval:          25 * time.Millisecond,
		HeartbeatInterval:    time.Hour,
		SSEFullSnapshotEvery: 2,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	events := readSSEEvents(t, res.Body, 6, time.Now().Add(1500*time.Millisecond))
	snaps := 0
	for _, e := range events {
		if e.Event == "snapshot" {
			snaps++
		}
	}
	// Initial + at least one resync (every 2nd tick).
	if snaps < 2 {
		t.Fatalf("expected >=2 snapshots (connect+resync), got %d events=%v", snaps, eventNames(events))
	}
}

// TestSSENotifyWake verifies ChangeNotifier wakes SSE earlier than the ticker.
func TestSSENotifyWake(t *testing.T) {
	notify := make(chan struct{}, 1)
	var phase int
	be := &fakeBackend{
		version: "push",
		notify:  notify,
		statusFn: func() map[string]any {
			phase++
			mounted := 0
			if phase > 1 {
				mounted = 1
			}
			return map[string]any{
				"version":  "push",
				"pid":      1,
				"counts":   map[string]any{"mounted": mounted},
				"mounted":  mounted,
				"archives": []any{},
				"low_disk": false,
			}
		},
	}
	// Ticker is very slow so only Notify can produce a counts delta quickly.
	srv := api.New(be, api.ServerOptions{
		Bind:                 "127.0.0.1:0",
		SSEInterval:          time.Hour,
		HeartbeatInterval:    time.Hour,
		SSEFullSnapshotEvery: 100,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/events", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	// Wait for initial snapshot to be consumed by read loop start.
	time.Sleep(50 * time.Millisecond)
	notify <- struct{}{}

	events := readSSEEvents(t, res.Body, 4, time.Now().Add(1500*time.Millisecond))
	byType := map[string]int{}
	for _, e := range events {
		byType[e.Event]++
	}
	if byType["snapshot"] < 1 {
		t.Fatalf("expected snapshot, events=%v", eventNames(events))
	}
	if byType["counts"] < 1 {
		t.Fatalf("expected counts delta from Notify wake, events=%v types=%v", eventNames(events), byType)
	}
}

func eventNames(events []sseEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Event
	}
	return out
}

func TestSPAIndex(t *testing.T) {
	srv := newTestServer(t, "tok", nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("index status=%d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "mount-wrapper") && !strings.Contains(string(body), "<div id=\"app\">") {
		// Placeholder or built SPA both OK as long as HTML
		if !strings.Contains(string(body), "<html") && !strings.Contains(string(body), "<!doctype") && !strings.Contains(string(body), "<!DOCTYPE") {
			t.Fatalf("unexpected body: %s", truncate(string(body), 200))
		}
	}
	if !strings.Contains(string(body), "__MOUNT_WRAPPER_TOKEN__") {
		t.Fatal("expected token injection in index.html")
	}
}

func TestControlUnavailableMaps503(t *testing.T) {
	be := &fakeBackend{version: "v", failOp: "status"}
	srv := newTestServer(t, "", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", res.StatusCode)
	}
}

func TestPrometheusMetricsScrape(t *testing.T) {
	be := &fakeBackend{
		version: "1.2.3-test",
		status: map[string]any{
			"version": "1.2.3-test",
			"counts": map[string]any{
				"mounted":    2,
				"discovered": 1,
				"indexing":   0,
				"converting": 0,
				"absent":     3,
			},
			"low_disk":     true,
			"last_scan_at": "2026-01-15T12:00:00Z",
			"mounted":      2,
		},
	}
	// Loopback bind + token: /metrics still open without Bearer (scrape policy).
	srv := newTestServer(t, "secret-token", be)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type=%q want text/plain", ct)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)

	for _, needle := range []string{
		"mount_wrapper_archives{",
		`mount_wrapper_archives{status="mounted"} 2`,
		`mount_wrapper_archives{status="discovered"} 1`,
		`mount_wrapper_archives{status="absent"} 3`,
		`mount_wrapper_archives{status="indexing"} 0`,
		"mount_wrapper_low_disk 1",
		"mount_wrapper_last_scan_timestamp_seconds ",
		`mount_wrapper_info{version="1.2.3-test"} 1`,
		"# TYPE mount_wrapper_archives gauge",
		"# TYPE mount_wrapper_low_disk gauge",
		"# TYPE mount_wrapper_last_scan_timestamp_seconds gauge",
		"# TYPE mount_wrapper_info gauge",
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", needle, text)
		}
	}
	// last_scan_at 2026-01-15T12:00:00Z → Unix seconds (allow float form).
	foundScan := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "mount_wrapper_last_scan_timestamp_seconds ") {
			foundScan = true
			if !strings.Contains(line, "1768478400") {
				t.Errorf("last_scan line=%q want unix 1768478400", line)
			}
		}
	}
	if !foundScan {
		t.Error("missing mount_wrapper_last_scan_timestamp_seconds line")
	}

	// /api/* still requires token when set.
	res2, err := http.Get(ts.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/health without token status=%d want 401", res2.StatusCode)
	}
}

func TestPrometheusMetricsAuthNonLoopback(t *testing.T) {
	be := &fakeBackend{
		version: "v",
		status: map[string]any{
			"counts":   map[string]any{"mounted": 1},
			"low_disk": false,
		},
	}
	srv := api.New(be, api.ServerOptions{
		Bind:                   "0.0.0.0:8787",
		Token:                  "secret-token",
		Version:                "web-test",
		SSEInterval:            50 * time.Millisecond,
		HeartbeatInterval:      200 * time.Millisecond,
		DestructiveMinInterval: -1,
	})
	// Use Handler() directly; bind host drives auth, not the test server address.
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	res, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("non-loopback without token status=%d want 401", res.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/metrics", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("non-loopback with token status=%d want 200", res2.StatusCode)
	}
	body, _ := io.ReadAll(res2.Body)
	if !strings.Contains(string(body), "mount_wrapper_low_disk 0") {
		t.Fatalf("unexpected body: %s", truncate(string(body), 300))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
