package scanner

import (
	"os"
	"path/filepath"
	"sort"
)

// ListSourceFiles lists regular (non-symlink) files under sourceDir.
// Non-recursive by default; when recursive, walks without following symlinks.
func ListSourceFiles(sourceDir string, recursive bool) ([]string, error) {
	st, err := os.Stat(sourceDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !st.IsDir() {
		return nil, nil
	}

	if !recursive {
		entries, err := os.ReadDir(sourceDir)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			// Skip symlinks (match Python: is_file and not is_symlink).
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			// ReadDir follows? ModeSymlink on Info from ReadDir for symlink is set.
			// Also skip non-regular files.
			if !info.Mode().IsRegular() {
				// On some FS, symlink may appear as ModeSymlink without IsRegular.
				// Double-check with Lstat.
				full := filepath.Join(sourceDir, e.Name())
				lst, err := os.Lstat(full)
				if err != nil || lst.Mode()&os.ModeSymlink != 0 || !lst.Mode().IsRegular() {
					continue
				}
			}
			out = append(out, filepath.Join(sourceDir, e.Name()))
		}
		sort.Strings(out)
		return out, nil
	}

	var out []string
	err = filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		// Lstat to be sure we don't follow.
		lst, err := os.Lstat(path)
		if err != nil || lst.Mode()&os.ModeSymlink != 0 || !lst.Mode().IsRegular() {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
