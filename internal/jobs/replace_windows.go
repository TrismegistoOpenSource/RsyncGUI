// SPDX-License-Identifier: MIT

//go:build windows

package jobs

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// replaceFile renames tmp over dst. On Windows that fails with "access denied"
// (or a sharing violation) while another process has dst open: the window
// reads job states and logs while the supervisor rewrites them, and Go opens
// files without FILE_SHARE_DELETE. The reader holds the file only for a
// moment, so a short retry is enough; failing at once lost state updates —
// the final outcome included — whose errors the supervisor cannot do
// anything with (TestWriteStateIsAtomic: 4 failures out of 40 on windows,
// 08/10/2026).
func replaceFile(tmp, dst string) error {
	var err error
	for i := 0; i < 200; i++ {
		if err = os.Rename(tmp, dst); err == nil || !busy(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

func busy(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
