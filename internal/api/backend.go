package api

import "github.com/hilather/mount-wrapper/internal/config"

// Backend is the daemon surface the HTTP API needs.
//
// Implemented by service via a thin adapter so api never imports service.
// HandleRequest uses the same control-plane map protocol as the Unix socket
// (ops: status, metrics, config_get, config_set, rescan, unmount, retry, purge,
// hooks_list, hooks_status, …).
type Backend interface {
	// HandleRequest dispatches a control-plane op. Returns
	// {"ok":true,"data":…} or {"ok":false,"error":…,"code":…}.
	HandleRequest(req map[string]any) map[string]any
	// Version is the daemon version string (for /api/health).
	Version() string
	// Config returns the effective live config (doctor, wsl-info, bind metadata).
	// May be nil only in tests; handlers tolerate empty config.
	Config() *config.Config
}

// ChangeNotifier is an optional Backend capability. When implemented, SSE
// clients also wake on each receive from Notify() (in addition to the refresh
// ticker) so status changes can push sooner than SSEInterval.
//
// Implementations should use a non-blocking broadcast pattern (e.g. buffered
// channel of size 1, send selects default) so a slow client never stalls the
// daemon. Ticker-only backends need not implement this.
type ChangeNotifier interface {
	Notify() <-chan struct{}
}
