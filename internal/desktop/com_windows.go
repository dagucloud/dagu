// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"fmt"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// COM is called through its vtables with no cgo. An interface pointer
// points at an object whose first word is the vtable, an array of
// function pointers that take the object as their first argument.

var (
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	oleaut32 = windows.NewLazySystemDLL("oleaut32.dll")

	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
	procSysFreeString    = oleaut32.NewProc("SysFreeString")
	procSysStringLen     = oleaut32.NewProc("SysStringLen")
	procVariantClear     = oleaut32.NewProc("VariantClear")
)

const clsctxInprocServer = 1

// hresult is a COM result code. The sign bit marks a failure.
type hresult uint32

var hresultNames = map[hresult]string{
	0x80004002: "E_NOINTERFACE",
	0x80004005: "E_FAIL",
	0x80070005: "E_ACCESSDENIED",
	0x80070057: "E_INVALIDARG",
	0x800401F0: "CO_E_NOTINITIALIZED",
	0x80040154: "REGDB_E_CLASSNOTREG",
	0x80040201: "UIA_E_ELEMENTNOTAVAILABLE",
	0x80040204: "UIA_E_NOTSUPPORTED",
	0x80131505: "UIA_E_TIMEOUT",
	0x80131509: "UIA_E_INVALIDOPERATION",
}

func (hr hresult) failed() bool { return int32(hr) < 0 } //nolint:gosec // The sign bit is the failure flag.

func (hr hresult) Error() string {
	if name, ok := hresultNames[hr]; ok {
		return fmt.Sprintf("%s (0x%08X)", name, uint32(hr))
	}
	return fmt.Sprintf("COM error 0x%08X", uint32(hr))
}

// comObject is what a COM interface pointer points at. The vtable is
// declared larger than any interface used here; only the indexes an
// interface defines are ever read.
type comObject struct {
	vtbl *[128]uintptr
}

// call invokes the method at index with the object as its first argument
// and returns the HRESULT.
func (o *comObject) call(index int, args ...uintptr) hresult {
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(unsafe.Pointer(o))) //nolint:gosec // COM takes the object as its first argument.
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[index], all...)
	return hresult(r) //nolint:gosec // An HRESULT is the low 32 bits of the result.
}

// addRef takes another reference, for a caller that will release one.
// AddRef and Release return a count, not a result code.
func (o *comObject) addRef() {
	_ = o.call(1)
}

// release drops the caller's reference. It is safe on nil.
func (o *comObject) release() {
	if o != nil {
		_ = o.call(2)
	}
}

// out returns a pointer argument, for a method's out-parameter or a
// struct it reads.
func out[T any](v *T) uintptr {
	return uintptr(unsafe.Pointer(v)) //nolint:gosec // COM takes out-parameters as pointers.
}

// coCreateInstance creates the in-process COM object clsid and returns its
// iid interface.
func coCreateInstance(clsid, iid *windows.GUID) (*comObject, error) {
	var obj *comObject
	r, _, _ := procCoCreateInstance.Call(out(clsid), 0, clsctxInprocServer, out(iid), out(&obj))
	if hr := hresult(r); hr.failed() { //nolint:gosec // An HRESULT is the low 32 bits of the result.
		return nil, hr
	}
	return obj, nil
}

// A BSTR is a COM string: UTF-16 with its length stored before it, which
// SysStringLen reads, so embedded NULs survive.

// bstrString returns the text of a BSTR.
func bstrString(b *uint16) string {
	if b == nil {
		return ""
	}
	n, _, _ := procSysStringLen.Call(out(b))
	if n == 0 {
		return ""
	}
	return string(utf16.Decode(unsafe.Slice(b, n))) //nolint:gosec // n is the length SysStringLen reports.
}

// bstrFree releases a BSTR. It is safe on nil.
func bstrFree(b *uint16) {
	if b != nil {
		_, _, _ = procSysFreeString.Call(out(b))
	}
}

// Variant types used here.
const (
	vtI4   = 3
	vtBSTR = 8
)

// variant is a COM VARIANT on a 64-bit system: the type, three reserved
// words, and a sixteen-byte value, since a record holds two pointers.
type variant struct {
	vt  uint16
	_   [3]uint16
	val [2]uintptr
}

// text returns the string a VT_BSTR variant holds, or "" for any other
// type. The variant still owns the string until clear.
func (v *variant) text() string {
	if v.vt != vtBSTR {
		return ""
	}
	// The first word of the value is the BSTR pointer COM wrote; it is
	// read through the field's address so the conversion stays in bounds.
	return bstrString(*(**uint16)(unsafe.Pointer(&v.val[0]))) //nolint:gosec // See above.
}

// integer returns the number a VT_I4 variant holds, or 0 for any other
// type.
func (v *variant) integer() int32 {
	if v.vt != vtI4 {
		return 0
	}
	return *(*int32)(unsafe.Pointer(&v.val[0])) //nolint:gosec // The value's first word holds the number.
}

// clear releases whatever the variant holds.
func (v *variant) clear() {
	_, _, _ = procVariantClear.Call(out(v))
}

// mustGUID parses a GUID literal.
func mustGUID(s string) windows.GUID {
	guid, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return guid
}
