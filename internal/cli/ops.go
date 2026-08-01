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

func runHooks(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: mount-wrapper hooks <list|status> …")
		return ExitUsage
	}
	switch args[0] {
	case "list":
		return runHooksList(args[1:], stdout, stderr)
	case "status":
		return runHooksStatus(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, `hooks — inspect first-mount hooks

Usage:
  mount-wrapper hooks list [--config PATH] [--socket PATH]
  mount-wrapper hooks status ARCHIVE_ID [--config PATH] [--socket PATH]
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
