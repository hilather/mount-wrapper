// Package service is the main serve loop: scanner, engine, reconcile, cleaner,
// hooks, pidfile, signals, and control request handling.
//
// When config.control_socket is set, Start binds an internal/control.Server,
// Tick polls ServeReady, and Shutdown closes the socket. HandleRequest is the
// shared op dispatcher used by the socket and HTTP/SSE (via APIBackend).
//
// Tick runs first-mount hooks (hooks.Runner) for mounted archives that are
// eligible (ShouldRunHooksRecord). After scan/reconcile/cleanup/work/hooks
// activity, NotifyChange wakes SSE clients early; the SSE refresh ticker is
// the fallback when idle.
//
// When convert_7z_nonsolid + flatten scope are set, New wires a best-effort
// 7z list solid/nested FlattenNeededFunc on the Engine if none was injected.
//
// Production metrics collectors get ConvertSidecarMeta{Config} so convert
// savings read the sidecar next to archive_path (or outer nonsolid cache dest)
// when store convert fields are sparse.
//
// Parity sources: tarmount-wsl service.py + control.py.
package service
