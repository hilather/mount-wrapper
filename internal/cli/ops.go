package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/hilather/mount-wrapper/internal/status"
)

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	sizes := fs.Bool("sizes", false, "include index/extracted size metrics (slower)")
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

	fields := map[string]any{}
	if *sizes {
		fields["include_sizes"] = true
	}
	data, code := requestOK(configFlag, socketFlag, "status", fields, stderr)
	if code != ExitOK {
		return code
	}
	if *jsonOut || *sizes {
		if err := printJSON(stdout, data); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		return ExitOK
	}
	fmt.Fprint(stdout, formatStatusOutput(data))
	return ExitOK
}

// formatStatusOutput prefers status.FormatHuman when the payload re-decodes
// into status.Payload; otherwise falls back to the map-based formatter.
func formatStatusOutput(data any) string {
	if data == nil {
		return formatStatusHuman(nil)
	}
	// Already a *status.Payload (in-process; rare for CLI client path).
	if p, ok := data.(*status.Payload); ok {
		return status.FormatHuman(p)
	}
	b, err := json.Marshal(data)
	if err != nil {
		return formatStatusHuman(data)
	}
	var p status.Payload
	if err := json.Unmarshal(b, &p); err != nil {
		return formatStatusHuman(data)
	}
	return status.FormatHuman(&p)
}

func runMetrics(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	noCache := fs.Bool("no-cache", false, "bypass metrics cache")
	preferMount := fs.Bool("prefer-mount", false, "prefer walking the FUSE mount over the index")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	var archiveID string
	if fs.NArg() > 1 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args()[1:], " "))
		return ExitUsage
	}
	if fs.NArg() == 1 {
		archiveID = fs.Arg(0)
	}

	fields := map[string]any{
		"no_cache":     *noCache,
		"prefer_mount": *preferMount,
	}
	if archiveID != "" {
		fields["archive_id"] = archiveID
	}
	data, code := requestOK(configFlag, socketFlag, "metrics", fields, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runRescan(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("rescan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	assumeStable := fs.Bool("assume-stable", false, "bypass stable-file gate (admin/acceptance only)")
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

	fields := map[string]any{"assume_stable": *assumeStable}
	data, code := requestOK(configFlag, socketFlag, "rescan", fields, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runRetry(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("retry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: mount-wrapper retry ARCHIVE_ID [--config PATH] [--socket PATH]")
		return ExitUsage
	}
	fields := map[string]any{"archive_id": fs.Arg(0)}
	data, code := requestOK(configFlag, socketFlag, "retry", fields, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runMount(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: mount-wrapper mount PATH [--config PATH] [--socket PATH]")
		return ExitUsage
	}
	fields := map[string]any{"path": fs.Arg(0)}
	data, code := requestOK(configFlag, socketFlag, "mount", fields, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runUnmount(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("unmount", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	all := fs.Bool("all", false, "unmount all managed archives")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	target := ""
	if fs.NArg() > 1 {
		fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args()[1:], " "))
		return ExitUsage
	}
	if fs.NArg() == 1 {
		target = fs.Arg(0)
	}
	if !*all && target == "" {
		fmt.Fprintln(stderr, "error: provide TARGET or --all")
		return ExitUsage
	}
	if *all && target != "" {
		fmt.Fprintln(stderr, "error: cannot combine TARGET with --all")
		return ExitUsage
	}

	fields := map[string]any{}
	if *all {
		fields["all"] = true
	} else {
		fields["target"] = target
	}
	data, code := requestOK(configFlag, socketFlag, "unmount", fields, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runPurge(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("purge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	yes := fs.Bool("yes", false, "confirm purge (required)")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: mount-wrapper purge ARCHIVE_ID --yes [--config PATH] [--socket PATH]")
		return ExitUsage
	}
	if !*yes {
		fmt.Fprintln(stderr, "error: purge requires --yes")
		return ExitUsage
	}
	fields := map[string]any{
		"archive_id": fs.Arg(0),
		"yes":        true,
	}
	data, code := requestOK(configFlag, socketFlag, "purge", fields, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runReload(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("reload", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
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
	data, code := requestOK(configFlag, socketFlag, "reload", nil, stderr)
	if code != ExitOK {
		return code
	}
	if *jsonOut {
		// Control ack is {"reload":"scheduled"}; dump as machine-readable JSON.
		if err := printJSON(stdout, data); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		return ExitOK
	}
	// Default: human line for operators (other ops dump useful JSON payloads).
	fmt.Fprintln(stdout, "reload scheduled")
	return ExitOK
}

func runStop(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
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
	data, code := requestOK(configFlag, socketFlag, "stop", nil, stderr)
	if code != ExitOK {
		return code
	}
	if *jsonOut {
		// Control ack is {"stop":"scheduled"}; dump as machine-readable JSON.
		if err := printJSON(stdout, data); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		return ExitOK
	}
	// Default: human line for operators (other ops dump useful JSON payloads).
	fmt.Fprintln(stdout, "stop scheduled")
	return ExitOK
}

func runHooks(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: mount-wrapper hooks <list|status|rerun> …")
		return ExitUsage
	}
	switch args[0] {
	case "list":
		return runHooksList(args[1:], stdout, stderr)
	case "status":
		return runHooksStatus(args[1:], stdout, stderr)
	case "rerun":
		return runHooksRerun(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, `hooks — inspect or re-run first-mount hooks

Usage:
  mount-wrapper hooks list [--config PATH] [--socket PATH]
  mount-wrapper hooks status ARCHIVE_ID [--config PATH] [--socket PATH]
  mount-wrapper hooks rerun [flags] ARCHIVE_ID

Flags:
  --force              re-run even when hooks_status is terminal success/failed
  --json               machine-readable JSON
  --config PATH / -c   config YAML
  --socket PATH        control socket override

hooks rerun uses control op hooks_run. Without --force, eligibility matches
serve (none|pending|retry|running; failed only if hook_rerun_on_failure).
--force re-runs even after terminal success/failed. Archive must be mounted
or hooks_running. Successful per-hook rows are still skipped on re-run
(resume semantics) unless you clear hook state separately.
`)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "unknown hooks command %q\n", args[0])
		return ExitUsage
	}
}

func runHooksList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hooks list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
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
	data, code := requestOK(configFlag, socketFlag, "hooks_list", nil, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runHooksStatus(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hooks status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: mount-wrapper hooks status ARCHIVE_ID [--config PATH] [--socket PATH]")
		return ExitUsage
	}
	fields := map[string]any{"archive_id": fs.Arg(0)}
	data, code := requestOK(configFlag, socketFlag, "hooks_status", fields, stderr)
	if code != ExitOK {
		return code
	}
	if err := printJSON(stdout, data); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}
	return ExitOK
}

func runHooksRerun(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hooks rerun", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	force := fs.Bool("force", false, "re-run even when hooks_status is terminal success/failed")
	jsonOut := fs.Bool("json", false, "machine-readable JSON output")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return ExitOK
		}
		return ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: mount-wrapper hooks rerun [--force] [--json] [--config PATH] [--socket PATH] ARCHIVE_ID")
		return ExitUsage
	}
	fields := map[string]any{
		"archive_id": fs.Arg(0),
		"force":      *force,
	}
	data, code := requestOK(configFlag, socketFlag, "hooks_run", fields, stderr)
	if code != ExitOK {
		return code
	}
	if *jsonOut {
		if err := printJSON(stdout, data); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		return ExitOK
	}
	fmt.Fprint(stdout, formatHooksRerunHuman(data))
	return ExitOK
}

// formatHooksRerunHuman prints a one-line operator summary of hooks_run data.
func formatHooksRerunHuman(data any) string {
	m, ok := data.(map[string]any)
	if !ok || m == nil {
		return "hooks rerun: ok\n"
	}
	id, _ := m["archive_id"].(string)
	status, _ := m["hooks_status"].(string)
	ran, _ := m["ran"].(bool)
	force, _ := m["force"].(bool)
	skip, _ := m["skipped_reason"].(string)
	forceNote := ""
	if force {
		forceNote = " force=true"
	}
	if !ran {
		if skip == "" {
			skip = "not eligible"
		}
		return fmt.Sprintf("hooks skipped archive_id=%s hooks_status=%s%s (%s)\n", id, status, forceNote, skip)
	}
	return fmt.Sprintf("hooks ran archive_id=%s hooks_status=%s%s\n", id, status, forceNote)
}
