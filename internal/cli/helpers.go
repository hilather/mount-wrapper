package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

// Process exit codes (parity with tarmount-wsl CLI).
const (
	ExitOK                 = 0
	ExitError              = 1
	ExitUsage              = 2
	ExitServiceUnavailable = 4
	ExitPermission         = 5
)

// printJSON writes indented JSON to w.
func printJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(data)
}

// handleControlError prints a control error and returns the process exit code.
func handleControlError(stderr io.Writer, err error) int {
	if err == nil {
		return ExitOK
	}
	fmt.Fprintf(stderr, "error: %v\n", err)
	if ce, ok := err.(*ControlError); ok {
		switch ce.Code {
		case "UNAVAILABLE":
			return ExitServiceUnavailable
		case "PERMISSION_DENIED":
			return ExitPermission
		}
	}
	return ExitError
}

// loadConfigRequired loads config from --config or the host default path.
func loadConfigRequired(configFlag string) (*config.Config, error) {
	path := config.ResolveConfigPath(configFlag)
	return config.Load(path)
}

// loadConfigOptional returns nil when the file does not exist; other errors
// propagate (invalid YAML, permission, …).
func loadConfigOptional(configFlag string) (*config.Config, string, error) {
	path := config.ResolveConfigPath(configFlag)
	cfg, err := config.Load(path)
	if err != nil {
		if os.IsNotExist(err) || isConfigNotFound(err) {
			return nil, path, nil
		}
		return nil, path, err
	}
	return cfg, path, nil
}

func isConfigNotFound(err error) bool {
	if err == nil {
		return false
	}
	// config.Load wraps missing files as ConfigError("config file not found: …").
	return strings.Contains(err.Error(), "config file not found")
}

// resolveSocket returns the control socket path from --socket or config.
// When socketFlag is set, config load is skipped (socket-only mode).
func resolveSocket(configFlag, socketFlag string) (string, error) {
	if strings.TrimSpace(socketFlag) != "" {
		return strings.TrimSpace(socketFlag), nil
	}
	cfg, err := loadConfigRequired(configFlag)
	if err != nil {
		return "", err
	}
	if cfg.ControlSocket == "" {
		return "", fmt.Errorf("control_socket is empty in config")
	}
	return cfg.ControlSocket, nil
}

// newClient builds a ControlClient for socket-backed commands.
func newClient(configFlag, socketFlag string) (*ControlClient, error) {
	path, err := resolveSocket(configFlag, socketFlag)
	if err != nil {
		return nil, err
	}
	return newControlClient(path), nil
}

// flagSet is the subset of *flag.FlagSet used by subcommands.
type flagSet interface {
	StringVar(p *string, name string, value string, usage string)
}

// addConfigSocketFlags registers --config / -c / --socket on a FlagSet.
func addConfigSocketFlags(fs flagSet, configFlag, socketFlag *string) {
	fs.StringVar(configFlag, "config", "", "path to config YAML (default: platform path)")
	fs.StringVar(configFlag, "c", "", "alias for --config")
	fs.StringVar(socketFlag, "socket", "", "control socket path (overrides config control_socket)")
}

// requestOK is a convenience wrapper: dial, RequestOK, handle errors → (data, exit).
func requestOK(configFlag, socketFlag string, op string, fields map[string]any, stderr io.Writer) (any, int) {
	client, err := newClient(configFlag, socketFlag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return nil, ExitError
	}
	data, err := client.RequestOK(op, fields)
	if err != nil {
		return nil, handleControlError(stderr, err)
	}
	return data, ExitOK
}
