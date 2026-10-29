// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"context"
	"errors"
	"image"
	"slices"
)

// ErrNoElements reports an application that exposes no accessible
// elements, such as one run as administrator or one that draws its own
// controls.
var ErrNoElements = errors.New("the application exposes no accessible elements")

// ErrElementsUnsupported reports a system that cannot read elements.
var ErrElementsUnsupported = errors.New("accessible elements are supported on macOS and 64-bit Windows only")

// ErrNoWindow reports that no application window is in front, as for a
// moment between one window closing and the next one opening.
var ErrNoWindow = errors.New("no window is in front")

// ErrNoPattern reports an element that cannot perform a pattern itself, so
// the action falls back to the pointer and keys.
var ErrNoPattern = errors.New("the element offers no such pattern")

// ErrDisabled reports an element that takes no input until the application
// enables it.
var ErrDisabled = errors.New("the element is disabled")

// ErrRefused marks an error an element gave before it acted, such as a
// read-only field asked for a value, so nothing reached the application.
var ErrRefused = errors.New("the element refused before acting")

// refusal is an error an element gives before it acts: it reads as its own
// message and matches ErrRefused.
type refusal string

func (r refusal) Error() string { return string(r) }

func (refusal) Is(target error) bool { return target == ErrRefused }

// Refused reports an error from Apply that came before the element acted:
// the element was not there, was disabled, lacked the pattern, or refused
// it. Any other error may have come after the element acted.
func Refused(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrDisabled) || errors.Is(err, ErrNoPattern) || errors.Is(err, ErrRefused)
}

// Pattern is something an element does by itself when asked through the
// system's automation, with no pointer or key: the UI Automation control
// patterns on Windows, and Accessibility actions and attributes on macOS.
type Pattern string

const (
	// PatternInvoke presses a button, menu item, or link.
	PatternInvoke Pattern = "invoke"
	// PatternToggle flips a checkbox.
	PatternToggle Pattern = "toggle"
	// PatternSelect selects a radio button, list item, tab, or cell.
	PatternSelect Pattern = "select"
	// PatternExpand opens a combo box or another collapsed element.
	PatternExpand Pattern = "expand"
	// PatternSetValue replaces the text of a field.
	PatternSetValue Pattern = "set_value"
)

// Roles an element can have. Each system maps its control types onto them.
const (
	RoleButton    = "button"
	RoleTextField = "text_field"
	RoleText      = "text"
	RoleCheckbox  = "checkbox"
	RoleRadio     = "radio"
	RoleComboBox  = "combo_box"
	RoleListItem  = "list_item"
	RoleMenuItem  = "menu_item"
	RoleTab       = "tab"
	RoleLink      = "link"
	RoleCell      = "cell"
	RoleList      = "list"
	RoleGroup     = "group"
	RoleWindow    = "window"
	RoleOther     = "other"
)

// RoleDialog is the role of a window in front that holds the window behind
// it until it is answered: a macOS sheet or modal alert, or a Windows
// dialog that keeps its owner disabled. It is a window's role only, so no
// selector names it.
const RoleDialog = "dialog"

// Roles lists every role a selector may name.
var Roles = []string{
	RoleButton, RoleTextField, RoleText, RoleCheckbox, RoleRadio, RoleComboBox,
	RoleListItem, RoleMenuItem, RoleTab, RoleLink, RoleCell, RoleList, RoleGroup, RoleWindow, RoleOther,
}

// maxOutline bounds an outline that sets no limit, so a window with an
// enormous tree, such as a spreadsheet grid, does not exhaust memory.
const maxOutline = 2000

// PathStep is one level of an element's path from its window down.
type PathStep struct {
	Role string `json:"role"`
	Name string `json:"name"`
	// Index is the element's position among its parent's children of the
	// same role, from 0.
	Index int `json:"index"`
}

// Element is an accessible element of a window on the desktop.
type Element struct {
	Role string
	Name string
	// ID is the identifier the application gives the element, when it
	// sets one: the UI Automation AutomationId on Windows, and on macOS the
	// accessibility identifier, or a web page's element id.
	ID string
	// Value is what the element holds, such as the text of a field. It is
	// blank for a password field.
	Value string
	// Placeholder is the hint a field shows while it holds nothing, such as
	// a grey "0". It is read on macOS; Windows UI Automation has no such
	// property.
	Placeholder string
	// Label is the name of the element that labels this one, when the
	// application links them.
	Label string
	// App is the process image name without its extension. It is set on
	// window elements.
	App string
	// Bounds is where the element is, in display pixels.
	Bounds image.Rectangle
	// Window is the title of the window the element belongs to.
	Window string
	// Disabled is set for an element the system reports takes no input
	// until the application enables it, such as a grayed-out button.
	Disabled bool
	// Path leads from the window down to the element. A window's path is
	// empty.
	Path []PathStep
}

// Elements reads the accessible elements of the desktop. Positions are
// display pixels, as the Driver uses them.
type Elements interface {
	// At returns the element under a point.
	At(x, y int) (Element, error)
	// Focused returns the element with the keyboard focus.
	Focused() (Element, error)
	// Find returns the elements a selector matches in the front window, in
	// reading order. It returns no error and no elements when nothing
	// matches.
	Find(Selector) ([]Element, error)
	// FrontWindow returns the window in front.
	FrontWindow() (Element, error)
	// Outline lists the visible elements of a window in tree order, at
	// most limit of them, or a bounded number when limit is 0.
	Outline(window Element, limit int) ([]Element, error)
	// Focus gives an element the keyboard focus.
	Focus(Element) error
	// Apply has an element perform a pattern itself, finding it again as
	// Focus does; value is the text PatternSetValue sets. ErrNoPattern says
	// the element cannot, so the action falls back to the pointer and keys;
	// ErrDisabled says it takes no input yet; a read-only field or an
	// element with nothing to expand is an error saying so.
	Apply(target Element, pattern Pattern, value string) error
	// Rows reads a grid, table, or list's rows as text, one []string per
	// row, each cell the text under the column that columns names by its
	// header, up to max rows. A container with no grid, a column with no
	// header, or an unreadable cell is an error. It may take longer than
	// one read, so it is bounded by ctx.
	Rows(ctx context.Context, container Selector, columns []string, max int) ([][]string, error)
	Close() error
}

// treeNode is an element with its children, as a system exposes a
// window's tree. element fills everything but Window and Path; placed
// tells the node the path the walk gave it.
type treeNode interface {
	element() Element
	visible() bool
	children() []treeNode
	placed(path []PathStep)
}

// outlineTree lists root's descendants in preorder, with Window and Path
// filled, up to limit elements. Root itself is left out. Elements that are
// not visible are left out too, though their children are walked and they
// still count toward their siblings' indexes, so a path names the same
// element whether or not it is on screen.
func outlineTree(root treeNode, window string, limit int) []Element {
	if limit <= 0 {
		limit = maxOutline
	}
	var out []Element
	var walk func(node treeNode, path []PathStep)
	walk = func(node treeNode, path []PathStep) {
		indexes := map[string]int{}
		for _, child := range node.children() {
			if len(out) >= limit {
				return
			}
			e := child.element()
			step := PathStep{Role: e.Role, Name: e.Name, Index: indexes[e.Role]}
			indexes[e.Role]++
			e.Path = append(slices.Clone(path), step)
			e.Window = window
			child.placed(e.Path)
			if child.visible() {
				out = append(out, e)
			}
			walk(child, e.Path)
		}
	}
	walk(root, nil)
	return out
}
