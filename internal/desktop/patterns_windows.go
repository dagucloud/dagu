// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// Control patterns are what an element does by itself: a button invokes,
// a checkbox toggles, a field takes a value. UI Automation hands a pattern
// out as an interface of its own, and an element that lacks one answers
// with no interface rather than an error. Indexes and ids come from
// UIAutomationClient.h in Windows SDK 10.0.26100, as in uia_windows.go.

// Pattern ids.
const (
	uiaInvokePatternID         = 10000
	uiaValuePatternID          = 10002
	uiaExpandCollapsePatternID = 10005
	uiaSelectionItemPatternID  = 10010
	uiaTogglePatternID         = 10015
	uiaScrollItemPatternID     = 10017
)

var (
	iidIUIAutomation2        = mustGUID("{34723AFF-0C9D-49D0-9896-7AB52DF8CD8A}")
	iidInvokePattern         = mustGUID("{FB377FBE-8EA6-46D5-9C73-6499642D3059}")
	iidValuePattern          = mustGUID("{A94CD8B1-0844-4CD6-9D2D-640537AB39E9}")
	iidExpandCollapsePattern = mustGUID("{619BE086-1F4E-4EE4-BAFA-210128738730}")
	iidSelectionItemPattern  = mustGUID("{A8EFA66A-0FDA-421A-9194-38021F3578EA}")
	iidTogglePattern         = mustGUID("{94CF8058-9B8D-4AB9-8BFD-4CD0A33C8C70}")
	iidScrollItemPattern     = mustGUID("{B488300F-D015-4F19-9C29-BB595E3645EF}")
)

// IUIAutomationElement methods beside those in uia_windows.go, and
// IUIAutomation2, which extends IUIAutomation (last index 57).
const (
	uiaGetCurrentPatternAs    = 14
	uiaGetCurrentIsEnabled    = 28
	uia2PutTransactionTimeout = 63
)

// The pattern interfaces' own methods; IUnknown holds 0 to 2.
const (
	uiaInvokeInvoke                  = 3
	uiaValueSetValue                 = 3
	uiaValueGetCurrentIsReadOnly     = 5
	uiaToggleToggle                  = 3
	uiaSelectionItemSelect           = 3
	uiaExpandCollapseExpand          = 3
	uiaExpandCollapseGetCurrentState = 5
	uiaScrollItemScrollIntoView      = 3
)

// ExpandCollapseState values: an element open in full, and one with
// nothing to expand.
const (
	expandCollapseExpanded = 1
	expandCollapseLeafNode = 3
)

// Results a pattern call answers with.
const (
	hrNoInterface         hresult = 0x80004002
	hrElementNotEnabled   hresult = 0x80040200
	hrElementNotAvailable hresult = 0x80040201
	hrNotSupported        hresult = 0x80040204
)

// transactionTimeout is how long the newer automation client waits for an
// application to answer one call, kept under elementsTimeout so a call an
// application holds comes back as an error before the caller gives up.
const transactionTimeout = elementsTimeout - time.Second

var (
	errReadOnly error = refusal("the field is read-only")
	errLeaf     error = refusal("the element has nothing to expand")
)

// patternAs returns the element's interface for a pattern, which the
// caller releases, or ErrNoPattern when the element does not offer it.
func (e *element) patternAs(id int32, iid *windows.GUID) (*comObject, error) {
	// The out-pointer lives on the heap: out converts its address before
	// the call runs, and a stack that grows meanwhile would move it.
	obj := new(*comObject)
	hr := e.com().call(uiaGetCurrentPatternAs, uintptr(id), out(iid), out(obj)) //nolint:gosec // A pattern id is a small positive number.
	switch {
	case hr == hrNoInterface || hr == hrNotSupported:
		return nil, ErrNoPattern
	case hr == hrElementNotAvailable:
		return nil, fmt.Errorf("%w: the element went away", ErrNotFound)
	case hr.failed():
		return nil, fmt.Errorf("GetCurrentPatternAs(%d): %w", id, hr)
	case *obj == nil:
		return nil, ErrNoPattern
	}
	return *obj, nil
}

func (e *element) currentIsEnabled() (bool, error) {
	return e.cachedBool("get_CurrentIsEnabled", uiaGetCurrentIsEnabled)
}

// invoke presses a button, menu item, or link.
func (e *element) invoke() error {
	p, err := e.patternAs(uiaInvokePatternID, &iidInvokePattern)
	if err != nil {
		return err
	}
	defer p.release()
	return check("Invoke", p.call(uiaInvokeInvoke))
}

// toggle flips a checkbox.
func (e *element) toggle() error {
	p, err := e.patternAs(uiaTogglePatternID, &iidTogglePattern)
	if err != nil {
		return err
	}
	defer p.release()
	return check("Toggle", p.call(uiaToggleToggle))
}

// selectItem selects a radio button, list item, tab, or cell.
func (e *element) selectItem() error {
	p, err := e.patternAs(uiaSelectionItemPatternID, &iidSelectionItemPattern)
	if err != nil {
		return err
	}
	defer p.release()
	return check("Select", p.call(uiaSelectionItemSelect))
}

// expand opens a collapsed element, such as a combo box's list.
func (e *element) expand() error {
	p, err := e.patternAs(uiaExpandCollapsePatternID, &iidExpandCollapsePattern)
	if err != nil {
		return err
	}
	defer p.release()
	var state int32
	if err := check("get_CurrentExpandCollapseState", p.call(uiaExpandCollapseGetCurrentState, out(&state))); err != nil {
		return err
	}
	switch state {
	case expandCollapseExpanded:
		return nil
	case expandCollapseLeafNode:
		return errLeaf
	}
	return check("Expand", p.call(uiaExpandCollapseExpand))
}

// setValue replaces the text of a field. The application sees one change,
// as from WM_SETTEXT, not key presses, so a validator that runs per key
// never runs.
func (e *element) setValue(text string) error {
	p, err := e.patternAs(uiaValuePatternID, &iidValuePattern)
	if err != nil {
		return err
	}
	defer p.release()
	var readOnly int32
	if err := check("get_CurrentIsReadOnly", p.call(uiaValueGetCurrentIsReadOnly, out(&readOnly))); err != nil {
		return err
	}
	if readOnly != 0 {
		return errReadOnly
	}
	b, err := bstrAlloc(text)
	if err != nil {
		return err
	}
	defer bstrFree(b)
	return check("SetValue", p.call(uiaValueSetValue, out(b)))
}

// reveal scrolls an element into view when it can be, and does nothing
// for one that cannot.
func (e *element) reveal() error {
	p, err := e.patternAs(uiaScrollItemPatternID, &iidScrollItemPattern)
	if errors.Is(err, ErrNoPattern) {
		return nil
	}
	if err != nil {
		return err
	}
	defer p.release()
	return check("ScrollIntoView", p.call(uiaScrollItemScrollIntoView))
}

// perform has a located, enabled element carry out a pattern.
func perform(el *element, pattern Pattern, value string) error {
	if pattern == PatternInvoke || pattern == PatternToggle || pattern == PatternSelect {
		if err := el.reveal(); err != nil {
			return err
		}
	}
	switch pattern {
	case PatternInvoke:
		return el.invoke()
	case PatternToggle:
		return el.toggle()
	case PatternSelect:
		return el.selectItem()
	case PatternExpand:
		return el.expand()
	case PatternSetValue:
		return el.setValue(value)
	default:
		return fmt.Errorf("unknown pattern %q", pattern)
	}
}
