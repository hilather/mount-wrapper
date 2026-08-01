// Package api is the localhost HTTP API and SSE stream for the operator SPA.
//
// The server is embedded in `mount-wrapper serve` when web_enabled is true.
// It binds web_host:web_port (default 127.0.0.1:8787), optionally requires
// Bearer web_token on /api/* (or ?token= for GET), and serves the embedded
// SPA from internal/webui at /.
//
// Handlers depend on a Backend interface so this package never imports
// internal/service (avoids import cycles). The service package constructs an
// adapter and starts/stops the server from Service.Start / Shutdown.
//
// Destructive POSTs (purge, unmount-all, rescan) are rate-limited per client
// IP (default 2s min interval) and return 429 RATE_LIMITED when exceeded.
//
// GET /metrics exposes a small Prometheus text scrape (hand-written exposition,
// no client_golang dependency). Always registered when the HTTP server is up
// (web_enabled). Auth: open on loopback bind; on non-loopback bind, same
// web_token rules as /api/*. By default scrapes request status with
// include_sizes and emit aggregate size/savings gauges from metrics_summary
// (metrics op fallback); no per-archive series. That path can be slower than
// count-only status — set ServerOptions.PrometheusOmitSizeGauges to skip.
//
// SSE GET /api/events sends an initial snapshot, then delta events
// (counts, archive, scan, low_disk, optional metrics) when status changes
// between refresh ticks, plus a full snapshot every Nth tick for resync.
package api
