package securefile

import (
	"os"
	"path/filepath"
)

// WriteAtomic keeps private content out of the temporary file until its
// platform access controls are installed, then renames it into place.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := restrictOwnerAccess(dir, true); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".private-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := restrictOwnerAccess(tmp.Name(), false); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
