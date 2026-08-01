package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/hilather/mount-wrapper/internal/doctor"
)

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var configFlag, socketFlag string
	addConfigSocketFlags(fs, &configFlag, &socketFlag)
	jsonOut := fs.Bool("json", false, "JSON report")
	fixSystemd := fs.Bool("fix-systemd", false, "write systemd drop-in for source paths")
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
	_ = socketFlag // doctor is offline; socket unused

	cfg, resolvedPath, err := loadConfigOptional(configFlag)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return ExitError
	}

	report := doctor.Run(doctor.Options{
		Config:     cfg,
		FixSystemd: *fixSystemd,
	})
	// Surface the resolved path when no config was loaded so operators see where we looked.
	if report != nil && report.ConfigPath == "" && resolvedPath != "" {
		// Keep ConfigPath empty for JSON parity with "not found"; text notes the lookup.
	}

	if *jsonOut {
		text, err := doctor.FormatJSON(report)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return ExitError
		}
		fmt.Fprint(stdout, text)
	} else {
		text := doctor.FormatText(report)
		if cfg == nil {
			// Append lookup path when config was missing (FormatText omits empty config_path).
			text = strings.TrimRight(text, "\n") + fmt.Sprintf("\nconfig lookup: %s (not found or not loaded)\n", resolvedPath)
		}
		fmt.Fprint(stdout, text)
	}
	if report != nil && report.OK {
		return ExitOK
	}
	return ExitError
}
