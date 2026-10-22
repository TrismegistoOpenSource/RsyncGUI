// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
)

// writeFileAtomic replaces path with data so that a crash or a full disk
// leaves either the old file or the new one, never half of each: it writes a
// uniquely named temporary file next to it, flushes it to disk, and renames
// it over the target. Used for profiles.json and the settings, which are
// rare writes of precious data; frequent state files do not pay for the sync.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	_ = f.Chmod(perm) // best effort: CreateTemp makes it 0600, the old file was 0644
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}
