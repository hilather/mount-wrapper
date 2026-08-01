// Package state is the SQLite archive lifecycle store and forward-only migrations.
//
// Parity source: tarmount-wsl state.py and migrations/ (schema version 6).
//
// Single-writer rule: only the serve process should write the state database in
// production. This package is the single writer API; callers must not share a
// Store across goroutines without external serialization (v1 is a single-threaded
// service loop). Concurrent readers of a file DB are possible via SQLite WAL,
// but all mutations should go through one serve-owned Store.
package state
