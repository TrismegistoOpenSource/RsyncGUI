// SPDX-License-Identifier: MIT

//go:build !windows

package jobs

import "os"

// replaceFile renames tmp over dst. On Unix a rename over an open file just
// works; Windows needs a retry (replace_windows.go).
func replaceFile(tmp, dst string) error { return os.Rename(tmp, dst) }
