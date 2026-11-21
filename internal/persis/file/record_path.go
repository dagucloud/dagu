// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// A record ID maps to a path that Windows, macOS, and Linux all store as
// written. Each "/"-separated segment of the ID names a directory, and the
// last one names the record's file, with ".json" added. A segment keeps its
// characters, except that these are written as %XX, so a path maps back to
// exactly one ID:
//
//   - "%" itself
//   - the characters Windows forbids in a file name: control characters and
//     < > : " \ | ? *
//   - the first character of a Windows device name, such as nul, CON, com1,
//     or aux.txt, which Windows reserves in any case and with any extension
//   - the trailing dot or space of a directory, which Windows drops
//
// Records used to be written under their raw segments, so a record whose ID
// needs escaping may still have its file there. Reads find it, and the next
// write moves it to the escaped path. Windows never held a usable file under
// a name it reserves, refuses, or rewrites, and opening one there may open a
// device, so it reads a raw path only when its only escapes are of "%".

// RecordIDFromPath returns the ID of the record a collection keeps at rel, a
// path relative to the collection's directory.
func RecordIDFromPath(rel string) string {
	return relPathToID(rel)
}

// idToRelPath converts "a/b/c" → "a/b/c.json" using the OS path separator.
func idToRelPath(id string) string {
	segments := strings.Split(id, "/")
	for i, segment := range segments {
		segments[i] = escapeSegment(segment, i < len(segments)-1)
	}
	return filepath.Join(segments...) + ".json"
}

// relPathToID is the inverse of idToRelPath. It also returns the ID of a
// record kept under its raw segments.
func relPathToID(rel string) string {
	segments := strings.Split(filepath.ToSlash(strings.TrimSuffix(rel, ".json")), "/")
	for i, segment := range segments {
		segments[i] = segmentFromName(segment, i < len(segments)-1)
	}
	return strings.Join(segments, "/")
}

// rawRelPath is where a record was written before IDs were escaped.
func rawRelPath(id string) string {
	return filepath.Join(strings.Split(id, "/")...) + ".json"
}

// rawNameUsable reports whether this OS can hold the raw segments of id.
// dir says whether the last segment names a directory.
func rawNameUsable(id string, dir bool) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	segments := strings.Split(id, "/")
	for i, segment := range segments {
		if isWindowsDeviceName(segment) {
			return false
		}
		for j := 0; j < len(segment); j++ {
			if forbiddenOnWindows(segment[j]) {
				return false
			}
		}
		if (dir || i < len(segments)-1) && (strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ")) {
			return false
		}
	}
	return true
}

// escapeSegment returns the name of the directory (dir) or file that holds
// segment. "." and ".." are kept, so a path that leaves the collection is
// still refused.
func escapeSegment(segment string, dir bool) string {
	if segment == "." || segment == ".." {
		return segment
	}
	device := isWindowsDeviceName(segment)
	var b strings.Builder
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		last := i == len(segment)-1
		if c == '%' || forbiddenOnWindows(c) || (i == 0 && device) || (dir && last && (c == '.' || c == ' ')) {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// segmentFromName returns the ID segment that a directory (dir) or file
// name holds. A name that is not the escaped form of its own decoding was
// written before IDs were escaped, so it is its own segment.
func segmentFromName(name string, dir bool) string {
	decoded := unescapeSegment(name)
	if escapeSegment(decoded, dir) != name {
		return name
	}
	return decoded
}

// unescapeSegment turns each %XX of name back into its byte.
func unescapeSegment(name string) string {
	if !strings.Contains(name, "%") {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if name[i] == '%' && i+2 < len(name) {
			if hi, ok := hexValue(name[i+1]); ok {
				if lo, ok := hexValue(name[i+2]); ok {
					b.WriteByte(hi<<4 | lo)
					i += 2
					continue
				}
			}
		}
		b.WriteByte(name[i])
	}
	return b.String()
}

func hexValue(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

func forbiddenOnWindows(c byte) bool {
	return c < 0x20 || strings.IndexByte(`<>:"\|?*`, c) >= 0
}

// isWindowsDeviceName reports whether Windows reserves name for a device. It
// does so for the part before the first dot, with trailing spaces removed.
func isWindowsDeviceName(name string) bool {
	base, _, _ := strings.Cut(name, ".")
	base = strings.TrimRight(base, " ")
	if len(base) < 3 {
		return false
	}
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	switch strings.ToUpper(base[:3]) {
	case "COM", "LPT":
		// Windows documents digits 0 through 9 and the superscripts ¹, ², ³.
		switch number := base[3:]; {
		case len(number) == 1 && '0' <= number[0] && number[0] <= '9':
			return true
		case number == "¹" || number == "²" || number == "³":
			return true
		}
	}
	return false
}
