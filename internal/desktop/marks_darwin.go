// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// marksHelperArgs start this program as the helper that draws marks: the
// hidden command internal/cmd registers as dagu computer marks. AppKit
// draws windows only on a process's main thread, which a step running in
// a goroutine of the dagu process does not have.
var marksHelperArgs = []string{"computer", "marks"}

func init() {
	// An init function that locks the main thread keeps main, and so the
	// helper's command, on it.
	if slices.Equal(os.Args[1:], marksHelperArgs) {
		runtime.LockOSThread()
	}
}

const (
	foundationPath = "/System/Library/Frameworks/Foundation.framework/Foundation"
	libObjCPath    = "/usr/lib/libobjc.A.dylib"

	// markReplyOK is the helper's answer once it is ready and once each
	// request is done; any other line says why a request failed.
	markReplyOK = "ok"
	// helperStart bounds how long the helper may take to start, and
	// helperReply how long it may take to show or hide a mark or to end,
	// so one that hangs cannot hold the actions up for long.
	helperStart = 5 * time.Second
	helperReply = time.Second
	// markFontPoints is the size of a label's text.
	markFontPoints = 12
)

// AppKit constants.
const (
	activationPolicyProhibited  = 2
	backingStoreBuffered        = 2
	styleMaskBorderless         = 0
	windowSharingNone           = 0
	eventTypeApplicationDefined = 15
	eventMaskAny                = ^uint64(0)
	// The mark shows on every Space and over full-screen windows, and
	// stays out of window cycling.
	collectionBehavior = 1<<0 | 1<<4 | 1<<6 | 1<<8
	// markWindowLevel is the screen saver's level, among the overlays no
	// click is taken to land in.
	markWindowLevel = overlayWindowLayer
)

// markRequest is one line the helper reads: a mark to show, in points of
// the global display space, or a request to hide the one shown.
type markRequest struct {
	Hide   bool     `json:"hide,omitempty"`
	X      float64  `json:"x,omitempty"`
	Y      float64  `json:"y,omitempty"`
	Width  float64  `json:"width,omitempty"`
	Height float64  `json:"height,omitempty"`
	Label  string   `json:"label,omitempty"`
	Color  [3]uint8 `json:"color"`
}

// OpenMarks opens marks drawn on the desktop of the current session by a
// helper process, this program started as dagu computer marks, in a window
// macOS shares with no capture of the screen. It fails unless the program
// registered the helper with RegisterMarksHelper.
func OpenMarks() (Marks, error) {
	if !marksHelper.Load() {
		return nil, errNoMarksHelper
	}
	if err := load(); err != nil {
		return nil, err
	}
	d, err := mainDisplay()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("find this program to draw marks: %w", err)
	}
	s, err := startMarksHelper(exec.Command(executable, marksHelperArgs...), d) //nolint:gosec // this program, started as its own helper
	if err != nil {
		return nil, err
	}
	return newMarks(s), nil
}

// helperSurface draws marks through the helper process.
type helperSurface struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	encoder *json.Encoder
	replies chan string
	display display
}

func startMarksHelper(cmd *exec.Cmd, d display) (*helperSurface, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the marks helper: %w", err)
	}
	s := &helperSurface{cmd: cmd, in: in, encoder: json.NewEncoder(in), replies: make(chan string, 1), display: d}
	go s.read(out)
	if err := s.await(helperStart); err != nil {
		_ = s.close()
		return nil, fmt.Errorf("start the marks helper: %w", err)
	}
	return s, nil
}

func (s *helperSurface) read(out io.Reader) {
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		s.replies <- scanner.Text()
	}
	close(s.replies)
}

// await waits up to wait for the helper's answer.
func (s *helperSurface) await(wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case reply, ok := <-s.replies:
		switch {
		case !ok:
			return errors.New("the marks helper exited")
		case reply != markReplyOK:
			return errors.New(reply)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("the marks helper did not answer within %s", wait)
	}
}

func (s *helperSurface) request(r markRequest) error {
	if err := s.encoder.Encode(r); err != nil {
		return fmt.Errorf("send a mark to the helper: %w", err)
	}
	return s.await(helperReply)
}

func (s *helperSurface) draw(m Mark) error {
	from, to := s.display.points(m.Target.Min), s.display.points(m.Target.Max)
	return s.request(markRequest{
		X: from.X, Y: from.Y, Width: to.X - from.X, Height: to.Y - from.Y,
		Label: m.Label, Color: [3]uint8{m.Color.R, m.Color.G, m.Color.B},
	})
}

func (s *helperSurface) clear() error {
	return s.request(markRequest{Hide: true})
}

// close ends the helper at the end of its input, and kills one that does
// not end within helperReply.
func (s *helperSurface) close() error {
	_ = s.in.Close()
	timer := time.NewTimer(helperReply)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-s.replies:
			if !ok {
				return s.cmd.Wait()
			}
		case <-timer.C:
			_ = s.cmd.Process.Kill()
			_ = s.cmd.Wait()
			return fmt.Errorf("the marks helper did not end within %s", helperReply)
		}
	}
}

// helperMessage is one request the helper read, or why it could not read
// one.
type helperMessage struct {
	request markRequest
	err     error
}

// ServeMarks draws the marks of a step in another process, which sends
// them on in as one JSON object per line and reads one line on out for
// each: ok once the helper is ready, then ok once each request is done, or
// why it failed. It returns at the end of in. It runs only on the main
// thread, which this program keeps for dagu computer marks.
func ServeMarks(in io.Reader, out io.Writer) error {
	ui, err := loadAppKit()
	if err != nil {
		return err
	}
	if !ui.onMainThread() {
		return errors.New("marks are drawn only on the main thread")
	}
	app := objc.ID(objc.GetClass("NSApplication")).Send(objc.RegisterName("sharedApplication"))
	app.Send(objc.RegisterName("setActivationPolicy:"), activationPolicyProhibited)
	window := newMarkWindow()
	messages := make(chan helperMessage, 1)
	go ui.readRequests(in, messages, app)
	if _, err := fmt.Fprintln(out, markReplyOK); err != nil {
		return err
	}
	nextEvent := objc.RegisterName("nextEventMatchingMask:untilDate:inMode:dequeue:")
	future := objc.ID(objc.GetClass("NSDate")).Send(objc.RegisterName("distantFuture"))
	for {
		pool := ui.poolPush()
		event := app.Send(nextEvent, eventMaskAny, future, ui.defaultMode, true)
		if event != 0 && objc.Send[uint64](event, objc.RegisterName("type")) != eventTypeApplicationDefined {
			app.Send(objc.RegisterName("sendEvent:"), event)
		}
		ui.poolPop(pool)
		for drained := false; !drained; {
			select {
			case message, ok := <-messages:
				if !ok {
					return nil
				}
				reply := markReplyOK
				if message.err != nil {
					reply = message.err.Error()
				} else {
					window.apply(message.request)
				}
				if _, err := fmt.Fprintln(out, reply); err != nil {
					return err
				}
			default:
				drained = true
			}
		}
	}
}

// appKit holds what the helper calls besides Objective-C messages.
type appKit struct {
	defaultMode objc.ID
	poolPush    func() uintptr
	poolPop     func(pool uintptr)
	pthreadMain func() int32
}

func loadAppKit() (*appKit, error) {
	if _, err := purego.Dlopen(appKitPath, purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		return nil, fmt.Errorf("load AppKit: %w", err)
	}
	foundation, err := purego.Dlopen(foundationPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("load Foundation: %w", err)
	}
	mode, err := purego.Dlsym(foundation, "NSDefaultRunLoopMode")
	if err != nil {
		return nil, fmt.Errorf("load NSDefaultRunLoopMode: %w", err)
	}
	libObjC, err := purego.Dlopen(libObjCPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("load the Objective-C runtime: %w", err)
	}
	libSystem, err := purego.Dlopen(libSystemPath, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("load libSystem: %w", err)
	}
	// The symbol is the address of the variable that holds the string.
	ui := &appKit{defaultMode: *(*objc.ID)(unsafe.Add(nil, mode))} //nolint:gosec // reads a C global whose address dlsym returned
	purego.RegisterLibFunc(&ui.poolPush, libObjC, "objc_autoreleasePoolPush")
	purego.RegisterLibFunc(&ui.poolPop, libObjC, "objc_autoreleasePoolPop")
	purego.RegisterLibFunc(&ui.pthreadMain, libSystem, "pthread_main_np")
	return ui, nil
}

func (ui *appKit) onMainThread() bool { return ui.pthreadMain() != 0 }

// readRequests reads requests from in until it ends, and wakes the main
// thread's event loop after each, which reads them from messages.
func (ui *appKit) readRequests(in io.Reader, messages chan<- helperMessage, app objc.ID) {
	// An autorelease pool is pushed and popped on one thread.
	runtime.LockOSThread()
	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		var message helperMessage
		if err := json.Unmarshal(scanner.Bytes(), &message.request); err != nil {
			message.err = fmt.Errorf("read a mark: %w", err)
		}
		messages <- message
		ui.wake(app)
	}
	close(messages)
	ui.wake(app)
}

// wake posts an event of the application's own, which ends the main
// thread's wait for one.
func (ui *appKit) wake(app objc.ID) {
	pool := ui.poolPush()
	defer ui.poolPop(pool)
	event := objc.ID(objc.GetClass("NSEvent")).Send(
		objc.RegisterName("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:"),
		uint64(eventTypeApplicationDefined), cgPoint{}, uint64(0), float64(0), int64(0), objc.ID(0), int16(0), int64(0), int64(0),
	)
	app.Send(objc.RegisterName("postEvent:atStart:"), event, false)
}

// markWindow is the helper's window: a frame, and a box holding the label.
type markWindow struct {
	window objc.ID
	frame  objc.ID
	box    objc.ID
	text   objc.ID
	// screen is the main display in points.
	screen image.Rectangle
}

func newMarkWindow() *markWindow {
	sel := objc.RegisterName
	screens := objc.ID(objc.GetClass("NSScreen")).Send(sel("screens"))
	mainFrame := objc.Send[cgRect](screens.Send(sel("objectAtIndex:"), uint64(0)), sel("frame"))
	w := &markWindow{screen: image.Rect(0, 0, int(mainFrame.Size.Width), int(mainFrame.Size.Height))}
	w.window = objc.ID(objc.GetClass("NSWindow")).Send(sel("alloc")).Send(
		sel("initWithContentRect:styleMask:backing:defer:"),
		cgRect{Size: cgSize{Width: 1, Height: 1}}, uint64(styleMaskBorderless), uint64(backingStoreBuffered), false,
	)
	w.window.Send(sel("setReleasedWhenClosed:"), false)
	w.window.Send(sel("setOpaque:"), false)
	w.window.Send(sel("setBackgroundColor:"), objc.ID(objc.GetClass("NSColor")).Send(sel("clearColor")))
	w.window.Send(sel("setHasShadow:"), false)
	w.window.Send(sel("setIgnoresMouseEvents:"), true)
	w.window.Send(sel("setLevel:"), int64(markWindowLevel))
	w.window.Send(sel("setSharingType:"), uint64(windowSharingNone))
	w.window.Send(sel("setCollectionBehavior:"), uint64(collectionBehavior))
	content := w.window.Send(sel("contentView"))
	content.Send(sel("setWantsLayer:"), true)
	w.frame = layerView(content)
	w.frame.Send(sel("layer")).Send(sel("setBorderWidth:"), float64(markBorder))
	w.box = layerView(content)
	w.text = objc.ID(objc.GetClass("NSTextField")).Send(sel("labelWithString:"), nsString(""))
	w.text.Send(sel("setFont:"), objc.ID(objc.GetClass("NSFont")).Send(sel("boldSystemFontOfSize:"), float64(markFontPoints)))
	w.text.Send(sel("setTextColor:"), objc.ID(objc.GetClass("NSColor")).Send(sel("whiteColor")))
	w.box.Send(sel("addSubview:"), w.text)
	return w
}

// layerView adds a view drawn by its layer to parent.
func layerView(parent objc.ID) objc.ID {
	view := objc.ID(objc.GetClass("NSView")).Send(objc.RegisterName("alloc")).Send(objc.RegisterName("initWithFrame:"), cgRect{})
	view.Send(objc.RegisterName("setWantsLayer:"), true)
	parent.Send(objc.RegisterName("addSubview:"), view)
	return view
}

func nsString(text string) objc.ID {
	return objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), text)
}

// apply shows or hides the mark a request names, and returns once the
// window server has it.
func (w *markWindow) apply(r markRequest) {
	sel := objc.RegisterName
	if r.Hide {
		w.window.Send(sel("orderOut:"), objc.ID(0))
		flush()
		return
	}
	w.text.Send(sel("setStringValue:"), nsString(r.Label))
	w.text.Send(sel("sizeToFit"))
	text := objc.Send[cgRect](w.text, sel("frame")).Size
	label := image.Pt(int(math.Ceil(text.Width))+2*markPadding, int(math.Ceil(text.Height))+markPadding)
	target := image.Rect(int(math.Round(r.X)), int(math.Round(r.Y)), int(math.Round(r.X+r.Width)), int(math.Round(r.Y+r.Height)))
	window, frame, box := markLayout(target, w.screen, label, markBorder)
	if window.Empty() {
		w.window.Send(sel("orderOut:"), objc.ID(0))
		flush()
		return
	}
	color := objc.ID(objc.GetClass("NSColor")).Send(sel("colorWithSRGBRed:green:blue:alpha:"),
		float64(r.Color[0])/0xFF, float64(r.Color[1])/0xFF, float64(r.Color[2])/0xFF, float64(1))
	cgColor := color.Send(sel("CGColor"))
	w.window.Send(sel("setFrame:display:"), w.screenRect(window), true)
	w.frame.Send(sel("setFrame:"), viewRect(window, frame))
	w.frame.Send(sel("layer")).Send(sel("setBorderColor:"), cgColor)
	w.box.Send(sel("setFrame:"), viewRect(window, box))
	w.box.Send(sel("layer")).Send(sel("setBackgroundColor:"), cgColor)
	w.text.Send(sel("setFrameOrigin:"), cgPoint{X: markPadding, Y: markPadding / 2})
	w.window.Send(sel("orderFrontRegardless"))
	w.window.Send(sel("displayIfNeeded"))
	flush()
}

// screenRect converts a rectangle of the main display, whose origin is its
// top left, to AppKit's screen coordinates, whose origin is its bottom left.
func (w *markWindow) screenRect(r image.Rectangle) cgRect {
	return cgRect{
		Origin: cgPoint{X: float64(r.Min.X), Y: float64(w.screen.Max.Y - r.Max.Y)},
		Size:   cgSize{Width: float64(r.Dx()), Height: float64(r.Dy())},
	}
}

// viewRect converts a rectangle of the main display to the coordinates of
// a view filling window, whose origin is its bottom left.
func viewRect(window, r image.Rectangle) cgRect {
	return cgRect{
		Origin: cgPoint{X: float64(r.Min.X - window.Min.X), Y: float64(window.Max.Y - r.Max.Y)},
		Size:   cgSize{Width: float64(r.Dx()), Height: float64(r.Dy())},
	}
}

// flush sends the window's changes to the window server now.
func flush() {
	objc.ID(objc.GetClass("CATransaction")).Send(objc.RegisterName("flush"))
}
