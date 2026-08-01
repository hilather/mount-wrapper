// Package control is the Unix-socket JSON-lines control plane.
//
// Protocol: newline-delimited JSON request/response objects.
// Requests must include "op"; optional "v" defaults to 1.
// Responses are {ok:true, data?...} or {ok:false, error, code}.
//
// Auth: SO_PEERCRED (Linux) / LOCAL_PEERCRED (Darwin) via internal/platform.
// Allow root (uid 0) or membership in the service group (default mount-wrapper).
// Escape hatches: Server.AllowAllAuth or env MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH=1.
//
// Parity: tarmount-wsl control.py (group/env names are mount-wrapper branded).
package control
