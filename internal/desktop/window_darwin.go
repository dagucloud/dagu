// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"errors"
	"image"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

const appKitPath = "/System/Library/Frameworks/AppKit.framework/AppKit"

// Options of CGWindowListCopyWindowInfo: windows on the screen, without
// the desktop's own.
const (
	windowListOnScreenOnly        = 1 << 0
	windowListExcludeDesktopItems = 1 << 4
)

// Window levels of the window server.
const (
	// normalWindowLayer holds applications' document and main windows.
	normalWindowLayer = 0
	// mainMenuWindowLayer holds the menu bar.
	mainMenuWindowLayer = 24
	// overlayWindowLayer and above hold overlays, the screen saver, and the
	// cursor, which never take a click meant for a window under them.
	overlayWindowLayer = 1000
)

// frameTolerance is how far, in points, a window's frame may be from the
// work area and still count as maximized, and how far an Accessibility
// frame may be from the window server's for the two to name one window.
const frameTolerance = 2

// raiseWait bounds how long a click waits for the window it lands on to
// come forward, polling every raisePoll; a window that never does still
// gets the click.
const (
	raiseWait = time.Second
	raisePoll = 20 * time.Millisecond
)

// windowInfo is one window the window server lists.
type windowInfo struct {
	id    WindowID
	layer int64
}

// listWindows lists the windows on the screen, the front first.
func (b *darwinBackend) listWindows() []windowInfo {
	list := cgWindowListCopyWindowInfo(windowListOnScreenOnly|windowListExcludeDesktopItems, 0)
	if list == 0 {
		return nil
	}
	defer cfRelease(list)
	apps := map[int64]string{}
	var out []windowInfo
	for _, dict := range cfArray(list) {
		number, _ := cfInt(cfDictionaryGetValue(dict, cgWindowNumberKey))
		pid, _ := cfInt(cfDictionaryGetValue(dict, cgWindowOwnerPIDKey))
		layer, _ := cfInt(cfDictionaryGetValue(dict, cgWindowLayerKey))
		if alpha, ok := cfFloat(cfDictionaryGetValue(dict, cgWindowAlphaKey)); ok && alpha == 0 {
			continue
		}
		var frame cgRect
		if bounds := cfDictionaryGetValue(dict, cgWindowBoundsKey); bounds == 0 || !cgRectMakeWithDictionaryRepresentation(bounds, &frame) {
			continue
		}
		rect := b.toPixelRect(frame)
		if number <= 0 || pid <= 0 || rect.Empty() {
			continue
		}
		app, ok := apps[pid]
		if !ok {
			app = processApp(int32(pid)) //nolint:gosec // process ids fit 32 bits
			apps[pid] = app
		}
		out = append(out, windowInfo{
			id: WindowID{
				Handle: uint64(number), //nolint:gosec // window numbers are positive
				Title:  goString(cfDictionaryGetValue(dict, cgWindowNameKey)),
				PID:    uint32(pid), //nolint:gosec // process ids fit 32 bits
				App:    app,
				Bounds: rect,
			},
			layer: layer,
		})
	}
	return out
}

// toPixelRect converts a frame in global points to display pixels.
func (b *darwinBackend) toPixelRect(frame cgRect) image.Rectangle {
	return b.display.pixelRect(frame)
}

// points converts a position in the display's pixels to global points.
func (d display) points(at image.Point) cgPoint {
	return cgPoint{X: d.bounds.Origin.X + float64(at.X)/d.scale, Y: d.bounds.Origin.Y + float64(at.Y)/d.scale}
}

// pixelRect converts a frame in global points to the display's pixels.
func (d display) pixelRect(frame cgRect) image.Rectangle {
	scale, origin := d.scale, d.bounds.Origin
	return image.Rect(
		int(math.Round((frame.Origin.X-origin.X)*scale)),
		int(math.Round((frame.Origin.Y-origin.Y)*scale)),
		int(math.Round((frame.Origin.X+frame.Size.Width-origin.X)*scale)),
		int(math.Round((frame.Origin.Y+frame.Size.Height-origin.Y)*scale)),
	)
}

// processApp is a process's executable name, such as TextEdit: unlike the
// name the window server shows, it does not change with the language of
// the user interface.
func processApp(pid int32) string {
	buffer := make([]byte, procPIDPathMax)
	n := procPIDPath(pid, &buffer[0], procPIDPathMax)
	if n <= 0 {
		return ""
	}
	return filepath.Base(string(buffer[:n]))
}

// Windows lists the applications' windows on the screen, the front first.
func (b *darwinBackend) Windows() []WindowID {
	var out []WindowID
	for _, w := range b.listWindows() {
		if w.layer == normalWindowLayer {
			out = append(out, w.id)
		}
	}
	return out
}

// WindowAt names the front window under a position, menus and the menu
// bar included, and overlays and windows that let clicks through left out.
func (b *darwinBackend) WindowAt(x, y int) WindowID {
	w, _ := windowAt(b.listWindows(), image.Pt(x, y), b.ownerAt)
	return w.id
}

// windowAt names the front window under a position among windows, the
// front first, menus and the menu bar included and overlays left out. A
// window above the applications' is passed over when the element under the
// position belongs to another process, as ownerAt tells: Notification
// Center covers the screen while a banner shows, and lets every click
// outside the banner through. The menu bar, which the window server draws
// for the front application, is taken as it is.
func windowAt(windows []windowInfo, at image.Point, ownerAt func(image.Point) (uint32, bool)) (windowInfo, bool) {
	var owner uint32
	asked, known := false, false
	for _, w := range windows {
		if w.layer >= overlayWindowLayer || !at.In(w.id.Bounds) {
			continue
		}
		if w.layer != normalWindowLayer && w.layer != mainMenuWindowLayer {
			if !asked {
				owner, known = ownerAt(at)
				asked = true
			}
			if known && owner != w.id.PID {
				continue
			}
		}
		return w, true
	}
	return windowInfo{}, false
}

// ownerAt is the process that owns the element under a position, as the
// Accessibility hit test finds it, through windows that let clicks
// through. It is unknown when the hit test finds nothing.
func (b *darwinBackend) ownerAt(at image.Point) (uint32, bool) {
	system := axUIElementCreateSystemWide()
	if system == 0 {
		return 0, false
	}
	defer cfRelease(system)
	point := b.display.points(at)
	var element uintptr
	if code := axUIElementCopyElementAtPosition(system, float32(point.X), float32(point.Y), &element); code != axErrorSuccess || element == 0 {
		return 0, false
	}
	defer cfRelease(element)
	var pid int32
	if axUIElementGetPid(element, &pid) != axErrorSuccess || pid <= 0 {
		return 0, false
	}
	return uint32(pid), true
}

// FocusedWindow names the front window of the application that has the
// keyboard focus.
func (b *darwinBackend) FocusedWindow() WindowID {
	pid := focusedApplication()
	for _, w := range b.listWindows() {
		if w.layer == normalWindowLayer && (pid == 0 || w.id.PID == uint32(pid)) { //nolint:gosec // process ids are positive
			return w.id
		}
	}
	return WindowID{}
}

// focusedApplication is the process of the application that has the
// keyboard focus, or 0 when the system does not say. Accessibility's
// system-wide answer is asked first; it can fail for a process started from
// a service or a terminal, and the owner of the front application window,
// which the window server orders first, answers then.
func focusedApplication() int32 {
	if system := axUIElementCreateSystemWide(); system != 0 {
		app := axCopy(system, "AXFocusedApplication")
		cfRelease(system)
		if app != 0 {
			var pid int32
			code := axUIElementGetPid(app, &pid)
			cfRelease(app)
			if code == axErrorSuccess && pid > 0 {
				return pid
			}
		}
	}
	return frontWindowOwner()
}

// frontWindowOwner is the process that owns the front application window.
func frontWindowOwner() int32 {
	list := cgWindowListCopyWindowInfo(windowListOnScreenOnly|windowListExcludeDesktopItems, 0)
	if list == 0 {
		return 0
	}
	defer cfRelease(list)
	for _, dict := range cfArray(list) {
		layer, _ := cfInt(cfDictionaryGetValue(dict, cgWindowLayerKey))
		if alpha, ok := cfFloat(cfDictionaryGetValue(dict, cgWindowAlphaKey)); layer != normalWindowLayer || (ok && alpha == 0) {
			continue
		}
		if pid, ok := cfInt(cfDictionaryGetValue(dict, cgWindowOwnerPIDKey)); ok && pid > 0 {
			return int32(pid) //nolint:gosec // process ids fit 32 bits
		}
	}
	return 0
}

// clickTarget names the application window a click at a position lands on,
// the front one there. A menu, the menu bar, or a panel above the
// application windows takes the click itself, so none is named; overlays
// and windows that let clicks through never take one, so the window under
// them is.
func clickTarget(windows []windowInfo, at image.Point, ownerAt func(image.Point) (uint32, bool)) (WindowID, bool) {
	w, ok := windowAt(windows, at, ownerAt)
	if !ok || w.layer != normalWindowLayer {
		return WindowID{}, false
	}
	return w.id, true
}

// bringForwardAt raises the application window under a position unless it
// has the focus, and waits until it takes the focus. macOS hands a click on
// a window in the background to it only when the application takes the
// first click; Chromium-based applications, among many others, just come
// forward, and the click is lost.
func (b *darwinBackend) bringForwardAt(at image.Point) {
	target, ok := clickTarget(b.listWindows(), at, b.ownerAt)
	if !ok || b.hasFocus(target) {
		return
	}
	_ = b.Raise(target)
	deadline := time.Now().Add(raiseWait)
	for !b.hasFocus(target) && time.Now().Before(deadline) {
		time.Sleep(raisePoll)
	}
}

// hasFocus reports whether a window has the focus as its application tells
// it: the application is active and the window is its focused one. The
// focused window the system names cannot stand in, since while the active
// application shows no window, it names the owner of the front window. An
// application that does not tell counts as focused, so its windows are
// clicked as before.
func (b *darwinBackend) hasFocus(w WindowID) bool {
	app := axUIElementCreateApplication(int32(w.PID)) //nolint:gosec // process ids fit 32 bits
	if app == 0 {
		return true
	}
	defer cfRelease(app)
	active, ok := axBool(app, "AXFrontmost")
	if !ok {
		return true
	}
	if !active {
		return false
	}
	focused := axCopy(app, "AXFocusedWindow")
	if focused == 0 {
		return true
	}
	defer cfRelease(focused)
	window := b.axWindow(app, w)
	if window == 0 {
		return true
	}
	defer cfRelease(window)
	return cfEqual(focused, window)
}

// Raise brings a window's application to the front and the window above
// its other windows.
func (b *darwinBackend) Raise(w WindowID) error {
	if w.PID == 0 {
		return nil
	}
	app := axUIElementCreateApplication(int32(w.PID)) //nolint:gosec // process ids fit 32 bits
	if app == 0 {
		return nil
	}
	defer cfRelease(app)
	_ = axSet(app, "AXFrontmost", cfBooleanTrue)
	if window := b.axWindow(app, w); window != 0 {
		_ = axPerform(window, "AXRaise")
		cfRelease(window)
	}
	return nil
}

// Maximize fills the main display's work area, between the menu bar and
// the Dock, with a standard window that can be zoomed. It does not use full
// screen, which would move the window to a Space of its own.
func (b *darwinBackend) Maximize(w WindowID) (bool, error) {
	if w.PID == 0 {
		return false, nil
	}
	app := axUIElementCreateApplication(int32(w.PID)) //nolint:gosec // process ids fit 32 bits
	if app == 0 {
		return false, nil
	}
	defer cfRelease(app)
	window := b.axWindow(app, w)
	if window == 0 {
		return false, nil
	}
	defer cfRelease(window)
	if !zoomable(window) {
		return false, nil
	}
	area, err := b.workArea()
	if err != nil {
		return false, err
	}
	if frame, ok := axFrame(window); ok && sameFrame(frame, area) {
		return false, nil
	}
	// The position goes first so the new size fits the display, and again
	// last, since an application may move a window it resizes.
	if err := axSetPoint(window, "AXPosition", area.Origin); err != nil {
		return false, err
	}
	if err := axSetSize(window, "AXSize", area.Size); err != nil {
		return false, err
	}
	_ = axSetPoint(window, "AXPosition", area.Origin)
	return true, nil
}

// zoomable reports a standard window, not minimized or in full screen,
// whose zoom button is enabled and whose frame can be set: a dialog, a
// panel, a sheet, or a window of fixed size is left as it is.
func zoomable(window uintptr) bool {
	if subrole := axString(window, "AXSubrole"); subrole != "AXStandardWindow" {
		return false
	}
	if minimized, _ := axBool(window, "AXMinimized"); minimized {
		return false
	}
	if full, _ := axBool(window, "AXFullScreen"); full {
		return false
	}
	button := axCopy(window, "AXZoomButton")
	if button == 0 {
		return false
	}
	enabled, ok := axBool(button, "AXEnabled")
	cfRelease(button)
	if ok && !enabled {
		return false
	}
	return axSettable(window, "AXSize") && axSettable(window, "AXPosition")
}

// axWindow finds the Accessibility window of an application that the
// window server lists as w: by its frame, else by its title. The caller
// releases a non-zero window.
func (b *darwinBackend) axWindow(app uintptr, w WindowID) uintptr {
	windows := axCopy(app, "AXWindows")
	if windows == 0 {
		return 0
	}
	defer cfRelease(windows)
	var byTitle uintptr
	for _, window := range cfArray(windows) {
		if frame, ok := axFrame(window); ok && nearRect(b.toPixelRect(frame), w.Bounds, int(math.Ceil(frameTolerance*b.display.scale))) {
			cfRetain(window)
			return window
		}
		if byTitle == 0 && w.Title != "" && axString(window, "AXTitle") == w.Title {
			byTitle = window
		}
	}
	if byTitle != 0 {
		cfRetain(byTitle)
	}
	return byTitle
}

// workArea is the main display's frame without the menu bar and the Dock,
// in global points.
func (b *darwinBackend) workArea() (cgRect, error) {
	if area, ok := visibleFrame(); ok {
		return area, nil
	}
	// Without AppKit, the work area is the display below the menu bar.
	area := b.display.bounds
	for _, w := range b.listWindows() {
		if w.layer == mainMenuWindowLayer && w.id.Bounds.Min.Y == 0 {
			top := float64(w.id.Bounds.Dy()) / b.display.scale
			area.Origin.Y += top
			area.Size.Height -= top
			break
		}
	}
	if area.Size.Width <= 0 || area.Size.Height <= 0 {
		return cgRect{}, errors.New("the display's work area is unavailable")
	}
	return area, nil
}

var (
	appKitOnce sync.Once
	appKitErr  error
)

// visibleFrame reads the main screen's visible frame from AppKit, which
// knows where the menu bar and the Dock are, and converts it to global
// points with the origin at the top left.
func visibleFrame() (cgRect, bool) {
	appKitOnce.Do(func() {
		_, appKitErr = purego.Dlopen(appKitPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	})
	if appKitErr != nil {
		return cgRect{}, false
	}
	screens := objc.ID(objc.GetClass("NSScreen")).Send(objc.RegisterName("screens"))
	if screens == 0 || objc.Send[uintptr](screens, objc.RegisterName("count")) == 0 {
		return cgRect{}, false
	}
	// The first screen holds the menu bar: it is the main display, whose
	// bottom left is AppKit's origin.
	main := screens.Send(objc.RegisterName("objectAtIndex:"), uintptr(0))
	frame := objc.Send[cgRect](main, objc.RegisterName("frame"))
	visible := objc.Send[cgRect](main, objc.RegisterName("visibleFrame"))
	if visible.Size.Width <= 0 || visible.Size.Height <= 0 {
		return cgRect{}, false
	}
	return cgRect{
		Origin: cgPoint{X: visible.Origin.X, Y: frame.Size.Height - visible.Origin.Y - visible.Size.Height},
		Size:   visible.Size,
	}, true
}

// sameFrame reports frames within frameTolerance points of each other.
func sameFrame(a, b cgRect) bool {
	return math.Abs(a.Origin.X-b.Origin.X) <= frameTolerance && math.Abs(a.Origin.Y-b.Origin.Y) <= frameTolerance &&
		math.Abs(a.Size.Width-b.Size.Width) <= frameTolerance && math.Abs(a.Size.Height-b.Size.Height) <= frameTolerance
}

// nearRect reports rectangles whose corners are within tolerance pixels.
func nearRect(a, b image.Rectangle, tolerance int) bool {
	return abs(a.Min.X-b.Min.X) <= tolerance && abs(a.Min.Y-b.Min.Y) <= tolerance &&
		abs(a.Max.X-b.Max.X) <= tolerance && abs(a.Max.Y-b.Max.Y) <= tolerance
}
