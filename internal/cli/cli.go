// Package cli implements the mount-wrapper command-line interface.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/service"
)

// BuildInfo is injected from main via ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// Run parses args and executes a command. Returns a process exit code.
func Run(args []string, info BuildInfo) int {
	return RunWithIO(args, info, os.Stdout, os.Stderr)
}

// RunWithIO is like Run but uses the provided writers (for tests).
func RunWithIO(args []string, info BuildInfo, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return ExitUsage
	}

	switch args[0] {
	case "version", "--version", "-V":
		fmt.Fprintf(stdout, "mount-wrapper %s (commit=%s date=%s)\n", info.Version, info.Commit, info.Date)
		return ExitOK
	case "help", "--help", "-h":
		printUsage(stdout)
		return ExitOK
	case "serve":
		return runServe(args[1:], info, stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "metrics":
		return runMetrics(args[1:], stdout, stderr)
	case "rescan":
		return runRescan(args[1:], stdout, stderr)
	case "retry":
		return runRetry(args[1:], stdout, stderr)
	case "mount":
		return runMount(args[1:], stdout, stderr)
	case "unmount":
		return runUnmount(args[1:], stdout, stderr)
	case "purge":
		return runPurge(args[1:], stdout, stderr)
	case "hooks":
		return runHooks(args[1:], stdout, stderr)
	case "reload":
		return runReload(args[1:], stdout, stderr)
	case "stop":
		return runStop(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return ExitUsage
	}
}

func runServe(args []string, info BuildInfo, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config YAML (default: platform path)")
	fs.StringVar(configPath, "c", "", "alias for --config")
	once := fs.Bool("once", false, "run a single tick then exit (debug / tests)")
	allowUnauth := fs.Bool("allow-unauth", false, "allow unauthenticated control ops (also env MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH)")
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

	path := config.ResolveConfigPath(*configPath)
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return ExitError
	}

	allow := *allowUnauth
	if v := strings.TrimSpace(os.Getenv("MOUNT_WRAPPER_CONTROL_ALLOW_UNAUTH")); v == "1" || strings.EqualFold(v, "true") {
		allow = true
	}

	svc, err := service.New(cfg, &service.Options{
		AllowAllAuth: allow,
		Version:      info.Version,
	})
	if err != nil {
		fmt.Fprintf(stderr, "service init: %v\n", err)
		return ExitError
	}

	fmt.Fprintf(stdout, "mount-wrapper serve starting version=%s once=%v config=%s\n",
		info.Version, *once, path)
	if err := svc.Run(*once); err != nil {
		fmt.Fprintf(stderr, "serve: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `mount-wrapper — archive auto-mounter orchestrator

Usage:
  mount-wrapper <command> [flags]

Commands:
  version              Print version
  help                 Show this help
  serve                Run the daemon (scan / mount / hooks loop)
  doctor               Offline environment diagnostics
  config show|set      Show or update configuration
  status               Service status (requires serve)
  metrics [ARCHIVE_ID] Archive size metrics (requires serve; --no-cache, --prefer-mount)
  rescan               Trigger an immediate source scan (requires serve)
  retry ARCHIVE_ID     Reset mount attempts (requires serve)
  mount PATH           Request mount of an archive path (requires serve)
  unmount TARGET|--all Unmount managed archive(s) (requires serve)
  purge ARCHIVE_ID     Purge state/index/overlay (requires --yes; serve)
  hooks list|status|rerun  Inspect or re-run first-mount hooks (serve)
  reload               Reload config from disk (SIGHUP equivalent; serve)
  stop                 Request graceful serve shutdown (SIGTERM equivalent; serve)

Common flags:
  --config, -c PATH    Config YAML (default: %s)
  --socket PATH        Control socket (overrides config control_socket)

serve flags:
  --once               Single tick then exit (tests / oneshot debug)
  --allow-unauth       Allow unauthenticated control (env also supported)

doctor flags:
  --json               JSON report
  --fix-systemd        Write systemd drop-in for source paths
  --dry-run            With --fix-systemd: print drop-in; do not write

config show flags:
  --local              Read config file only (no control socket)

config set flags:
  --json JSON | --file PATH   Patch or full config
  --patch              Treat root object as shallow patch
  --dry-run            Validate only; do not write

status flags:
  --json               Machine-readable JSON
  --sizes              Include size metrics (slower)

reload flags:
  --json               Machine-readable JSON (control ack, e.g. {"reload":"scheduled"})

stop flags:
  --json               Machine-readable JSON (control ack, e.g. {"stop":"scheduled"})

Socket-backed commands require a running "mount-wrapper serve".
Offline without serve: version, help, doctor, config show --local, config set --dry-run.

See https://github.com/hilather/mount-wrapper
`, config.DefaultConfigPathForHost())
}
