// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Mark is a frame a person at the desktop sees around what an action is
// about to act on.
type Mark struct {
	// Target is what the action acts on, in display pixels.
	Target image.Rectangle
	// Label names the action and who acts.
	Label string
	Color color.RGBA
}

// Marks shows marks on the desktop. A mark takes no focus or input, lets
// clicks through, and is left out of every capture of the screen.
type Marks interface {
	// Show shows a mark in place of the one shown before, until a moment
	// passes or the next one shows.
	Show(Mark) error
	Close() error
}

var errMarksClosed = errors.New("the marks are closed")

// errNoMarksHelper reports a program that does not draw marks as a helper
// process: on macOS one that embeds Dagu, which a step must not start with
// arguments it may not know, and elsewhere every program, since steps draw
// their own marks there.
var errNoMarksHelper = errors.New("this program does not draw marks in a helper process")

// marksHelper is whether this program draws marks when started as the
// marks helper; see RegisterMarksHelper.
var marksHelper atomic.Bool

// RegisterMarksHelper tells OpenMarks that this program draws marks when
// started with the arguments of dagu computer marks, the command that
// calls ServeMarks. Without it, macOS shows no marks.
func RegisterMarksHelper() {
	marksHelper.Store(true)
}

// markLinger is how long a mark stays after it shows, long enough for a
// person to notice it and short enough not to outlast its action by much.
const markLinger = 2 * time.Second

// Size of a mark, in display pixels at the system's base resolution:
// pointMarkSize is the side of the square marking a point, markBorder the
// width of the frame, and markPadding the space around the label's text.
const (
	pointMarkSize = 48
	markBorder    = 3
	markPadding   = 4
	// maxMarkLabel bounds a label, in characters, so a long step name
	// does not cover the screen.
	maxMarkLabel = 60
)

// markPalette holds colors of marks that white text reads on, and that
// stand apart from one another.
var markPalette = [...]color.RGBA{
	{R: 0xC2, G: 0x41, B: 0x0C, A: 0xFF},
	{R: 0x1D, G: 0x4E, B: 0xD8, A: 0xFF},
	{R: 0x15, G: 0x80, B: 0x3D, A: 0xFF},
	{R: 0x7E, G: 0x22, B: 0xCE, A: 0xFF},
	{R: 0xBE, G: 0x12, B: 0x3C, A: 0xFF},
	{R: 0x0F, G: 0x76, B: 0x6E, A: 0xFF},
}

// markColor is the color of the marks of one name, the same every time.
func markColor(name string) color.RGBA {
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return markPalette[h.Sum32()%uint32(len(markPalette))]
}

// markLabel names an action and who acts, cut to maxMarkLabel characters.
func markLabel(name, action string) string {
	label := action
	if name != "" {
		label = name + " · " + action
	}
	if runes := []rune(label); len(runes) > maxMarkLabel {
		label = string(runes[:maxMarkLabel-1]) + "…"
	}
	return label
}

// pointMark is the target that marks a point.
func pointMark(at image.Point) image.Rectangle {
	half := image.Pt(pointMarkSize/2, pointMarkSize/2)
	return image.Rectangle{Min: at.Sub(half), Max: at.Add(half)}
}

// markLayout places a mark on screen: its frame around target, outside it
// by border so the frame leaves the target's own edges in view, and its
// label of size label at the frame's top left corner, above the frame, or
// below it where the screen has no room above, or inside the frame where it
// has none below either, as around a window that fills the screen. window
// is the area the frame and the label cover, empty for a target off the
// screen.
func markLayout(target, screen image.Rectangle, label image.Point, border int) (window, frame, box image.Rectangle) {
	frame = target.Inset(-border).Intersect(screen)
	if frame.Empty() {
		return image.Rectangle{}, frame, image.Rectangle{}
	}
	label.X = min(label.X, screen.Dx())
	at := image.Pt(frame.Min.X, frame.Min.Y-label.Y)
	if at.Y < screen.Min.Y {
		at.Y = frame.Max.Y
	}
	if at.Y+label.Y > screen.Max.Y {
		at.Y = frame.Min.Y + border
	}
	at.X = max(min(at.X, screen.Max.X-label.X), screen.Min.X)
	box = image.Rectangle{Min: at, Max: at.Add(label)}.Intersect(screen)
	return frame.Union(box), frame, box
}

// surface draws one mark at a time, the operating system's side of Marks.
type surface interface {
	draw(Mark) error
	clear() error
	close() error
}

// lingeringMarks shows each mark on a surface for linger, markLinger
// unless a test sets it. A surface that fails once is not asked again, so a
// broken one never delays the actions.
type lingeringMarks struct {
	surface surface
	linger  time.Duration

	mu     sync.Mutex
	shown  int
	err    error
	closed bool
}

func newMarks(s surface) *lingeringMarks {
	return &lingeringMarks{surface: s, linger: markLinger}
}

func (m *lingeringMarks) Show(mark Mark) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.closed:
		return errMarksClosed
	case m.err != nil:
		return m.err
	}
	if err := m.surface.draw(mark); err != nil {
		m.err = err
		return err
	}
	m.shown++
	shown := m.shown
	time.AfterFunc(m.linger, func() { m.lapse(shown) })
	return nil
}

// lapse hides a mark its linger has passed for, unless another showed since.
func (m *lingeringMarks) lapse(shown int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.err != nil || shown != m.shown {
		return
	}
	if err := m.surface.clear(); err != nil {
		m.err = err
	}
}

func (m *lingeringMarks) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return m.surface.close()
}

// ShowMarks has the driver show marks, labelled with name, of where each
// later action lands, just before it acts: the point of a pointer action,
// and the window with the keyboard focus for keys and typing. The driver
// closes the marks when it closes. A mark that fails to show never fails
// the action.
func (d *Driver) ShowMarks(marks Marks, name string) {
	d.marks, d.markName = marks, name
}

// Mark shows, when the driver shows marks, that an action is about to act
// on target, such as an element performing a pattern itself.
func (d *Driver) Mark(target image.Rectangle, action string) {
	if d.marks == nil || target.Empty() {
		return
	}
	_ = d.marks.Show(Mark{Target: target, Label: markLabel(d.markName, action), Color: markColor(d.markName)})
}

// markAt marks a pointer action at a position, or at the pointer when at
// is nil.
func (d *Driver) markAt(at *image.Point, action string) {
	if d.marks == nil {
		return
	}
	if at == nil {
		x, y, err := d.backend.Position()
		if err != nil {
			return
		}
		at = &image.Point{X: x, Y: y}
	}
	d.Mark(pointMark(*at), action)
}

// markFocus marks keys or typing on the window with the keyboard focus.
func (d *Driver) markFocus(action string) {
	if d.marks == nil {
		return
	}
	d.Mark(d.backend.FocusedWindow().Bounds, action)
}

// clickAction names a click by its button and count.
func clickAction(button Button, count int) string {
	action := "click"
	if button != ButtonLeft {
		action = string(button) + " click"
	}
	if count > 1 {
		action = fmt.Sprintf("%s x%d", action, count)
	}
	return action
}

// keysAction names keys pressed together, such as press ctrl+s.
func keysAction(verb string, keys []Key) string {
	names := make([]string, len(keys))
	for i, key := range keys {
		names[i] = string(key)
	}
	return verb + " " + strings.Join(names, "+")
}

func (d *Driver) closeMarks() error {
	if d.marks == nil {
		return nil
	}
	marks := d.marks
	d.marks = nil
	return marks.Close()
}
