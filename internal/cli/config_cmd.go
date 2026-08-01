package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
)

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: mount-wrapper config <show|set> [flags]")
		return ExitUsage
	}
	switch args[0] {
	case "show":
		return runConfigShow(args[1:], stdout, stderr)
	case "set":
		return runConfigSet(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, configUsage())
		return ExitOK
	default:
		fmt.Fprintf(stderr, "unknown config command %q\n", args[0])
		fmt.Fprint(stderr, configUsage())
		return ExitUsage
	}
}

func configUsage() string {
	return `config — show or update configuration

Usage:
  mount-wrapper config show [--local] [--config PATH] [--socket PATH]
  mount-wrapper config set --json JSON|--file PATH [--patch] [--dry-run] [--config PATH] [--socket PATH]

show:
  --local     Read config file only (do not contact the service)
  Without --local, queries the running serve via config_get.

set:
  --file PATH   JSON file with full config or {"patch":{…}} / {"config":{…}}
  --json JSON   Inline JSON object (same shapes)
  --patch       Treat root object as a shallow patch over current config
  --dry-run     Validate only; do not write or reload
  Apply writes the file offline; when serve is up, also sends config_set on the socket.
`
}

func runConfigShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	local := fs.Bool("local", false, "read config file only (no control socket)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return ExitUsage
	}

	if *local {
		cfg, err := loadConfigRequired(configFlag)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		snap := config.Snapshot(cfg)
		if err := printJSON(stdout, snap); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		return ExitOK
	}

	// Socket-backed config_get (falls back to local snapshot if service is down
	// would surprise operators — match upstream: require serve).
	data, code := requestOK(configFlag, socketFlag, "config_get", nil, stderr)
	if code != ExitOK {
		// Helpful hint for offline use.
		if code == ExitServiceUnavailable {
			fmt.Fprintln(stderr, "hint: use --local to read the config file without a running serve")
		}
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runConfigSet(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	jsonFile := fs.String("file", "", "JSON file with full config or patch wrapper")
	fs.StringVar(jsonFile, "f", "", "alias for --file")
	jsonInline := fs.String("json", "", "inline JSON object")
	dryRun := fs.Bool("dry-run", false, "validate only; do not write or reload")
	asPatch := fs.Bool("patch", false, "treat JSON root as a shallow patch")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return ExitUsage
	}

	rawText, err := readJSONInput(*jsonFile, *jsonInline)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		if strings.Contains(err.Error(), "provide --file or --json") {
			return ExitUsage
		}
		return ExitError
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(rawText), &payload); err != nil {
		fmt.Fprintf(stderr, "error: invalid JSON: %v\n", err)
		return ExitError
	}

	// Normalize to {config|patch, apply}.
	var body map[string]any
	if _, hasCfg := payload["config"]; hasCfg {
		body = map[string]any{"config": payload["config"]}
		if p, ok := payload["patch"]; ok {
			body["patch"] = p
		}
	} else if _, hasPatch := payload["patch"]; hasPatch {
		body = map[string]any{"patch": payload["patch"]}
	} else if *asPatch {
		body = map[string]any{"patch": payload}
	} else {
		body = map[string]any{"config": payload}
	}
	body["apply"] = !*dryRun

	cfg, err := loadConfigRequired(configFlag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}

	// Prefer control socket when serve is up (apply or dry-run can both run server-side).
	// Offline path: ApplyUpdate locally (always works for dry-run; apply writes the file).
	if client, cerr := newClient(configFlag, socketFlag); cerr == nil {
		data, rerr := client.RequestOK("config_set", body)
		if rerr == nil {
			if err := printJSON(stdout, data); err != nil {
				fmt.Fprintf(stderr, "error: %v\n", err)
				return ExitError
			}
			return ExitOK
		}
		// Socket down or op not implemented yet → fall through to offline unless
		// the error is not UNAVAILABLE / BAD_REQUEST(unknown op).
		if ce, ok := rerr.(*ControlError); ok {
			if ce.Code == "UNAVAILABLE" || ce.Code == "BAD_REQUEST" {
				// offline fallback (socket down or op not implemented yet)
			} else {
				return handleControlError(stderr, rerr)
			}
		} else {
			return handleControlError(stderr, rerr)
		}
	}

	// Offline ApplyUpdate.
	var patch, full map[string]any
	if p, ok := body["patch"].(map[string]any); ok {
		patch = p
	}
	if c, ok := body["config"].(map[string]any); ok {
		full = c
	}
	apply := !*dryRun
	result, err := config.ApplyUpdate(cfg, patch, full, apply, cfg.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	result["offline"] = true
	if err := printJSON(stdout, result); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func readJSONInput(filePath, inline string) (string, error) {
	switch {
	case filePath != "":
		b, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("cannot read %s: %w", filePath, err)
		}
		return string(b), nil
	case inline != "":
		return inline, nil
	default:
		return "", fmt.Errorf("provide --file or --json")
	}
}
