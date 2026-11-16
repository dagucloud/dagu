// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSafeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "AlreadySafeString",
			input:    "simple_file-name123",
			expected: "simple_file-name123",
		},
		{
			name:     "StringWithSpaces",
			input:    "file name with spaces",
			expected: "file_name_with_spaces",
		},
		{
			name:     "StringWithSpecialCharacters",
			input:    "file!@#$%^&*()name.txt",
			expected: "file__________name_txt",
		},
		{
			name:     "StringWithWindowsReservedFilename",
			input:    "CON.txt",
			expected: "CON_txt",
		},
		{
			name:     "StringWithPathLikeCharacters",
			input:    "path/to\\file:name",
			expected: "path_to_file_name",
		},
		{
			name:     "EmptyString",
			input:    "",
			expected: "",
		},
		{
			name:     "VeryLongString",
			input:    strings.Repeat("a", 200),
			expected: strings.Repeat("a", MaxSafeNameLength),
		},
		{
			name:     "StringWithMixedCharacters",
			input:    "File Name 123!@#.txt",
			expected: "File_Name_123____txt",
		},
		{
			name:     "StringWithLeadingAndTrailingSpaces",
			input:    " filename ",
			expected: "_filename_",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SafeName(tt.input)
			if result != tt.expected {
				t.Errorf("SafeName(%q) = %q, expected %q", tt.input, result, tt.expected)
			}

			// Verify that the result only contains allowed characters
			if !isAllowedCharsOnly(result) {
				t.Errorf("SafeName(%q) = %q, contains disallowed characters", tt.input, result)
			}
		})
	}
}

// Helper function to verify that a string only contains allowed characters
func isAllowedCharsOnly(s string) bool {
	for _, r := range s {
		// nolint:staticcheck
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// SafeName output names directories and files, with or without an
// extension. Windows versions differ in which device names they map to a
// device, so on Windows every device name changes, on every version, to a
// name that no other natural name shares, such as con_. Other systems keep
// the name, so the runs and logs stored under it before still resolve.
func TestSafeNameDeviceNames(t *testing.T) {
	for _, name := range []string{"con", "PRN", "Aux", "nul", "COM1", "lpt9"} {
		t.Run(name, func(t *testing.T) {
			safe := SafeName(name)
			if runtime.GOOS == "windows" {
				require.NotEqual(t, name, safe)
				require.NotEqual(t, SafeName(name+"_"), safe)
			} else {
				require.Equal(t, name, safe)
			}
			require.NoError(t, storeUnder(t.TempDir(), safe))
		})
	}
}

// SafeKey keeps a name of letters, digits, "_", and "-" as written, and
// gives every name a key that no other name shares and the host stores.
func TestSafeKey(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", MaxSafeNameLength)
	names := []string{
		"fetch_data", "fetch data", "fetch.data", "fetch%20data", "fetch%2Edata",
		"a/b", "nul", "con", long, long + "b", long + "c", "日本",
	}
	keys := map[string]string{}
	for _, name := range names {
		key := SafeKey(name)
		require.NoError(t, storeUnder(t.TempDir(), key), name)
		require.LessOrEqual(t, len(key), MaxSafeNameLength, name)
		for other, otherKey := range keys {
			require.NotEqual(t, otherKey, key, "%q and %q", name, other)
		}
		keys[name] = key
	}
	assert.Equal(t, "fetch_data", keys["fetch_data"])
	assert.Equal(t, long, keys[long])
}

// Data that released versions stored under a SafeName belongs to a name only
// when no other name of the same set had that SafeName.
func TestLegacySafeName(t *testing.T) {
	t.Parallel()

	legacy, ok := LegacySafeName("fetch data", []string{"fetch data", "parse"})
	assert.True(t, ok)
	assert.Equal(t, "fetch_data", legacy)

	for _, siblings := range [][]string{{"fetch data", "fetch_data"}, {"fetch data", "fetch.data"}, {"fetch data", "Fetch.data"}} {
		_, ok = LegacySafeName("fetch data", siblings)
		assert.False(t, ok, siblings)
	}

	legacy, ok = LegacySafeName("fetch_data", []string{"fetch_data", "parse"})
	assert.True(t, ok)
	assert.Equal(t, "fetch_data", legacy)
	_, ok = LegacySafeName("fetch_data", []string{"fetch_data", "fetch data"})
	assert.False(t, ok)
}

// ConvertSafeNameDir gives a released version's entry to the one name that
// had its SafeName, and sets aside an entry that several names, or none, had.
func TestConvertSafeNameDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, file := range []string{"fetch_data.json", "a_b.json", "gone.json", "a%2Eb.json"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(file), 0o600))
	}
	names := []string{"fetch data", "a.b", "a_b", "fetch data"}
	require.NoError(t, ConvertSafeNameDir(dir, ".json", names))

	read := func(file string) string {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return ""
		}
		return string(data)
	}
	assert.Equal(t, "fetch_data.json", read(SafeKey("fetch data")+".json"))
	assert.Empty(t, read("fetch_data.json"))
	assert.Empty(t, read("a_b.json"))
	assert.Empty(t, read("gone.json"))
	assert.Equal(t, "a%2Eb.json", read("a%2Eb.json"))

	// A converted directory is left as it is.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a_b.json"), []byte("new"), 0o600))
	require.NoError(t, ConvertSafeNameDir(dir, ".json", names))
	assert.Equal(t, "new", read("a_b.json"))
	assert.True(t, IsSafeKeyDir(dir))
}

// A key that SafeKey cut maps back to its name once the name is kept.
func TestSafeKeyName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	long := strings.Repeat("日", 12)
	for _, name := range []string{"fetch_data", "fetch data", "con", long} {
		require.NoError(t, KeepSafeKeyName(dir, name))
		assert.Equal(t, name, SafeKeyName(dir, SafeKey(name)), name)
	}
	assert.Equal(t, SafeKey(long+"x"), SafeKeyName(dir, SafeKey(long+"x")))
}

// EscapeName returns a name this host stores as written, the same on every
// version of the host, and UnescapeName maps it back. On Windows a device
// name is escaped in any case and with any extension.
func TestEscapeName(t *testing.T) {
	tests := []struct{ name, windows, other string }{
		{name: "plain", windows: "plain", other: "plain"},
		{name: "50%", windows: "50%25", other: "50%25"},
		{name: "fetch/parse", windows: "fetch%2Fparse", other: "fetch%2Fparse"},
		{name: `fetch\parse`, windows: "fetch%5Cparse", other: `fetch\parse`},
		{name: "db:query", windows: "db%3Aquery", other: "db:query"},
		{name: "why?", windows: "why%3F", other: "why?"},
		{name: "tab\there", windows: "tab%09here", other: "tab\there"},
		{name: "nul\x00byte", windows: "nul%00byte", other: "nul%00byte"},
		{name: "nul", windows: "%6Eul", other: "nul"},
		{name: "nul.json", windows: "%6Eul.json", other: "nul.json"},
		{name: "con", windows: "%63on", other: "con"},
		{name: "COM1.txt", windows: "%43OM1.txt", other: "COM1.txt"},
		{name: "lpt\u00b2", windows: "%6Cpt\u00b2", other: "lpt\u00b2"},
		{name: "CONIN$", windows: "%43ONIN$", other: "CONIN$"},
		{name: "aux .log", windows: "%61ux .log", other: "aux .log"},
		{name: "COM0", windows: "COM0", other: "COM0"},
		{name: "console", windows: "console", other: "console"},
		{name: "nightly.", windows: "nightly%2E", other: "nightly."},
		{name: "trailing ", windows: "trailing%20", other: "trailing "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.other
			if runtime.GOOS == "windows" {
				want = tt.windows
			}
			escaped := EscapeName(tt.name)
			require.Equal(t, want, escaped)
			require.NoError(t, storeUnder(t.TempDir(), escaped))
			require.Equal(t, tt.name, UnescapeName(escaped))
		})
	}
}

// Different names never share a file or directory, even on a file system
// that ignores case, such as NTFS or APFS.
func TestEscapeNameDistinct(t *testing.T) {
	pairs := [][2]string{
		{"a/b", "a%2Fb"}, {"a/b", "a%2fb"}, {"db:query", "db%3aquery"}, {"nul", "%6eul"}, {"50%", "50%25"},
	}
	for _, pair := range pairs {
		t.Run(pair[0]+" "+pair[1], func(t *testing.T) {
			parent := t.TempDir()
			for _, name := range pair {
				require.NoError(t, os.Mkdir(filepath.Join(parent, EscapeName(name)), 0o750))
			}
			entries, err := os.ReadDir(parent)
			require.NoError(t, err)
			require.Len(t, entries, 2)
		})
	}
}

// storeUnder creates a directory called name in parent, writes a file named
// after it there, and reads the file back. It fails when the host stores the
// directory under another name, as Windows does by dropping a trailing dot.
func storeUnder(parent, name string) error {
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o750); err != nil {
		return err
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != name {
		return fmt.Errorf("%s is not stored under its own name", dir)
	}
	// A device name can "create" a device rather than a directory, and
	// reading a device such as CON may block.
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	file := filepath.Join(dir, name+".log")
	if err := os.WriteFile(file, []byte(name), 0o600); err != nil {
		return err
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if string(data) != name {
		return fmt.Errorf("read %q from %s", data, file)
	}
	return nil
}

// TestEdgeCases tests some additional edge cases
func TestEdgeCases(t *testing.T) {
	// Test that strings with only disallowed characters work correctly
	result := SafeName("!@#$%^&*()")
	if result != "__________" {
		t.Errorf("SafeName('!@#$%%^&*()') = %q, expected '__________'", result)
	}

	// Test truncation at exactly the maximum length
	exactLengthInput := strings.Repeat("a", MaxSafeNameLength)
	result = SafeName(exactLengthInput)
	if len(result) != MaxSafeNameLength {
		t.Errorf("SafeName with exact max length returned incorrect length: got %d, want %d",
			len(result), MaxSafeNameLength)
	}

	// Test truncation with one character over the limit
	overLengthInput := strings.Repeat("a", MaxSafeNameLength+1)
	result = SafeName(overLengthInput)
	if len(result) != MaxSafeNameLength {
		t.Errorf("SafeName with over max length returned incorrect length: got %d, want %d",
			len(result), MaxSafeNameLength)
	}
}

func TestNormalizeFilename(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		replacement string
		expected    string
	}{
		{"EmptyString", "", "-", ""},
		{"NormalName", "normal-name", "-", "normal-name"},
		{"Spaces", "hello world", "-", "hello-world"},
		{"ReservedChars", "<>:\"|?*", "-", "-------"},
		{"BackslashForwardSlash", "a/b\\c", "-", "a-b-c"},
		{"ControlChars", "a\x00b\x1Fc", "-", "a-b-c"},
		{"WindowsReservedCON", "CON", "-", "-"},
		{"WindowsReservedPRN", "PRN", "_", "_"},
		{"WindowsReservedAUX", "AUX", "-", "-"},
		{"WindowsReservedNUL", "NUL", "-", "-"},
		{"WindowsReservedCOM1", "COM1", "-", "-"},
		{"WindowsReservedLPT1", "LPT1", "-", "-"},
		{"WindowsReservedCaseInsensitive", "con", "-", "-"},
		{"WindowsReservedWithExtension", "CON.txt", "-", "-.txt"},
		{"WindowsReservedCOM1WithExt", "COM1.txt", "-", "-.txt"},
		{"COM0NotReserved", "COM0", "-", "COM0"},
		{"LPT0NotReserved", "LPT0", "-", "LPT0"},
		{"MixedReservedAndNormal", "mix<ed>name", "-", "mix-ed-name"},
		{"MultipleSpaces", "a  b  c", "-", "a--b--c"},
		{"UnderscoreReplacement", "hello world", "_", "hello_world"},
		{"ReservedWithMultiDotExt", "con.foo.txt", "-", "-.foo.txt"},
		{"ReservedWithEmptySegment", "CON..txt", "-", "-..txt"},
		{"ReservedLowerMultiDot", "aux.tar.gz", "-", "-.tar.gz"},
		{"NotReservedPrefix", "console.txt", "-", "console.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeFilename(tt.input, tt.replacement)
			require.Equal(t, tt.expected, result, "NormalizeFilename(%q, %q)", tt.input, tt.replacement)
		})
	}
}
