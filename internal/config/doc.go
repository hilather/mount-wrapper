// Package config loads, validates, and serializes mount-wrapper YAML configuration
// (schema version 1).
//
// Parity source: tarmount-wsl config.py / config_io.py. Paths and binary defaults
// use the mount-wrapper naming (see defaults.go). Duration fields accept human
// strings (24h, 30m) and are stored as seconds on Config.
package config
