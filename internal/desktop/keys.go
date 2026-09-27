// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Key is a keyboard key: one of the named keys below, a function key "f1"
// to "f24", or a single printable ASCII character such as "a" or "/".
type Key string

// Named keys.
const (
	KeyShift       Key = "shift"
	KeyCtrl        Key = "ctrl"
	KeyAlt         Key = "alt"
	KeyMeta        Key = "meta" // Command on macOS, Windows key on Windows.
	KeyEnter       Key = "enter"
	KeyTab         Key = "tab"
	KeyEscape      Key = "escape"
	KeyBackspace   Key = "backspace"
	KeyDelete      Key = "delete"
	KeySpace       Key = "space"
	KeyInsert      Key = "insert"
	KeyHome        Key = "home"
	KeyEnd         Key = "end"
	KeyPageUp      Key = "pageup"
	KeyPageDown    Key = "pagedown"
	KeyUp          Key = "up"
	KeyDown        Key = "down"
	KeyLeft        Key = "left"
	KeyRight       Key = "right"
	KeyCapsLock    Key = "capslock"
	KeyPrintScreen Key = "printscreen"
)

const maxFunctionKey = 24

// keyAliases maps normalized spellings from models and tools, such as
// xdotool keysyms and browser key names, to keys.
var keyAliases = map[string]Key{
	"shift": KeyShift,
	"ctrl":  KeyCtrl, "control": KeyCtrl, "ctl": KeyCtrl,
	"alt": KeyAlt, "option": KeyAlt, "opt": KeyAlt,
	"meta": KeyMeta, "cmd": KeyMeta, "command": KeyMeta, "super": KeyMeta, "win": KeyMeta, "windows": KeyMeta,
	"enter": KeyEnter, "return": KeyEnter, "kpenter": KeyEnter,
	"tab":    KeyTab,
	"escape": KeyEscape, "esc": KeyEscape,
	"backspace": KeyBackspace,
	"delete":    KeyDelete, "del": KeyDelete,
	"space": KeySpace, "spacebar": KeySpace,
	"insert": KeyInsert, "ins": KeyInsert,
	"home":   KeyHome,
	"end":    KeyEnd,
	"pageup": KeyPageUp, "pgup": KeyPageUp, "prior": KeyPageUp,
	"pagedown": KeyPageDown, "pgdn": KeyPageDown, "next": KeyPageDown,
	"up": KeyUp, "arrowup": KeyUp, "uparrow": KeyUp,
	"down": KeyDown, "arrowdown": KeyDown, "downarrow": KeyDown,
	"left": KeyLeft, "arrowleft": KeyLeft, "leftarrow": KeyLeft,
	"right": KeyRight, "arrowright": KeyRight, "rightarrow": KeyRight,
	"capslock": KeyCapsLock, "caps": KeyCapsLock,
	"printscreen": KeyPrintScreen, "print": KeyPrintScreen, "prtsc": KeyPrintScreen,
	"minus": "-", "equal": "=", "plus": "+", "comma": ",", "period": ".", "slash": "/",
	"backslash": `\`, "semicolon": ";", "apostrophe": "'", "quote": "'", "grave": "`",
	"backquote": "`", "bracketleft": "[", "bracketright": "]",
}

var modifierKeys = map[Key]bool{KeyShift: true, KeyCtrl: true, KeyAlt: true, KeyMeta: true}

// sideSuffixes name the left or right copy of a modifier, as in
// "Control_L" or "ShiftRight".
var sideSuffixes = []string{"left", "right", "l", "r"}

// shiftedChars maps characters typed with Shift on a US layout to the key
// that produces them.
var shiftedChars = map[rune]rune{
	'~': '`', '!': '1', '@': '2', '#': '3', '$': '4', '%': '5', '^': '6', '&': '7', '*': '8',
	'(': '9', ')': '0', '_': '-', '+': '=', '{': '[', '}': ']', '|': '\\', ':': ';', '"': '\'',
	'<': ',', '>': '.', '?': '/',
}

// ParseKey accepts a key name in any common spelling, ignoring case.
func ParseKey(name string) (Key, error) {
	if utf8.RuneCountInString(name) == 1 {
		r, _ := utf8.DecodeRuneInString(name)
		if r < '!' || r > '~' {
			return "", fmt.Errorf("unsupported key %q: type the character instead", name)
		}
		return Key(strings.ToLower(name)), nil
	}
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(name))
	if key, ok := keyAliases[normalized]; ok {
		return key, nil
	}
	for _, suffix := range sideSuffixes {
		if key, ok := keyAliases[strings.TrimSuffix(normalized, suffix)]; ok && modifierKeys[key] {
			return key, nil
		}
	}
	if n, ok := strings.CutPrefix(normalized, "f"); ok {
		if number, err := strconv.Atoi(n); err == nil && number >= 1 && number <= maxFunctionKey {
			return Key(normalized), nil
		}
	}
	return "", fmt.Errorf("unknown key %q", name)
}

// ParseKeys parses each key name.
func ParseKeys(names []string) ([]Key, error) {
	keys := make([]Key, 0, len(names))
	for _, name := range names {
		key, err := ParseKey(name)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// Char returns the character of a single-character key.
func (k Key) Char() (rune, bool) {
	if utf8.RuneCountInString(string(k)) != 1 {
		return 0, false
	}
	r, _ := utf8.DecodeRuneInString(string(k))
	return r, true
}

// USKey returns the unshifted character that produces a character key on a
// US layout and whether Shift is needed.
func (k Key) USKey() (base rune, shift bool, ok bool) {
	r, ok := k.Char()
	if !ok {
		return 0, false, false
	}
	if base, shifted := shiftedChars[r]; shifted {
		return base, true, true
	}
	return r, false, true
}

// FunctionNumber returns n for the function key "fn".
func (k Key) FunctionNumber() (int, bool) {
	n, ok := strings.CutPrefix(string(k), "f")
	if !ok {
		return 0, false
	}
	number, err := strconv.Atoi(n)
	if err != nil || number < 1 || number > maxFunctionKey {
		return 0, false
	}
	return number, true
}
