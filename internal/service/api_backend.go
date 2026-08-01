package service

import (
	"github.com/hilather/mount-wrapper/internal/api"
	"github.com/hilather/mount-wrapper/internal/config"
)

// APIBackend adapts Service to api.Backend (avoids import cycles at the method set).
type APIBackend struct {
	S *Service
}

// HandleRequest implements api.Backend.
func (b *APIBackend) HandleRequest(req map[string]any) map[string]any {
	if b == nil || b.S == nil {
		return ErrResponse("service not available", "UNAVAILABLE")
	}
	return b.S.HandleRequest(req)
}

// Version implements api.Backend.
func (b *APIBackend) Version() string {
	if b == nil || b.S == nil || b.S.Version == "" {
		return "dev"
	}
	return b.S.Version
}

// Config implements api.Backend.
// Returns a deep snapshot under Service.opMu so concurrent doReload cannot race
// HTTP health/doctor/wsl-info readers of config fields.
func (b *APIBackend) Config() *config.Config {
	if b == nil || b.S == nil {
		return nil
	}
	return b.S.ConfigSnapshot()
}

// Notify implements api.ChangeNotifier so SSE streams wake early after
// service tick / control-plane mutations (ticker remains the fallback).
func (b *APIBackend) Notify() <-chan struct{} {
	if b == nil || b.S == nil {
		return nil
	}
	return b.S.Changes()
}

// Ensure APIBackend implements api.Backend and api.ChangeNotifier.
var (
	_ api.Backend        = (*APIBackend)(nil)
	_ api.ChangeNotifier = (*APIBackend)(nil)
)
