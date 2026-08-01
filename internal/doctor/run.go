package doctor

// Run executes diagnostic checks and returns a Report.
//
// When opts.Config is nil, only host/binary probes run (no path/source/config
// summary). When opts.FixSystemd is true, a systemd drop-in is written using
// BuildSystemdDropin (requires Config).
//
// All external I/O goes through Options injectables so unit tests can fake
// every probe.
func Run(opts Options) *Report {
	o := &opts

	checks := []CheckResult{
		checkGoVersion(o),
		checkHostPlatform(o),
		checkPeercred(o),
		checkFuseDevice(o),
		checkFusermount(o),
		checkUserAllowOther(o),
		checkRatarmount(o),
		checkArchiveconverter(o),
		checkSevenZipBin(o),
		checkMountBackend(o),
		checkSystemd(o),
		checkServiceUser(o),
	}

	var notes []string
	var fixes []string

	if o.Config != nil {
		checks = append(checks, checkServicePaths(o)...)
		checks = append(checks, checkSourceDirs(o)...)
		checks = append(checks, checkIndexLayout(o))
		checks = append(checks, checkFreeSpace(o)...)
		checks = append(checks, checkWebBindSecurity(o))
		checks = append(checks, checkConvertDirs(o)...)
		if c := checkControlSocketPathLength(o); c.Name != "" {
			checks = append(checks, c)
		}
		checks = append(checks, checkConfig(o))
	}

	if o.FixSystemd {
		if o.Config == nil {
			notes = append(notes, "--fix-systemd requires a readable config file")
			checks = append(checks, warnCheck("fix_systemd", false,
				"no config loaded; cannot write systemd drop-in",
				map[string]any{},
			))
		} else {
			path := o.dropinPath()
			ok, msg := ApplyFixSystemd(o.Config, path, o)
			sev := SeverityInfo
			if !ok {
				sev = SeverityError
			}
			if ok {
				msg = msg + "; run: systemctl daemon-reload && systemctl restart mount-wrapper"
				fixes = append(fixes, msg)
			} else {
				notes = append(notes,
					"Could not write systemd drop-in (need root?). "+
						"Preview with: mount-wrapper doctor",
				)
			}
			checks = append(checks, CheckResult{
				Name:     "fix_systemd",
				OK:       ok,
				Severity: sev,
				Message:  msg,
				Details:  map[string]any{"path": path},
			})
		}
	}

	configPath := ""
	if o.Config != nil {
		configPath = o.Config.ConfigPath
	}

	return &Report{
		OK:           !HardFail(checks),
		Checks:       checks,
		ConfigPath:   configPath,
		Notes:        notes,
		FixesApplied: fixes,
	}
}
