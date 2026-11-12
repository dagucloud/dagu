// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// carbonPath is the Carbon framework, which re-exports the Text Input
// Source functions of HIToolbox.
const carbonPath = "/System/Library/Frameworks/Carbon.framework/Carbon"

// inputSourceSettle is how long typing waits after the keyboard input
// source was switched, since applications learn of the switch by
// notification and keys posted before they do go through the previous one.
const inputSourceSettle = 100 * time.Millisecond

// Text Input Source functions, bound when the libraries load.
var (
	tisCopyCurrentKeyboardInputSource             func() uintptr
	tisCopyCurrentASCIICapableKeyboardInputSource func() uintptr
	tisGetInputSourceProperty                     func(source, key uintptr) uintptr
	tisSelectInputSource                          func(source uintptr) int32
)

// Keys of input source properties, resolved when the libraries load.
var (
	tisPropertyInputSourceID             uintptr
	tisPropertyInputSourceIsASCIICapable uintptr
)

// loadInputSourceFuncs binds what switching the keyboard input source
// needs.
func loadInputSourceFuncs() error {
	carbon, err := purego.Dlopen(carbonPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("load Carbon: %w", err)
	}
	purego.RegisterLibFunc(&tisCopyCurrentKeyboardInputSource, carbon, "TISCopyCurrentKeyboardInputSource")
	purego.RegisterLibFunc(&tisCopyCurrentASCIICapableKeyboardInputSource, carbon, "TISCopyCurrentASCIICapableKeyboardInputSource")
	purego.RegisterLibFunc(&tisGetInputSourceProperty, carbon, "TISGetInputSourceProperty")
	purego.RegisterLibFunc(&tisSelectInputSource, carbon, "TISSelectInputSource")
	for _, key := range []struct {
		name string
		ref  *uintptr
	}{
		{"kTISPropertyInputSourceID", &tisPropertyInputSourceID},
		{"kTISPropertyInputSourceIsASCIICapable", &tisPropertyInputSourceIsASCIICapable},
	} {
		symbol, err := purego.Dlsym(carbon, key.name)
		if err != nil {
			return fmt.Errorf("load %s: %w", key.name, err)
		}
		// The symbol is the address of the variable that holds the string.
		*key.ref = *(*uintptr)(unsafe.Add(nil, symbol)) //nolint:gosec // reads a C global whose address dlsym returned
	}
	return nil
}

// ASCIITyping selects the current ASCII-capable keyboard input source while
// the keyboard is on an input method, such as Japanese, that would turn
// typed keys into other text, and returns what selects the previous input
// source again. A keyboard already on an ASCII-capable input source stays
// as it is.
func (b *darwinBackend) ASCIITyping() (restore func() error, err error) {
	current := tisCopyCurrentKeyboardInputSource()
	if current == 0 {
		return nil, errors.New("read the keyboard input source")
	}
	if asciiCapable, ok := cfBool(tisGetInputSourceProperty(current, tisPropertyInputSourceIsASCIICapable)); ok && asciiCapable {
		cfRelease(current)
		return func() error { return nil }, nil
	}
	ascii := tisCopyCurrentASCIICapableKeyboardInputSource()
	if ascii == 0 {
		cfRelease(current)
		return nil, fmt.Errorf("the keyboard input source %s is not ASCII-capable and no ASCII-capable one is enabled; add one, such as ABC, in System Settings > Keyboard > Input Sources", inputSourceID(current))
	}
	defer cfRelease(ascii)
	if status := tisSelectInputSource(ascii); status != 0 {
		cfRelease(current)
		return nil, fmt.Errorf("select the keyboard input source %s for typing: OSStatus %d", inputSourceID(ascii), status)
	}
	time.Sleep(inputSourceSettle)
	return func() error {
		defer cfRelease(current)
		if status := tisSelectInputSource(current); status != 0 {
			return fmt.Errorf("select the keyboard input source %s again: OSStatus %d", inputSourceID(current), status)
		}
		return nil
	}, nil
}

// inputSourceID names an input source, such as com.apple.keylayout.ABC.
func inputSourceID(source uintptr) string {
	return goString(tisGetInputSourceProperty(source, tisPropertyInputSourceID))
}
