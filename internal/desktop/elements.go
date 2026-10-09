// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package desktop

import (
	"errors"
	"image"
	"slices"
)

// ErrNoElements reports an application that exposes no accessible
// elements, such as one run as administrator or one that draws its own
// controls.
var ErrNoElements = errors.New("the application exposes no accessible elements")

// ErrElementsUnsupported reports a system that cannot read elements.
var ErrElementsUnsupported = errors.New("accessible elements are supported on 64-bit Windows only")

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
	RoleGroup     = "group"
	RoleWindow    = "window"
	RoleOther     = "other"
)

// Roles lists every role a selector may name.
var Roles = []string{
	RoleButton, RoleTextField, RoleText, RoleCheckbox, RoleRadio, RoleComboBox,
	RoleListItem, RoleMenuItem, RoleTab, RoleLink, RoleCell, RoleGroup, RoleWindow, RoleOther,
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
	// sets one: the UI Automation AutomationId on Windows.
	ID string
	// Value is what the element holds, such as the text of a field. It is
	// blank for a password field.
	Value string
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
