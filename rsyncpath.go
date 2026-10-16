// SPDX-License-Identifier: MIT

package main

import (
	"runtime"
	"strings"
)

// rsyncPath turns a local path into the form the rsync binary understands.
//
// On Windows rsync is a Cygwin (cwRsync) or MSYS2 program, and it reads
// "C:\Users\..." as host "C" plus a remote path: it tries ssh and fails
// ("Could not resolve hostname c"); "C:/Users/..." gives "The source and
// destination cannot both be remote". Every local copy failed — on Windows
// RsyncGUI could not copy anything (found 08/10/2026: the tests that run the
// real rsync had always been skipped there for lack of rsync).
//
// The two families mount drives differently (/cygdrive/c for Cygwin, /c for
// MSYS2), but both have /proc/cygdrive pointing at their own prefix, so
// /proc/cygdrive/c/... works with either. Elsewhere the path is unchanged.
func rsyncPath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	return cygwinPath(p)
}

// cygwinPath: "C:\A\b" → "/proc/cygdrive/c/A/b", "\\srv\share\x" →
// "//srv/share/x" (the UNC form of Cygwin). Remote specs (host:path,
// rsync://) and paths already in POSIX form pass through untouched. A
// backslash cannot be part of a Windows file name, so turning every one into
// a slash is safe.
func cygwinPath(p string) string {
	if len(p) >= 2 && p[1] == ':' && isASCIILetter(p[0]) && (len(p) == 2 || p[2] == '\\' || p[2] == '/') {
		rest := strings.ReplaceAll(p[2:], `\`, "/")
		if rest == "" {
			rest = "/"
		}
		return "/proc/cygdrive/" + strings.ToLower(p[:1]) + rest
	}
	if strings.HasPrefix(p, `\\`) {
		return strings.ReplaceAll(p, `\`, "/")
	}
	return p
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isWindowsLocalPath: on Windows, a path with a drive letter or a UNC prefix
// (the ones cygwinPath converts). Paths in "/" form and remote specs keep
// the separator-agnostic handling of pathBase and pathJoin.
func isWindowsLocalPath(p string) bool {
	return runtime.GOOS == "windows" && cygwinPath(p) != p
}
