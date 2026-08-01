package config

// Clone returns a deep copy of cfg (all slices and *int knobs duplicated).
// Nil input yields nil. Safe for concurrent readers after the source is mutated
// under a lock (e.g. service doReload).
func Clone(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.SourceDirs = copyStringSlice(cfg.SourceDirs)
	out.RecursiveMountExtensions = copyStringSlice(cfg.RecursiveMountExtensions)
	out.Convert7zFlattenExclude = copyStringSlice(cfg.Convert7zFlattenExclude)
	out.ExtraRatarmountArgs = copyStringSlice(cfg.ExtraRatarmountArgs)
	out.ArchiveconverterExcludeInner = copyStringSlice(cfg.ArchiveconverterExcludeInner)
	out.ArchiveconverterExcludeOuter = copyStringSlice(cfg.ArchiveconverterExcludeOuter)
	out.ArchiveconverterRename = copyStringSlice(cfg.ArchiveconverterRename)
	out.ArchiveconverterExtraArgs = copyStringSlice(cfg.ArchiveconverterExtraArgs)
	out.UnknownKeys = copyStringSlice(cfg.UnknownKeys)
	out.ArchiveconverterThreads = copyIntPtr(cfg.ArchiveconverterThreads)
	out.ArchiveconverterNestedConcurrency = copyIntPtr(cfg.ArchiveconverterNestedConcurrency)
	return &out
}

// copyStringSlice returns a new slice with the same elements, or nil if in is nil.
// Unlike stringSliceCopy (public map helpers), this preserves nil vs empty.
func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
