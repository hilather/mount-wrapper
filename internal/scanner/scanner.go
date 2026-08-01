package scanner

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/hilather/mount-wrapper/internal/archives"
	"github.com/hilather/mount-wrapper/internal/config"
	"github.com/hilather/mount-wrapper/internal/match"
	"github.com/hilather/mount-wrapper/internal/paths"
	"github.com/hilather/mount-wrapper/internal/state"
)

// Clock returns the current Unix time in seconds (injectable for tests).
type Clock func() float64

// Scanner polls source directories and reconciles archive rows in the state store.
type Scanner struct {
	Config     *config.Config
	Store      *state.Store
	Extensions []string
	regex      *regexp.Regexp
	clock      Clock
	Gate       *StableFileGate
	// mappedSources is (original, mapped) pairs.
	mappedSources [][2]string
	// PathOpts optional ToWSL mapping options (nil = default pure-Go mapping).
	PathOpts *paths.ToWSLOpts
}

// New creates a Scanner from config and store.
// nameRegex empty uses config.NameRegex; extensions nil means no allow-list filter.
func New(cfg *config.Config, store *state.Store, nameRegex string, extensions []string, clock Clock) (*Scanner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("scanner: config is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("scanner: store is nil")
	}
	pattern := nameRegex
	if pattern == "" {
		pattern = cfg.NameRegex
	}
	re, err := match.CompileNameRegex(pattern)
	if err != nil {
		return nil, err
	}
	gate, err := NewStableFileGate(cfg.StableFileMode, cfg.MinFileAgeSeconds)
	if err != nil {
		return nil, err
	}
	if clock == nil {
		clock = func() float64 { return float64(time.Now().UnixNano()) / 1e9 }
	}
	s := &Scanner{
		Config:     cfg,
		Store:      store,
		Extensions: extensions,
		regex:      re,
		clock:      clock,
		Gate:       gate,
	}
	s.mapSources()
	return s, nil
}

func (s *Scanner) mapSources() {
	mapped := make([][2]string, 0, len(s.Config.SourceDirs))
	for _, raw := range s.Config.SourceDirs {
		p, err := paths.ToWSLPath(raw, s.PathOpts)
		if err != nil {
			log.Printf("source_dirs map failed: %v", err)
			continue
		}
		mapped = append(mapped, [2]string{raw, p})
	}
	s.mappedSources = mapped
}

// ReloadSources re-maps source_dirs and hot-reloads discovery knobs after config change.
func (s *Scanner) ReloadSources() error {
	re, err := match.CompileNameRegex(s.Config.NameRegex)
	if err != nil {
		return err
	}
	gate, err := NewStableFileGate(s.Config.StableFileMode, s.Config.MinFileAgeSeconds)
	if err != nil {
		return err
	}
	s.regex = re
	s.Gate = gate
	s.mapSources()
	return nil
}

// MappedSources returns a copy of (original, mapped) source pairs.
func (s *Scanner) MappedSources() [][2]string {
	out := make([][2]string, len(s.mappedSources))
	copy(out, s.mappedSources)
	return out
}

// IsStable reports whether archivePath currently passes the stable-file gate.
// Uses in-memory gate state from the last Scan observations. Paths never scanned
// return false unless they live under archives_dir (relocated copies).
func (s *Scanner) IsStable(archivePath string) bool {
	if archives.IsArchivesPath(s.Config, archivePath) {
		st, err := os.Stat(archivePath)
		return err == nil && st.Mode().IsRegular()
	}
	now := s.clock()
	return s.Gate.Peek(archivePath, nil, nil, &now)
}

// StableArchiveIDs returns archive IDs in discovered that pass the stable-file gate.
func (s *Scanner) StableArchiveIDs() ([]string, error) {
	recs, err := s.Store.ListArchives(state.StatusDiscovered)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, rec := range recs {
		if s.IsStable(rec.ArchivePath) {
			ids = append(ids, rec.ArchiveID)
		}
	}
	return ids, nil
}

// Scan runs one full discovery pass and updates the state store.
// assumeStable treats all currently present matching files as stable immediately.
func (s *Scanner) Scan(assumeStable bool) (*ScanResult, error) {
	t0 := time.Now()
	now := s.clock()
	nowISO := state.UTCNowISO()
	result := &ScanResult{AssumeStable: assumeStable}
	if assumeStable {
		log.Printf("assume_stable bypass: stable-file gate disabled for this scan (admin/acceptance only)")
	}

	var observations []FileObservation
	for _, pair := range s.mappedSources {
		mapped := pair[1]
		st, err := os.Stat(mapped)
		if err != nil || !st.IsDir() {
			log.Printf("source missing or not a directory: %s → %s", pair[0], mapped)
			result.SkippedSources = append(result.SkippedSources, mapped)
			continue
		}
		files, err := ListSourceFiles(mapped, s.Config.Recursive)
		if err != nil {
			msg := fmt.Sprintf("cannot list %s: %v", mapped, err)
			log.Printf("%s", msg)
			result.Errors = append(result.Errors, msg)
			continue
		}
		for _, path := range files {
			obs, err := s.observeFile(path, mapped, now, assumeStable, result)
			if err != nil {
				// errors already recorded on result
				continue
			}
			if obs != nil {
				observations = append(observations, *obs)
			}
		}
	}

	result.Observations = observations
	seen := make([]string, 0, len(observations))
	seenSet := make(map[string]struct{}, len(observations))
	for _, o := range observations {
		seen = append(seen, o.Path)
		seenSet[o.Path] = struct{}{}
	}
	s.Gate.ForgetMissing(seen)

	for i := range observations {
		if err := s.reconcileObservation(&observations[i], result, nowISO); err != nil {
			msg := fmt.Sprintf("reconcile %s: %v", observations[i].Path, err)
			log.Printf("%s", msg)
			result.Errors = append(result.Errors, msg)
		}
	}

	// Active rows not seen → absent (skip archives relocated off source_dirs)
	active, err := s.Store.ListArchives(state.ActiveStatusesList())
	if err != nil {
		return result, err
	}
	for _, rec := range active {
		if _, ok := seenSet[rec.ArchivePath]; ok {
			continue
		}
		if archives.IsArchivesPath(s.Config, rec.ArchivePath) {
			st, err := os.Stat(rec.ArchivePath)
			if err == nil && st.Mode().IsRegular() {
				continue
			}
		}
		absent, err := s.Store.MarkAbsent(rec.ArchiveID, nowISO, nil)
		if err != nil {
			msg := fmt.Sprintf("mark_absent %s: %v", rec.ArchiveID, err)
			log.Printf("%s", msg)
			result.Errors = append(result.Errors, msg)
			continue
		}
		result.AbsentIDs = append(result.AbsentIDs, absent.ArchiveID)
		s.Gate.Reset(rec.ArchivePath)
	}

	// Stable archive ids among discovered
	for _, obs := range observations {
		if !obs.Stable {
			continue
		}
		rec, err := s.Store.GetArchiveByPath(obs.Path)
		if err != nil {
			continue
		}
		if rec != nil && rec.Status == state.StatusDiscovered {
			result.StableArchiveIDs = append(result.StableArchiveIDs, rec.ArchiveID)
		}
	}

	result.DurationMs = float64(time.Since(t0).Microseconds()) / 1000.0
	log.Printf(
		"scan done duration_ms=%.1f seen=%d inserted=%d changed=%d absent=%d stable=%d",
		result.DurationMs,
		len(observations),
		len(result.InsertedIDs),
		len(result.ContentChangedIDs),
		len(result.AbsentIDs),
		len(result.StableArchiveIDs),
	)
	return result, nil
}

func (s *Scanner) observeFile(
	path, sourceDir string,
	now float64,
	assumeStable bool,
	result *ScanResult,
) (*FileObservation, error) {
	basename := filepath.Base(path)
	// path.Base rejects empty; skip dot entries.
	if basename == "" || basename == "." || basename == ".." {
		return nil, nil
	}
	if !match.ExtensionAllowed(basename, s.Extensions) {
		return nil, nil
	}
	if s.regex == nil || !s.regex.MatchString(basename) {
		return nil, nil
	}

	st, err := os.Stat(path)
	if err != nil {
		msg := fmt.Sprintf("stat failed %s: %v", path, err)
		log.Printf("%s", msg)
		result.Errors = append(result.Errors, msg)
		return nil, nil
	}
	if !st.Mode().IsRegular() {
		return nil, nil
	}

	size := st.Size()
	mtimeNs := st.ModTime().UnixNano()
	var inode *uint64
	if ino, ok := fileInode(st); ok {
		inode = &ino
	}

	if s.Config.MaxArchiveBytes > 0 && size > int64(s.Config.MaxArchiveBytes) {
		msg := fmt.Sprintf("skip oversized %s size=%d max=%d", path, size, s.Config.MaxArchiveBytes)
		log.Printf("%s", msg)
		result.SkippedFiles = append(result.SkippedFiles, path)
		return nil, nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	// Prefer resolved absolute path.
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		absPath = resolved
	}

	fingerprint, err := ComputeFingerprint(path, size, mtimeNs, s.Config.ContentFingerprint)
	if err != nil {
		msg := fmt.Sprintf("fingerprint failed %s: %v", path, err)
		log.Printf("%s", msg)
		result.Errors = append(result.Errors, msg)
		return nil, nil
	}

	stable := s.Gate.Check(absPath, size, mtimeNs, now, assumeStable)

	return &FileObservation{
		Path:        absPath,
		SourceDir:   sourceDir,
		Basename:    basename,
		SizeBytes:   size,
		MtimeNs:     mtimeNs,
		Inode:       inode,
		Fingerprint: fingerprint,
		Stable:      stable,
	}, nil
}

func (s *Scanner) reconcileObservation(obs *FileObservation, result *ScanResult, nowISO string) error {
	existing, err := s.Store.GetArchiveByPath(obs.Path)
	if err != nil {
		return err
	}
	if existing == nil {
		rec, err := s.Store.InsertDiscovered(state.InsertDiscoveredParams{
			SourceDir:       obs.SourceDir,
			ArchivePath:     obs.Path,
			ArchiveBasename: obs.Basename,
			SizeBytes:       obs.SizeBytes,
			MtimeNs:         obs.MtimeNs,
			Fingerprint:     obs.Fingerprint,
			Now:             nowISO,
		})
		if err != nil {
			return err
		}
		result.InsertedIDs = append(result.InsertedIDs, rec.ArchiveID)
		return nil
	}

	if existing.Status == state.StatusAbsent {
		rec, err := s.Store.Reappear(existing.ArchiveID, obs.SizeBytes, obs.MtimeNs, obs.Fingerprint, nowISO)
		if err != nil {
			return err
		}
		result.ReappearedIDs = append(result.ReappearedIDs, rec.ArchiveID)
		return nil
	}

	if existing.Fingerprint != obs.Fingerprint {
		resetHooks := s.Config.OnContentChange == config.OnContentRemountResetHooks
		rec, err := s.Store.RecordContentChange(
			existing.ArchiveID,
			obs.SizeBytes, obs.MtimeNs, obs.Fingerprint,
			resetHooks, nowISO,
		)
		if err != nil {
			return err
		}
		result.ContentChangedIDs = append(result.ContentChangedIDs, rec.ArchiveID)
		return nil
	}

	// Same fingerprint — refresh last_seen
	size := obs.SizeBytes
	mtime := obs.MtimeNs
	fp := obs.Fingerprint
	rec, err := s.Store.TouchSeen(existing.ArchiveID, &size, &mtime, &fp, nowISO)
	if err != nil {
		return err
	}
	result.TouchedIDs = append(result.TouchedIDs, rec.ArchiveID)
	return nil
}

// fileInode extracts inode when available (Linux/Unix).
func fileInode(st os.FileInfo) (uint64, bool) {
	return inodeFromFileInfo(st)
}
