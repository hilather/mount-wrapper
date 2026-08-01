package mounter_test

import "os"

func writeFileMode(path, content string, mode os.FileMode) error {
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
