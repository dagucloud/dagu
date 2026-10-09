// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"fmt"
	"image"
)

// UI Automation client interfaces, as thin wrappers over their vtables.
// Method indexes come from UIAutomationClient.h in Windows SDK 10.0.26100;
// IUnknown holds indexes 0 to 2. The interfaces are versioned by
// appending, so the indexes are stable.

var (
	clsidCUIAutomation8 = mustGUID("{E22AD333-B25F-460C-83D0-0581107395C9}")
	clsidCUIAutomation  = mustGUID("{FF48DBA4-60EF-4201-AA87-54103EEF594E}")
	iidIUIAutomation    = mustGUID("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}")
)

// Property ids.
const (
	uiaRuntimeIdProperty              = 30000
	uiaBoundingRectangleProperty      = 30001
	uiaControlTypeProperty            = 30003
	uiaNameProperty                   = 30005
	uiaAutomationIdProperty           = 30011
	uiaClassNameProperty              = 30012
	uiaLabeledByProperty              = 30018
	uiaIsPasswordProperty             = 30019
	uiaNativeWindowHandleProperty     = 30020
	uiaIsOffscreenProperty            = 30022
	uiaFrameworkIdProperty            = 30024
	uiaValueValueProperty             = 30045
	uiaLegacyIAccessibleValueProperty = 30093
)

// Control type ids.
const (
	uiaButton      = 50000
	uiaCheckBox    = 50002
	uiaComboBox    = 50003
	uiaEdit        = 50004
	uiaHyperlink   = 50005
	uiaListItem    = 50007
	uiaMenuItem    = 50011
	uiaRadioButton = 50013
	uiaTabItem     = 50019
	uiaText        = 50020
	uiaTreeItem    = 50024
	uiaGroup       = 50026
	uiaDataItem    = 50029
	uiaDocument    = 50030
	uiaSplitButton = 50031
	uiaWindow      = 50032
	uiaHeaderItem  = 50035
)

// Tree scopes and the element mode a cache request takes.
const (
	treeScopeElement  = 1
	treeScopeChildren = 2
	treeScopeSubtree  = 7

	automationElementModeFull = 1
)

// IUIAutomation (UIAutomationClient.h, IUIAutomationVtbl).
const (
	uiaCompareElements             = 3
	uiaGetRootElementBuildCache    = 9
	uiaElementFromHandleBuildCache = 10
	uiaElementFromPointBuildCache  = 11
	uiaGetFocusedElementBuildCache = 12
	uiaGetControlViewWalker        = 14
	uiaGetControlViewCondition     = 18
	uiaCreateCacheRequest          = 20
)

// IUIAutomationElement (IUIAutomationElementVtbl).
const (
	uiaSetFocus                   = 3
	uiaBuildUpdatedCache          = 9
	uiaGetCachedPropertyValue     = 12
	uiaGetCachedChildren          = 19
	uiaGetCurrentName             = 23
	uiaGetCachedControlType       = 53
	uiaGetCachedName              = 55
	uiaGetCachedAutomationId      = 61
	uiaGetCachedClassName         = 62
	uiaGetCachedIsPassword        = 67
	uiaGetCachedIsOffscreen       = 70
	uiaGetCachedBoundingRectangle = 75
	uiaGetCachedLabeledBy         = 76
)

// IUIAutomationElementArray, IUIAutomationCacheRequest, and
// IUIAutomationTreeWalker.
const (
	uiaArrayGetLength  = 3
	uiaArrayGetElement = 4

	uiaCacheAddProperty    = 3
	uiaCachePutTreeScope   = 7
	uiaCachePutTreeFilter  = 9
	uiaCachePutElementMode = 11

	uiaWalkerGetParentBuildCache = 9
)

type (
	automation   comObject
	element      comObject
	elementArray comObject
	cacheRequest comObject
	condition    comObject
	treeWalker   comObject
)

func (a *automation) com() *comObject   { return (*comObject)(a) }
func (e *element) com() *comObject      { return (*comObject)(e) }
func (a *elementArray) com() *comObject { return (*comObject)(a) }
func (c *cacheRequest) com() *comObject { return (*comObject)(c) }
func (c *condition) com() *comObject    { return (*comObject)(c) }
func (w *treeWalker) com() *comObject   { return (*comObject)(w) }

func (a *automation) release()   { a.com().release() }
func (e *element) release()      { e.com().release() }
func (a *elementArray) release() { a.com().release() }
func (c *cacheRequest) release() { c.com().release() }
func (c *condition) release()    { c.com().release() }
func (w *treeWalker) release()   { w.com().release() }

// check turns a failed HRESULT into an error naming the call.
func check(what string, hr hresult) error {
	if hr.failed() {
		return fmt.Errorf("%s: %w", what, hr)
	}
	return nil
}

// pointArg packs a POINT, which UI Automation takes by value, into the
// one register a 64-bit call passes it in.
func pointArg(x, y int) uintptr {
	return uintptr(uint32(int32(x))) | uintptr(uint32(int32(y)))<<32 //nolint:gosec // Coordinates fit in 32 bits.
}

func (a *automation) compareElements(x, y *element) (bool, error) {
	var same int32
	if err := check("CompareElements", a.com().call(uiaCompareElements, out(x), out(y), out(&same))); err != nil {
		return false, err
	}
	return same != 0, nil
}

func (a *automation) rootElement(cache *cacheRequest) (*element, error) {
	var el *element
	if err := check("GetRootElement", a.com().call(uiaGetRootElementBuildCache, out(cache), out(&el))); err != nil {
		return nil, err
	}
	return el, nil
}

func (a *automation) elementFromHandle(hwnd uintptr, cache *cacheRequest) (*element, error) {
	var el *element
	if err := check("ElementFromHandle", a.com().call(uiaElementFromHandleBuildCache, hwnd, out(cache), out(&el))); err != nil {
		return nil, err
	}
	return el, nil
}

func (a *automation) elementFromPoint(x, y int, cache *cacheRequest) (*element, error) {
	var el *element
	if err := check("ElementFromPoint", a.com().call(uiaElementFromPointBuildCache, pointArg(x, y), out(cache), out(&el))); err != nil {
		return nil, err
	}
	return el, nil
}

func (a *automation) focusedElement(cache *cacheRequest) (*element, error) {
	var el *element
	if err := check("GetFocusedElement", a.com().call(uiaGetFocusedElementBuildCache, out(cache), out(&el))); err != nil {
		return nil, err
	}
	return el, nil
}

func (a *automation) controlViewWalker() (*treeWalker, error) {
	var w *treeWalker
	if err := check("get_ControlViewWalker", a.com().call(uiaGetControlViewWalker, out(&w))); err != nil {
		return nil, err
	}
	return w, nil
}

func (a *automation) controlViewCondition() (*condition, error) {
	var c *condition
	if err := check("get_ControlViewCondition", a.com().call(uiaGetControlViewCondition, out(&c))); err != nil {
		return nil, err
	}
	return c, nil
}

func (a *automation) createCacheRequest() (*cacheRequest, error) {
	var c *cacheRequest
	if err := check("CreateCacheRequest", a.com().call(uiaCreateCacheRequest, out(&c))); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *cacheRequest) addProperty(id int32) error {
	return check("AddProperty", c.com().call(uiaCacheAddProperty, uintptr(id)))
}

func (c *cacheRequest) setTreeScope(scope int32) error {
	return check("put_TreeScope", c.com().call(uiaCachePutTreeScope, uintptr(scope)))
}

func (c *cacheRequest) setTreeFilter(filter *condition) error {
	return check("put_TreeFilter", c.com().call(uiaCachePutTreeFilter, out(filter)))
}

func (c *cacheRequest) setElementMode(mode int32) error {
	return check("put_AutomationElementMode", c.com().call(uiaCachePutElementMode, uintptr(mode)))
}

func (w *treeWalker) parent(el *element, cache *cacheRequest) (*element, error) {
	var parent *element
	if err := check("GetParentElement", w.com().call(uiaWalkerGetParentBuildCache, out(el), out(cache), out(&parent))); err != nil {
		return nil, err
	}
	return parent, nil
}

func (e *element) setFocus() error {
	return check("SetFocus", e.com().call(uiaSetFocus))
}

func (e *element) buildUpdatedCache(cache *cacheRequest) (*element, error) {
	var updated *element
	if err := check("BuildUpdatedCache", e.com().call(uiaBuildUpdatedCache, out(cache), out(&updated))); err != nil {
		return nil, err
	}
	return updated, nil
}

// cachedText returns a cached property that is a string, through the
// value variant, so a property the element lacks reads as "".
func (e *element) cachedText(id int32) (string, error) {
	var v variant
	if err := check(fmt.Sprintf("GetCachedPropertyValue(%d)", id), e.com().call(uiaGetCachedPropertyValue, uintptr(id), out(&v))); err != nil {
		return "", err
	}
	defer v.clear()
	return v.text(), nil
}

// cachedInt returns a cached property that is a number, such as a window
// handle, or 0 when the element lacks it.
func (e *element) cachedInt(id int32) (int32, error) {
	var v variant
	if err := check(fmt.Sprintf("GetCachedPropertyValue(%d)", id), e.com().call(uiaGetCachedPropertyValue, uintptr(id), out(&v))); err != nil {
		return 0, err
	}
	defer v.clear()
	return v.integer(), nil
}

func (e *element) cachedChildren() (*elementArray, error) {
	var children *elementArray
	if err := check("GetCachedChildren", e.com().call(uiaGetCachedChildren, out(&children))); err != nil {
		return nil, err
	}
	return children, nil
}

func (e *element) cachedControlType() (int32, error) {
	var ct int32
	if err := check("get_CachedControlType", e.com().call(uiaGetCachedControlType, out(&ct))); err != nil {
		return 0, err
	}
	return ct, nil
}

// cachedString reads a cached BSTR property getter.
func (e *element) cachedString(what string, index int) (string, error) {
	var b *uint16
	if err := check(what, e.com().call(index, out(&b))); err != nil {
		return "", err
	}
	defer bstrFree(b)
	return bstrString(b), nil
}

func (e *element) cachedName() (string, error) {
	return e.cachedString("get_CachedName", uiaGetCachedName)
}

func (e *element) currentName() (string, error) {
	return e.cachedString("get_CurrentName", uiaGetCurrentName)
}

func (e *element) cachedAutomationID() (string, error) {
	return e.cachedString("get_CachedAutomationId", uiaGetCachedAutomationId)
}

func (e *element) cachedClassName() (string, error) {
	return e.cachedString("get_CachedClassName", uiaGetCachedClassName)
}

// cachedBool reads a cached BOOL property getter.
func (e *element) cachedBool(what string, index int) (bool, error) {
	var v int32
	if err := check(what, e.com().call(index, out(&v))); err != nil {
		return false, err
	}
	return v != 0, nil
}

func (e *element) cachedIsPassword() (bool, error) {
	return e.cachedBool("get_CachedIsPassword", uiaGetCachedIsPassword)
}

func (e *element) cachedIsOffscreen() (bool, error) {
	return e.cachedBool("get_CachedIsOffscreen", uiaGetCachedIsOffscreen)
}

// rect is a Win32 RECT.
type rect struct {
	left, top, right, bottom int32
}

func (e *element) cachedBoundingRectangle() (image.Rectangle, error) {
	var r rect
	if err := check("get_CachedBoundingRectangle", e.com().call(uiaGetCachedBoundingRectangle, out(&r))); err != nil {
		return image.Rectangle{}, err
	}
	return image.Rect(int(r.left), int(r.top), int(r.right), int(r.bottom)), nil
}

// cachedLabeledBy returns the element that labels this one, or nil.
func (e *element) cachedLabeledBy() (*element, error) {
	var label *element
	if err := check("get_CachedLabeledBy", e.com().call(uiaGetCachedLabeledBy, out(&label))); err != nil {
		return nil, err
	}
	return label, nil
}

func (a *elementArray) length() (int, error) {
	var n int32
	if err := check("get_Length", a.com().call(uiaArrayGetLength, out(&n))); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (a *elementArray) element(i int) (*element, error) {
	var el *element
	if err := check("GetElement", a.com().call(uiaArrayGetElement, uintptr(i), out(&el))); err != nil { //nolint:gosec // i is a small index.
		return nil, err
	}
	return el, nil
}
