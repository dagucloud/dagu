// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// MaxSafeNameLength is the maximum length of a safe filename
	MaxSafeNameLength = 100
)

var (
	// Only allow alphanumeric characters, underscores, and hyphens
	allowedCharRegex = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

	// https://github.com/sindresorhus/filename-reserved-regex/blob/master/index.js
	filenameReservedRegex             = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)
	filenameReservedWindowsNamesRegex = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)
)

// SafeName converts a string to a safe filename containing only alphanumeric characters,
// underscores, and hyphens. On Windows, a reserved device name such as CON or
// LPT1 gets a hyphen and a short hash of itself appended, so it does not share
// a result with a name such as CON_.
func SafeName(str string) string {
	safe := replacedName(str)
	if reservesDeviceName(safe) {
		sum := sha256.Sum256([]byte(safe))
		safe += "-" + hex.EncodeToString(sum[:4])
	}

	return safe
}

// replacedName replaces each character not in [a-zA-Z0-9_-] with an
// underscore and truncates the result to MaxSafeNameLength.
func replacedName(str string) string {
	safe := allowedCharRegex.ReplaceAllString(str, "_")
	if runes := []rune(safe); len(runes) > MaxSafeNameLength {
		safe = string(runes[:MaxSafeNameLength])
	}
	return safe
}

// SafeKey returns a file name for str that no other string shares. ASCII
// letters, digits, "_", and "-" are kept, so a name of only those, at most
// MaxSafeNameLength bytes long, is its own key, except a Windows device name
// on Windows. Every other byte, and on Windows the first byte of a device
// name, is written as %XX. A longer key is cut and ends in "%-" and a hash of
// str. Names that differ only in letter case still share an entry on a file
// system that ignores case.
func SafeKey(str string) string {
	var b strings.Builder
	for i := 0; i < len(str); i++ {
		if c := str[i]; isSafeKeyByte(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	key := b.String()
	if reservesDeviceName(key) {
		key = fmt.Sprintf("%%%02X", key[0]) + key[1:]
	}
	if len(key) > MaxSafeNameLength {
		sum := sha256.Sum256([]byte(str))
		suffix := "%-" + hex.EncodeToString(sum[:16])
		key = key[:MaxSafeNameLength-len(suffix)] + suffix
	}
	return key
}

func isSafeKeyByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-'
}

// LegacySafeName returns the name under which released versions stored data
// for name with SafeName, which other names could share, and reports whether
// that data belongs to name alone: no other name in siblings, the names that
// could share it, had that name too, ignoring case.
func LegacySafeName(name string, siblings []string) (string, bool) {
	legacy := replacedName(name)
	for _, sibling := range siblings {
		if sibling != name && strings.EqualFold(replacedName(sibling), legacy) {
			return legacy, false
		}
	}
	return legacy, true
}

// unescapeSafeKey returns the name that SafeKey made key from, or key itself
// when key does not hold a name in full, such as a key that SafeKey cut or a
// SafeName that released versions stored.
func unescapeSafeKey(key string) string {
	name, err := url.PathUnescape(key)
	if err != nil || SafeKey(name) != key {
		return key
	}
	return name
}

// safeKeyNameExt follows the key of the file that holds the name of a key
// SafeKey cut.
const safeKeyNameExt = ".name"

// KeepSafeKeyName records name in dir, which it creates, when SafeKey cut
// its key, so that SafeKeyName can return it.
func KeepSafeKeyName(dir, name string) error {
	key := SafeKey(name)
	if unescapeSafeKey(key) == name {
		return nil
	}
	path := filepath.Join(dir, key+safeKeyNameExt)
	if _, err := os.Lstat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return WriteFileAtomic(path, []byte(name), 0o600)
}

// SafeKeyName returns the name whose SafeKey is key, the name of an entry of
// dir. The name is decoded from key, or read from what KeepSafeKeyName
// recorded for a key SafeKey cut. Without either, as for a SafeName that
// released versions stored, it returns key.
func SafeKeyName(dir, key string) string {
	if name := unescapeSafeKey(key); SafeKey(name) == key {
		return name
	}
	data, err := os.ReadFile(filepath.Join(dir, key+safeKeyNameExt)) //nolint:gosec // dir and key name an entry the caller listed
	if err == nil && SafeKey(string(data)) == key {
		return string(data)
	}
	return key
}

// ClaimLegacySafeName moves the entry that released versions stored in
// fromDir for name, under its SafeName followed by suffix, to path, so name
// keeps its data under its own key. It moves nothing when another name in
// siblings may own that entry (see LegacySafeName), or when path exists.
// path must not be in fromDir, where an entry may be another name's.
func ClaimLegacySafeName(fromDir, name, suffix string, siblings []string, path string) error {
	legacy, ok := LegacySafeName(name, siblings)
	if !ok {
		return nil
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	rel, ok := LegacyEntry(fromDir, legacy+suffix)
	if !ok {
		return nil
	}
	if err := Rename(filepath.Join(fromDir, rel), path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// SafeNameEscapesDevice reports whether SafeName changes str only because
// this host reserves it for a device, which only Windows does. Earlier
// versions kept such a name as written.
func SafeNameEscapesDevice(str string) bool {
	return !allowedCharRegex.MatchString(str) && reservesDeviceName(str)
}

// EscapeName returns the name under which this host stores a file or
// directory called name. Each "%", each byte the host cannot store in a
// name, including a path separator, and on Windows the first byte of a
// reserved device name are written as %XX. Any other byte is kept, and "."
// and ".." are returned unchanged. Two different names never share an entry,
// even on a file system that ignores case.
func EscapeName(name string) string {
	if name == "." || name == ".." {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		if c := name[i]; c == '%' || unstorableByte(c, i == len(name)-1) {
			fmt.Fprintf(&b, "%%%02X", c)
		} else {
			b.WriteByte(c)
		}
	}
	escaped := b.String()
	if reservesDeviceName(escaped) {
		escaped = fmt.Sprintf("%%%02X", escaped[0]) + escaped[1:]
	}
	return escaped
}

// UnescapeName returns the name that EscapeName stores as escaped. A name
// that EscapeName does not produce is returned as is.
func UnescapeName(escaped string) string {
	name, err := url.PathUnescape(escaped)
	if err != nil || EscapeName(name) != escaped {
		return escaped
	}
	return name
}

// ExistingName returns the name of the entry in dir that holds name. It is
// name, unless dir holds no entry called name but holds the entry that
// LegacyEntry finds for legacy, the name that earlier versions used.
func ExistingName(dir, name, legacy string) string {
	if name == legacy {
		return name
	}
	if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
		return name
	}
	if rel, ok := LegacyEntry(dir, legacy); ok {
		return rel
	}
	return name
}

// LegacyEntry reports whether dir holds a regular file or directory called
// legacy, a name stored as written before names were escaped, and returns
// its path relative to dir. Only a name that names that entry exactly is
// looked up, so the entry never belongs to another name. Each "/"-separated
// segment must be non-empty, not "." or "..", stored by this host as written
// (on Windows, without a trailing dot or space, a reserved character, or a
// device name), and not, ignoring case, the escaped form of another name.
func LegacyEntry(dir, legacy string) (string, bool) {
	for segment := range strings.SplitSeq(filepath.ToSlash(legacy), "/") {
		if !exactSegment(segment) {
			return "", false
		}
	}
	rel := filepath.Clean(legacy)
	info, err := os.Lstat(filepath.Join(dir, rel))
	if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) {
		return "", false
	}
	return rel, true
}

// exactSegment reports whether this host stores a path segment under exactly
// its own name, and no escaped name may share it.
func exactSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." || opensDevice(segment) {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if unstorableByte(segment[i], i == len(segment)-1) {
			return false
		}
	}
	other, err := url.PathUnescape(segment)
	return err != nil || other == segment || !strings.EqualFold(EscapeName(other), segment)
}

// NormalizeFilename replaces OS-reserved characters and Windows reserved
// device names in a filename with the given replacement string.
func NormalizeFilename(name, replacement string) string {
	s := filenameReservedRegex.ReplaceAllString(name, replacement)
	s = strings.ReplaceAll(s, " ", replacement)

	// Windows reserves device names regardless of any extension, so the
	// segment before the first dot is what matters.
	device, rest, hasDot := strings.Cut(s, ".")
	if filenameReservedWindowsNamesRegex.MatchString(device) {
		device = replacement
	}
	if hasDot {
		return device + "." + rest
	}
	return device
}
