// SPDX-License-Identifier: MIT

package main

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Accented names come in two Unicode spellings: precomposed (NFC, "à" is one
// code point) and decomposed (NFD, "a" + combining grave). They look identical
// and every Apple filesystem treats them as the same name, but rsync compares
// bytes. When the destination hands names back in the other spelling, rsync
// takes the destination file for an extra one and --delete removes it.
//
// On an SMB share mounted by macOS this is not a one-off: smbfs lists every
// name decomposed, whatever the server stores. So a source file named in NFC is
// copied, then "deleted" as extra — and since for the server it is the same
// file, the copy just written is what disappears (rsync then fails setting its
// timestamps: "utimensat: No such file or directory"). Every run, forever.
// Observed on the QNAP share, 25/09/2026: 8 notes of the vault missing from
// the NAS, while the APFS disk, which keeps names as written, had them all.
//
// rsync's --iconv would be the textbook answer, but openrsync (the rsync macOS
// ships) does not have it. What works with both rsync families is a
// receiver-side exclude ("-r"), which protects a path from deletion without
// affecting what is sent: for every source name with an accent, its twin in
// the other spelling is protected. A file really removed from the source has
// no twin in it and is deleted as before.

// protectRulesForTwins returns one filter rule per source entry whose name has
// a twin spelling, anchored to the root of the transfer as rsync sees it.
//
// Only the last component takes the other spelling: rsync walks the
// destination through the directories of the sender's file list, so the parent
// path is matched as the source spells it (verified with openrsync on a nested
// accented folder + accented file).
func protectRulesForTwins(opts SyncOptions, sources []string) []string {
	var rules []string
	add := func(parent, name string) {
		for _, twin := range twinSpellings(name) {
			rules = append(rules, "-r /"+escapeFilterPattern(path.Join(parent, twin)))
		}
	}

	for _, src := range sources {
		fi, err := os.Stat(src)
		if err != nil {
			continue
		}
		// Same trailing-slash logic as rsyncArgs: what lands in the
		// destination is either the source's contents or the source itself.
		trimmed := strings.TrimRight(src, "/")
		prefix := ""
		if !fi.IsDir() || (opts.RecreateStructure && trimmed != "") {
			prefix = filepath.Base(trimmed)
			add("", prefix)
		}
		if !fi.IsDir() {
			continue
		}

		_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				// Unreadable entry: rsync will report it on its own.
				if d != nil && d.IsDir() && p != src {
					return fs.SkipDir
				}
				return nil
			}
			if p == src {
				return nil
			}
			rel, relErr := filepath.Rel(src, p)
			if relErr != nil {
				return nil
			}
			parent := path.Join(prefix, filepath.ToSlash(filepath.Dir(rel)))
			add(parent, d.Name())
			return nil
		})
	}
	return rules
}

// twinSpellings returns the other Unicode spellings of name (NFC and NFD,
// minus the one it already has). Plain ASCII names have none. Names with a
// line break are skipped: the rules travel one per line in a file.
func twinSpellings(name string) []string {
	if strings.ContainsAny(name, "\n\r") {
		return nil
	}
	var twins []string
	for _, form := range []norm.Form{norm.NFC, norm.NFD} {
		t := form.String(name)
		if t != name && (len(twins) == 0 || twins[0] != t) {
			twins = append(twins, t)
		}
	}
	return twins
}

// escapeFilterPattern makes a literal path safe as an rsync pattern. rsync
// only treats backslash as an escape when the pattern holds a wildcard (*, ?
// or [), so a path without them goes through untouched and one with them gets
// all four escaped.
func escapeFilterPattern(p string) string {
	if !strings.ContainsAny(p, "*?[") {
		return p
	}
	var b strings.Builder
	for _, r := range p {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// writeTwinProtectRules writes the rules to a temporary filter file and returns
// its path ("" when there is nothing to protect) plus a cleanup func. A file
// rather than one -f per rule: a large tree with many accented names would
// otherwise hit the command-line length limit.
//
// Deletion is the only thing the rules guard against, so without Delete no
// walk happens at all.
func writeTwinProtectRules(opts SyncOptions, sources []string) (string, func(), error) {
	noop := func() {}
	if !opts.Delete {
		return "", noop, nil
	}
	rules := protectRulesForTwins(opts, sources)
	if len(rules) == 0 {
		return "", noop, nil
	}
	f, err := os.CreateTemp("", "rsyncgui-protect-*.rules")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.Remove(f.Name()) }
	if _, err := f.WriteString(strings.Join(rules, "\n") + "\n"); err != nil {
		f.Close()
		cleanup()
		return "", noop, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", noop, err
	}
	return f.Name(), cleanup, nil
}
