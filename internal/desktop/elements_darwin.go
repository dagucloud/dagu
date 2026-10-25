// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package desktop

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strconv"
	"sync"
	"time"
)

const (
	// readBudget bounds reading one window's elements, one request to its
	// application per element.
	readBudget = 5 * time.Second
	// findLimit is how many elements a search reads from a window.
	findLimit = 5000
	// chromiumWait is how long to let a Chromium or Electron window build
	// its accessibility tree, which it does once a client turns it on.
	chromiumWait = 300 * time.Millisecond
	// maxDepth bounds the walk from an element up to its window.
	maxDepth = 64
	// menuCloseWait bounds how long a chosen option's menu takes to close
	// and its button to show the option, looked at every menuClosePoll.
	menuCloseWait = time.Second
	menuClosePoll = 20 * time.Millisecond
	// skippedLimit is how many elements are read looking for an element
	// below the parent it names, when that parent does not hold it.
	skippedLimit = 200
)

var (
	errReadOnly error = refusal("the field is read-only")
	errLeaf     error = refusal("the element has nothing to expand")
	errClosed         = errors.New("elements closed")
)

// Attributes read for every element, in one request each. The order is
// the index of each value in the answer.
var elementAttributes = []string{
	"AXRole", "AXSubrole", "AXTitle", "AXDescription", "AXValue", "AXIdentifier", "AXDOMIdentifier",
	"AXPosition", "AXSize", "AXEnabled", "AXChildren", "AXTitleUIElement",
}

const (
	attrRole = iota
	attrSubrole
	attrTitle
	attrDescription
	attrValue
	attrIdentifier
	attrDOMIdentifier
	attrPosition
	attrSize
	attrEnabled
	attrChildren
	attrTitleElement
)

// axRoles maps Accessibility roles onto roles.
var axRoles = map[string]string{
	"AXButton":      RoleButton,
	"AXMenuButton":  RoleButton,
	"AXTextField":   RoleTextField,
	"AXTextArea":    RoleTextField,
	"AXStaticText":  RoleText,
	"AXCheckBox":    RoleCheckbox,
	"AXRadioButton": RoleRadio,
	"AXPopUpButton": RoleComboBox,
	"AXComboBox":    RoleComboBox,
	"AXRow":         RoleListItem,
	"AXMenuItem":    RoleMenuItem,
	"AXMenuBarItem": RoleMenuItem,
	"AXLink":        RoleLink,
	"AXCell":        RoleCell,
	"AXTable":       RoleList,
	"AXOutline":     RoleList,
	"AXList":        RoleList,
	"AXBrowser":     RoleList,
	"AXGroup":       RoleGroup,
	"AXRadioGroup":  RoleGroup,
	"AXTabGroup":    RoleGroup,
	"AXWindow":      RoleWindow,
}

// roleOf maps an element's Accessibility role onto a role. A radio button
// of a tab group is a tab.
func roleOf(axRole, subrole, parentRole string) string {
	if axRole == "AXRadioButton" && (subrole == "AXTabButton" || parentRole == "AXTabGroup") {
		return RoleTab
	}
	if role, ok := axRoles[axRole]; ok {
		return role
	}
	return RoleOther
}

// OpenElements opens a reader of the desktop's accessible elements through
// the Accessibility permission the desktop already needs.
func OpenElements() (Elements, error) {
	if err := load(); err != nil {
		return nil, err
	}
	if !axIsProcessTrusted() {
		return nil, errors.New("the Accessibility permission is missing; grant it in System Settings > Privacy & Security > Accessibility")
	}
	d, err := mainDisplay()
	if err != nil {
		return nil, err
	}
	system := axUIElementCreateSystemWide()
	if system == 0 {
		return nil, errors.New("open the system-wide accessibility element")
	}
	attributes := cfStringArray(elementAttributes)
	if attributes == 0 {
		cfRelease(system)
		return nil, errors.New("create the attribute list")
	}
	return &axElements{display: d, system: system, attributes: attributes, web: map[int32]bool{}}, nil
}

// axElements reads elements through the macOS Accessibility API. Requests
// go to each application in turn, so the reader serializes them.
type axElements struct {
	mu         sync.Mutex
	display    display
	system     uintptr
	attributes uintptr
	// web records, for each application the reader met, whether it took
	// the attribute that turns on a Chromium or Electron tree.
	web    map[int32]bool
	closed bool
}

func (e *axElements) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	cfRelease(e.attributes)
	cfRelease(e.system)
	return nil
}

// with runs fn holding the reader.
func with[T any](e *axElements, fn func() (T, error)) (T, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		var zero T
		return zero, errClosed
	}
	return fn()
}

// axNode is one element's attributes as one request read them.
type axNode struct {
	role, subrole string
	title         string
	description   string
	value         string
	id            string
	frame         cgRect
	framed        bool
	enabled       bool
	hasEnabled    bool
	// children are retained references the reader releases.
	children []uintptr
	label    string
}

// read reads an element's attributes in one request. The children it
// returns are retained; pool collects them for release.
func (e *axElements) read(ref uintptr, pool *[]uintptr) (axNode, error) {
	var values uintptr
	if code := axUIElementCopyMultipleAttributeValues(ref, e.attributes, 0, &values); code != axErrorSuccess {
		return axNode{}, axError("read the element", code)
	}
	defer cfRelease(values)
	list := cfArray(values)
	if len(list) < len(elementAttributes) {
		return axNode{}, errors.New("read the element: an attribute is missing from the answer")
	}
	text := func(i int) string {
		if axMissing(list[i]) {
			return ""
		}
		return goString(list[i])
	}
	n := axNode{
		role: text(attrRole), subrole: text(attrSubrole), title: text(attrTitle), description: text(attrDescription),
		id: text(attrIdentifier),
	}
	if n.id == "" {
		n.id = text(attrDOMIdentifier)
	}
	if value := list[attrValue]; !axMissing(value) {
		n.value = valueText(value)
	}
	if !axMissing(list[attrPosition]) && !axMissing(list[attrSize]) {
		var position cgPoint
		var size cgSize
		if axValueGetPoint(list[attrPosition], axValueCGPointType, &position) && axValueGetSize(list[attrSize], axValueCGSizeType, &size) {
			n.frame, n.framed = cgRect{Origin: position, Size: size}, true
		}
	}
	if enabled, ok := cfBool(list[attrEnabled]); ok && !axMissing(list[attrEnabled]) {
		n.enabled, n.hasEnabled = enabled, true
	}
	if children := list[attrChildren]; !axMissing(children) {
		for _, child := range cfArray(children) {
			if child != 0 {
				cfRetain(child)
				*pool = append(*pool, child)
				n.children = append(n.children, child)
			}
		}
	}
	if label := list[attrTitleElement]; !axMissing(label) {
		n.label = axString(label, "AXValue")
		if n.label == "" {
			n.label = axString(label, "AXTitle")
		}
	}
	return n, nil
}

// valueText writes an element's value as text: a string as it is, a number,
// such as a checkbox's state, in digits, and a boolean as 1 or 0, as a
// checkbox's state reads.
func valueText(value uintptr) string {
	switch cfGetTypeID(value) {
	case cfStringGetTypeID():
		return goString(value)
	case cfNumberGetTypeID():
		if n, ok := cfFloat(value); ok {
			return strconv.FormatFloat(n, 'f', -1, 64)
		}
	case cfBooleanGetTypeID():
		if cfBooleanGetValue(value) {
			return "1"
		}
		return "0"
	}
	return ""
}

// element describes a node as an Element, without its window and path.
func (e *axElements) element(n axNode, parentRole string) Element {
	found := Element{Role: roleOf(n.role, n.subrole, parentRole), Name: n.title, ID: n.id, Label: n.label, Disabled: n.hasEnabled && !n.enabled}
	if found.Name == "" {
		found.Name = n.description
	}
	switch {
	case n.subrole == "AXSecureTextField":
		// A password field's value is never read.
	case found.Role == RoleCheckbox || found.Role == RoleRadio || found.Role == RoleTab:
		// A checkbox or radio button holds its state, not text, so its value
		// is its state as a boolean a person would read.
		switch n.value {
		case "1":
			found.Value = "true"
		case "0":
			found.Value = "false"
		}
	case (found.Role == RoleText || found.Role == RoleMenuItem) && found.Name == "":
		// Static text names itself with what it shows, and so does an
		// option of a web page's list box, whose text Chromium gives as
		// its value.
		found.Name = n.value
	default:
		found.Value = n.value
	}
	// A group, such as a titled box, is named by the element that titles it,
	// as a group box is by its text on Windows.
	if found.Role == RoleGroup && found.Name == "" {
		found.Name, found.Label = n.label, ""
	}
	if n.framed {
		found.Bounds = e.display.pixelRect(n.frame)
	}
	return found
}

// axWindow is a window in front, with the reference the reader releases.
type axWindow struct {
	ref    uintptr
	pid    int32
	window Element
	// web marks a Chromium or Electron window, which builds its tree a
	// moment after a client first asks.
	web bool
}

// frontApplication returns the application in front and its process. The
// caller releases the application.
func frontApplication() (uintptr, int32, error) {
	pid := focusedApplication()
	if pid == 0 {
		return 0, 0, ErrNoWindow
	}
	app := axUIElementCreateApplication(pid)
	if app == 0 {
		return 0, 0, ErrNoWindow
	}
	return app, pid, nil
}

// frontWindow returns the focused window of the application in front.
func (e *axElements) frontWindow() (axWindow, error) {
	app, pid, err := frontApplication()
	if err != nil {
		return axWindow{}, err
	}
	defer cfRelease(app)
	web := e.openWeb(app, pid)
	window := axCopy(app, "AXFocusedWindow")
	if window == 0 {
		window = axCopy(app, "AXMainWindow")
	}
	if window == 0 {
		if windows := axCopy(app, "AXWindows"); windows != 0 {
			if list := cfArray(windows); len(list) > 0 {
				window = cfRetain(list[0])
			}
			cfRelease(windows)
		}
	}
	if window == 0 {
		return axWindow{}, ErrNoWindow
	}
	top, err := e.asWindow(window, pid)
	top.web = web
	return top, err
}

// asWindow describes a window element; it takes ownership of ref.
func (e *axElements) asWindow(ref uintptr, pid int32) (axWindow, error) {
	var pool []uintptr
	n, err := e.read(ref, &pool)
	release(pool)
	if err != nil {
		cfRelease(ref)
		return axWindow{}, err
	}
	window := e.element(n, "")
	window.Role, window.Window, window.App = RoleWindow, window.Name, processApp(pid)
	// A sheet holds its window until it is answered, as a modal alert holds
	// the whole application; a panel that is not modal holds nothing.
	if modal, _ := axBool(ref, "AXModal"); modal || n.role == "AXSheet" {
		window.Role = RoleDialog
	}
	return axWindow{ref: ref, pid: pid, window: window}, nil
}

// openWeb turns on the accessibility tree of a Chromium or Electron
// application, which builds it only when a client asks, and reports whether
// the application is one: other applications refuse the attribute. An
// application that does not answer, as one starting up may not, is asked
// again next time rather than taken for one that refused.
func (e *axElements) openWeb(app uintptr, pid int32) bool {
	if web, met := e.web[pid]; met {
		return web
	}
	switch axUIElementSetAttributeValue(app, cfName("AXManualAccessibility"), cfBooleanTrue) {
	case axErrorSuccess:
		e.web[pid] = true
	case axErrorCannotComplete:
		return false
	default:
		e.web[pid] = false
	}
	return e.web[pid]
}

func release(refs []uintptr) {
	for _, ref := range refs {
		cfRelease(ref)
	}
}

func (e *axElements) FrontWindow() (Element, error) {
	return with(e, func() (Element, error) {
		top, err := e.frontWindow()
		if err != nil {
			return Element{}, err
		}
		cfRelease(top.ref)
		return top.window, nil
	})
}

func (e *axElements) Find(sel Selector) ([]Element, error) {
	return with(e, func() ([]Element, error) {
		top, err := e.frontWindow()
		if err != nil {
			return nil, err
		}
		defer cfRelease(top.ref)
		tree, err := e.subtree(top, findLimit)
		if err != nil {
			return nil, err
		}
		defer tree.release()
		return Match(top.window, tree.elements, sel)
	})
}

func (e *axElements) Outline(window Element, limit int) ([]Element, error) {
	return with(e, func() ([]Element, error) {
		top, err := e.resolveWindow(window)
		if err != nil {
			return nil, err
		}
		defer cfRelease(top.ref)
		tree, err := e.subtree(top, limit)
		if err != nil {
			return nil, err
		}
		defer tree.release()
		return tree.elements, nil
	})
}

// resolveWindow finds the window an element describes: the one in front,
// or the window under the element's centre.
func (e *axElements) resolveWindow(window Element) (axWindow, error) {
	top, err := e.frontWindow()
	if err != nil {
		return axWindow{}, err
	}
	if window.Name == "" || window.Name == top.window.Name || window.Bounds.Empty() {
		return top, nil
	}
	cfRelease(top.ref)
	centre := window.Bounds.Min.Add(window.Bounds.Size().Div(2))
	ref, err := e.elementAt(centre)
	if err != nil {
		return axWindow{}, err
	}
	defer cfRelease(ref)
	owner := axCopy(ref, "AXWindow")
	if owner == 0 {
		return axWindow{}, fmt.Errorf("%w: no window %q is there", ErrNotFound, window.Name)
	}
	var pid int32
	_ = axUIElementGetPid(owner, &pid)
	found, err := e.asWindow(owner, pid)
	if err != nil {
		return axWindow{}, err
	}
	if found.window.Name != window.Name {
		cfRelease(found.ref)
		return axWindow{}, fmt.Errorf("%w: %q is there, not %q", ErrNotFound, found.window.Name, window.Name)
	}
	return found, nil
}

// elementAt returns the element under a display position. The caller
// releases it.
func (e *axElements) elementAt(at image.Point) (uintptr, error) {
	point := e.display.points(at)
	var ref uintptr
	if code := axUIElementCopyElementAtPosition(e.system, float32(point.X), float32(point.Y), &ref); code != axErrorSuccess || ref == 0 {
		return 0, axError("element at point", code)
	}
	return ref, nil
}

func (e *axElements) At(x, y int) (Element, error) {
	return with(e, func() (Element, error) {
		ref, err := e.elementAt(image.Pt(x, y))
		if err != nil {
			return Element{}, err
		}
		defer cfRelease(ref)
		found, err := e.place(ref)
		if err == nil {
			return found, nil
		}
		// Chromium draws a list box's open menu apart from any window; the
		// option it shows is read from the list box in the window instead.
		if option := e.openOption(ref); option != 0 {
			defer cfRelease(option)
			return e.place(option)
		}
		return Element{}, err
	})
}

// openOption returns the option of the open pop-up button that a menu item
// drawn outside any window shows, matched by its name, or 0 when there is
// none. The caller releases it.
func (e *axElements) openOption(item uintptr) uintptr {
	var pool []uintptr
	defer func() { release(pool) }()
	shown, err := e.read(item, &pool)
	var pid int32
	if err != nil || shown.role != "AXMenuItem" || axUIElementGetPid(item, &pid) != axErrorSuccess {
		return 0
	}
	name := NormalizeText(e.element(shown, "AXMenu").Name)
	if name == "" {
		return 0
	}
	app := axUIElementCreateApplication(pid)
	if app == 0 {
		return 0
	}
	defer cfRelease(app)
	focused := axCopy(app, "AXFocusedUIElement")
	if focused == 0 {
		return 0
	}
	defer cfRelease(focused)
	menu, popup := popupOf(focused)
	if menu == 0 {
		return 0
	}
	defer cfRelease(menu)
	defer cfRelease(popup)
	options := axCopy(menu, "AXChildren")
	if options == 0 {
		return 0
	}
	defer cfRelease(options)
	list := cfArray(options)
	var named []int
	for i, option := range list {
		if n, err := e.read(option, &pool); err == nil && NormalizeText(e.element(n, "AXMenu").Name) == name {
			named = append(named, i)
		}
	}
	switch {
	case len(named) == 1:
		return cfRetain(list[named[0]])
	case len(named) > 1:
		// Options that share a name are told apart by their place, which
		// the drawn menu keeps; one that cannot be told apart is none.
		if at := itemIndex(item); slices.Contains(named, at) {
			return cfRetain(list[at])
		}
	}
	return 0
}

// itemIndex is a menu item's place among its menu's items, or -1.
func itemIndex(item uintptr) int {
	menu := axCopy(item, "AXParent")
	if menu == 0 {
		return -1
	}
	defer cfRelease(menu)
	items := axCopy(menu, "AXChildren")
	if items == 0 {
		return -1
	}
	defer cfRelease(items)
	for i, sibling := range cfArray(items) {
		if cfEqual(sibling, item) {
			return i
		}
	}
	return -1
}

func (e *axElements) Focused() (Element, error) {
	return with(e, func() (Element, error) {
		app, _, err := frontApplication()
		if err != nil {
			return Element{}, err
		}
		ref := axCopy(app, "AXFocusedUIElement")
		cfRelease(app)
		if ref == 0 {
			return Element{}, errors.New("no element has the keyboard focus")
		}
		defer cfRelease(ref)
		return e.place(ref)
	})
}

// place describes an element with its window and its path down from it.
func (e *axElements) place(ref uintptr) (Element, error) {
	var pool []uintptr
	defer func() { release(pool) }()
	n, err := e.read(ref, &pool)
	if err != nil {
		return Element{}, err
	}
	if n.role == "AXWindow" {
		var pid int32
		_ = axUIElementGetPid(ref, &pid)
		top, err := e.asWindow(cfRetain(ref), pid)
		if err != nil {
			return Element{}, err
		}
		cfRelease(top.ref)
		return top.window, nil
	}
	var steps []PathStep
	current, currentNode := ref, n
	var parentRole string
	var title string
	for depth := 0; ; depth++ {
		if depth >= maxDepth {
			return Element{}, errors.New("the element is nested too deep")
		}
		parent := axCopy(current, "AXParent")
		if parent == 0 {
			return Element{}, errors.New("the element has no window")
		}
		pool = append(pool, parent)
		parentNode, err := e.read(parent, &pool)
		if err != nil {
			return Element{}, err
		}
		below, holder, err := e.stepsBelow(parentNode, current, currentNode, &pool)
		if err != nil {
			return Element{}, err
		}
		if depth == 0 {
			parentRole = holder
		}
		steps = append(steps, below...)
		if parentNode.role == "AXWindow" {
			title = e.element(parentNode, "").Name
			break
		}
		if parentNode.role == "AXApplication" {
			return Element{}, errors.New("the element is not in a window")
		}
		current, currentNode = parent, parentNode
	}
	slices.Reverse(steps)
	found := e.element(n, parentRole)
	found.Path, found.Window = steps, title
	return found, nil
}

// stepsBelow describes a child's place below its parent: the steps from the
// parent down to it, the child's own first, and the role of the element
// that holds it. Electron names the window as its web area's parent, though
// the window holds the web area through groups of its own; a child that is
// not among its parent's children is looked for further down, reading up to
// skippedLimit elements.
func (e *axElements) stepsBelow(parent axNode, child uintptr, childNode axNode, pool *[]uintptr) ([]PathStep, string, error) {
	step, notAmong := e.stepOf(parent, child, childNode, pool)
	if notAmong == nil {
		return []PathStep{step}, parent.role, nil
	}
	// level is an element read on the way down, with the index of the
	// level holding it; the parent holds the first levels.
	type level struct {
		ref  uintptr
		node axNode
		up   int
	}
	levels := []level{{node: parent, up: -1}}
	for next := 0; next < len(levels); next++ {
		holder := levels[next]
		for _, ref := range holder.node.children {
			if !cfEqual(ref, child) {
				if len(levels) <= skippedLimit {
					if node, err := e.read(ref, pool); err == nil {
						levels = append(levels, level{ref: ref, node: node, up: next})
					}
				}
				continue
			}
			var steps []PathStep
			below, belowNode := child, childNode
			for at := next; at >= 0; at = levels[at].up {
				step, err := e.stepOf(levels[at].node, below, belowNode, pool)
				if err != nil {
					return nil, "", err
				}
				steps = append(steps, step)
				below, belowNode = levels[at].ref, levels[at].node
			}
			return steps, holder.node.role, nil
		}
	}
	return nil, "", notAmong
}

// stepOf describes a child's place among its parent's children: its role,
// its name, and how many earlier siblings share its role.
func (e *axElements) stepOf(parent axNode, child uintptr, childNode axNode, pool *[]uintptr) (PathStep, error) {
	described := e.element(childNode, parent.role)
	step := PathStep{Role: described.Role, Name: described.Name}
	for _, sibling := range parent.children {
		if cfEqual(sibling, child) {
			return step, nil
		}
		siblingNode, err := e.read(sibling, pool)
		if err != nil {
			continue
		}
		if roleOf(siblingNode.role, siblingNode.subrole, parent.role) == step.Role {
			step.Index++
		}
	}
	return step, errors.New("the element is not among its parent's children")
}

// axTree is one window's elements as one walk read them. Every reference
// it took is released together.
type axTree struct {
	reader   *axElements
	pool     []uintptr
	nodes    []*axTreeNode
	elements []Element
	window   image.Rectangle
	deadline time.Time
	err      error
}

func (t *axTree) release() {
	release(t.pool)
	t.pool = nil
}

// subtree reads a window's elements. A Chromium window builds its tree once
// a client turns it on, so a sparse answer from one is read again after a
// moment.
func (e *axElements) subtree(top axWindow, limit int) (*axTree, error) {
	tree, err := e.readSubtree(top, limit)
	if err != nil {
		return nil, err
	}
	if len(tree.nodes) <= 2 && top.web {
		tree.release()
		time.Sleep(chromiumWait)
		if tree, err = e.readSubtree(top, limit); err != nil {
			return nil, err
		}
	}
	if len(tree.nodes) == 0 {
		tree.release()
		return nil, fmt.Errorf("%w: %q may draw its own controls", ErrNoElements, top.window.Name)
	}
	return tree, nil
}

func (e *axElements) readSubtree(top axWindow, limit int) (*axTree, error) {
	tree := &axTree{reader: e, window: top.window.Bounds, deadline: time.Now().Add(readBudget)}
	root := &axTreeNode{tree: tree, ref: top.ref, role: "AXWindow"}
	tree.elements = outlineTree(root, top.window.Name, limit)
	if tree.err != nil {
		tree.release()
		return nil, tree.err
	}
	return tree, nil
}

// axTreeNode adapts an element to the tree walk.
type axTreeNode struct {
	tree       *axTree
	ref        uintptr
	parentRole string
	role       string
	path       []PathStep
	loaded     bool
	node       axNode
	value      Element
	visibly    bool
}

func (n *axTreeNode) load() {
	if n.loaded {
		return
	}
	n.loaded = true
	if time.Now().After(n.tree.deadline) {
		if n.tree.err == nil {
			n.tree.err = fmt.Errorf("reading the window's elements took longer than %s", readBudget)
		}
		return
	}
	node, err := n.tree.reader.read(n.ref, &n.tree.pool)
	if err != nil {
		// An element that went away while the walk read its siblings is left
		// out, as it is no longer there.
		return
	}
	n.node, n.role = node, node.role
	n.value = n.tree.reader.element(node, n.parentRole)
	n.visibly = !n.value.Bounds.Empty() && (n.tree.window.Empty() || n.value.Bounds.Overlaps(n.tree.window))
}

func (n *axTreeNode) element() Element {
	n.load()
	return n.value
}

func (n *axTreeNode) visible() bool {
	n.load()
	return n.visibly
}

func (n *axTreeNode) placed(path []PathStep) {
	n.path = path
}

func (n *axTreeNode) children() []treeNode {
	n.load()
	out := make([]treeNode, 0, len(n.node.children))
	for _, ref := range n.node.children {
		child := &axTreeNode{tree: n.tree, ref: ref, parentRole: n.role}
		n.tree.nodes = append(n.tree.nodes, child)
		out = append(out, child)
	}
	return out
}

// Focus finds the element again and gives it the keyboard focus, bringing
// its application and window to the front.
func (e *axElements) Focus(target Element) error {
	_, err := with(e, func() (struct{}, error) {
		ref, err := e.locate(target)
		if err != nil {
			return struct{}{}, err
		}
		defer cfRelease(ref)
		e.bringForward(ref)
		if !axSettable(ref, "AXFocused") {
			return struct{}{}, errors.New("the element cannot take the keyboard focus")
		}
		return struct{}{}, axSet(ref, "AXFocused", cfBooleanTrue)
	})
	return err
}

// bringForward brings an element's application to the front and its
// window above the application's others.
func (e *axElements) bringForward(ref uintptr) {
	var pid int32
	if axUIElementGetPid(ref, &pid) == axErrorSuccess {
		if app := axUIElementCreateApplication(pid); app != 0 {
			if front, _ := axBool(app, "AXFrontmost"); !front {
				_ = axSet(app, "AXFrontmost", cfBooleanTrue)
			}
			cfRelease(app)
		}
	}
	if window := axCopy(ref, "AXWindow"); window != 0 {
		_ = axPerform(window, "AXRaise")
		cfRelease(window)
	}
}

// Apply finds the element again and has it carry out a pattern through its
// Accessibility actions and attributes.
func (e *axElements) Apply(target Element, pattern Pattern, value string) error {
	_, err := with(e, func() (struct{}, error) {
		ref, err := e.locate(target)
		if err != nil {
			return struct{}{}, err
		}
		defer cfRelease(ref)
		if enabled, ok := axBool(ref, "AXEnabled"); ok && !enabled {
			return struct{}{}, ErrDisabled
		}
		return struct{}{}, perform(ref, pattern, value)
	})
	return err
}

// perform has a located, enabled element carry out a pattern.
func perform(ref uintptr, pattern Pattern, value string) error {
	actions := axActions(ref)
	switch pattern {
	case PatternInvoke, PatternToggle:
		if menu, popup := popupOf(ref); menu != 0 {
			defer cfRelease(menu)
			defer cfRelease(popup)
			if chosen, err := chooseOption(ref, menu, popup); chosen {
				return err
			}
		}
		if !slices.Contains(actions, "AXPress") {
			return ErrNoPattern
		}
		return act(ref, "AXPress")
	case PatternSelect:
		if axSettable(ref, "AXSelected") {
			return axSet(ref, "AXSelected", cfBooleanTrue)
		}
		if !slices.Contains(actions, "AXPress") {
			return ErrNoPattern
		}
		return act(ref, "AXPress")
	case PatternExpand:
		// A pop-up button opens its menu when pressed. On Chromium's list
		// box AXShowMenu opens the page's context menu instead, and a
		// recent Chromium offers no press, so such a box is opened on the
		// screen.
		if axString(ref, "AXRole") == "AXPopUpButton" {
			if !slices.Contains(actions, "AXPress") {
				return ErrNoPattern
			}
			return act(ref, "AXPress")
		}
		switch {
		case slices.Contains(actions, "AXShowMenu"):
			return act(ref, "AXShowMenu")
		case axSettable(ref, "AXExpanded"):
			return axSet(ref, "AXExpanded", cfBooleanTrue)
		case slices.Contains(actions, "AXPress"):
			return act(ref, "AXPress")
		}
		return errLeaf
	case PatternSetValue:
		if !axSettable(ref, "AXValue") {
			current := axCopy(ref, "AXValue")
			if current == 0 {
				return ErrNoPattern
			}
			cfRelease(current)
			return errReadOnly
		}
		text := cfString(value)
		if text == 0 {
			return errors.New("create the value")
		}
		defer cfRelease(text)
		return axSet(ref, "AXValue", text)
	default:
		return fmt.Errorf("unknown pattern %q", pattern)
	}
}

// chooseOption chooses an option of a Chromium list box's open menu with
// the keyboard, as a person would: up to the first item, down to the
// option, then Return. Chromium takes no press on an option through
// Accessibility, or keeps the menu open after one. It reports false, doing
// nothing, for a menu that does not have the keyboard focus, such as
// AppKit's, whose items close it when pressed. The button showing the
// option afterwards confirms the choice.
func chooseOption(option, menu, popup uintptr) (bool, error) {
	var pid int32
	if axUIElementGetPid(option, &pid) != axErrorSuccess || !menuHasFocus(pid, menu) {
		return false, nil
	}
	items := axCopy(menu, "AXChildren")
	if items == 0 {
		return true, ErrNotFound
	}
	// The arrow keys pass over disabled items, so only enabled ones count.
	position, count := -1, 0
	for _, item := range cfArray(items) {
		if enabled, ok := axBool(item, "AXEnabled"); ok && !enabled {
			continue
		}
		if cfEqual(item, option) {
			position = count
		}
		count++
	}
	cfRelease(items)
	if position < 0 {
		return true, ErrNotFound
	}
	want := NormalizeText(itemText(option))
	keys := slices.Concat(
		slices.Repeat([]uint16{namedKeyCodes[KeyUp]}, count),
		slices.Repeat([]uint16{namedKeyCodes[KeyDown]}, position),
		[]uint16{namedKeyCodes[KeyEnter]},
	)
	for _, code := range keys {
		for _, down := range []bool{true, false} {
			event := cgEventCreateKeyboardEvent(0, code, down)
			if event == 0 {
				return true, errors.New("create a key event")
			}
			cgEventPostToPid(pid, event)
			cfRelease(event)
		}
	}
	// A page that renders the box again on the change replaces it, and the
	// one chosen on is gone; only a box still there can show another option.
	deadline := time.Now().Add(menuCloseWait)
	for {
		open := menuHasFocus(pid, menu)
		shown := axValueText(popup)
		if !open && (axGone(popup) || NormalizeText(shown) == want) {
			return true, nil
		}
		if !time.Now().Before(deadline) {
			if open {
				return true, errors.New("the menu stayed open after its option was chosen")
			}
			return true, fmt.Errorf("the list box shows %q, not the option chosen, %q", shown, want)
		}
		time.Sleep(menuClosePoll)
	}
}

// axGone reports an element its application has destroyed, as a page does
// with a list box it builds again. A read that fails for another reason
// does not count.
func axGone(element uintptr) bool {
	var value uintptr
	code := axUIElementCopyAttributeValue(element, cfName("AXRole"), &value)
	if code == axErrorSuccess {
		cfRelease(value)
	}
	return code == axErrorInvalidUIElement
}

// itemText is what a menu item shows: its title, or, for an option of
// Chromium's list box, its value.
func itemText(item uintptr) string {
	if title := axString(item, "AXTitle"); title != "" {
		return title
	}
	return axValueText(item)
}

// axValueText reads an element's value as text, such as the option a
// pop-up button shows.
func axValueText(ref uintptr) string {
	value := axCopy(ref, "AXValue")
	if value == 0 {
		return ""
	}
	defer cfRelease(value)
	return valueText(value)
}

// popupOf returns the menu an option of a pop-up button sits in and the
// button, or zeros for an element that is not one. The caller releases
// both.
func popupOf(option uintptr) (menu, popup uintptr) {
	if axString(option, "AXRole") != "AXMenuItem" {
		return 0, 0
	}
	menu = axCopy(option, "AXParent")
	if menu == 0 {
		return 0, 0
	}
	popup = axCopy(menu, "AXParent")
	if axString(menu, "AXRole") == "AXMenu" && popup != 0 && axString(popup, "AXRole") == "AXPopUpButton" {
		return menu, popup
	}
	if popup != 0 {
		cfRelease(popup)
	}
	cfRelease(menu)
	return 0, 0
}

// menuHasFocus reports whether an application's keyboard focus is on an
// item of a menu, which it is while the menu is open.
func menuHasFocus(pid int32, menu uintptr) bool {
	app := axUIElementCreateApplication(pid)
	if app == 0 {
		return false
	}
	defer cfRelease(app)
	focused := axCopy(app, "AXFocusedUIElement")
	if focused == 0 {
		return false
	}
	defer cfRelease(focused)
	parent := axCopy(focused, "AXParent")
	if parent == 0 {
		return false
	}
	defer cfRelease(parent)
	return cfEqual(parent, menu)
}

// act performs an action of an element. An application that runs the
// action's handler before it answers, such as one that opens a dialog there,
// holds the request past the messaging timeout; the action may still take
// effect, as on Windows when an invoke outlives its deadline.
func act(ref uintptr, action string) error {
	switch code := axUIElementPerformAction(ref, cfName(action)); code {
	case axErrorSuccess:
		return nil
	case axErrorActionUnsupported:
		return ErrNoPattern
	case axErrorInvalidUIElement:
		return fmt.Errorf("%w: the element went away", ErrNotFound)
	case axErrorCannotComplete:
		return fmt.Errorf("%s: no answer within %ds; the application may still be handling it: %w", action, axMessagingTimeout, context.DeadlineExceeded)
	default:
		return axError(action, code)
	}
}

// locate finds an element again, under its centre first and then by its
// path in the front window. The caller releases what it returns.
func (e *axElements) locate(target Element) (uintptr, error) {
	if !target.Bounds.Empty() {
		centre := target.Bounds.Min.Add(target.Bounds.Size().Div(2))
		if ref, err := e.elementAt(centre); err == nil {
			var pool []uintptr
			n, err := e.read(ref, &pool)
			release(pool)
			if err == nil {
				found := e.element(n, e.parentRole(ref))
				if found.Role == target.Role && found.Name == target.Name && found.ID == target.ID {
					return ref, nil
				}
			}
			cfRelease(ref)
		}
	}
	if len(target.Path) == 0 {
		return 0, fmt.Errorf("%w: the element is not where it was", ErrNotFound)
	}
	top, err := e.frontWindow()
	if err != nil {
		return 0, err
	}
	defer cfRelease(top.ref)
	tree, err := e.subtree(top, findLimit)
	if err != nil {
		return 0, err
	}
	defer tree.release()
	for _, node := range tree.nodes {
		if slices.Equal(node.path, target.Path) {
			return cfRetain(node.ref), nil
		}
	}
	return 0, fmt.Errorf("%w: no element has the path %v in %q", ErrNotFound, target.Path, top.window.Name)
}

// parentRole reads the Accessibility role of an element's parent.
func (e *axElements) parentRole(ref uintptr) string {
	parent := axCopy(ref, "AXParent")
	if parent == 0 {
		return ""
	}
	defer cfRelease(parent)
	return axString(parent, "AXRole")
}

// Rows reads a table or outline's rows as text: one []string per row, each
// cell the text under the column whose header names it, up to max rows. A
// container with no columns, a column with no header, or a row it cannot
// read is a miss.
func (e *axElements) Rows(ctx context.Context, container Selector, columns []string, maxRows int) ([][]string, error) {
	return with(e, func() ([][]string, error) {
		top, err := e.frontWindow()
		if err != nil {
			return nil, err
		}
		defer cfRelease(top.ref)
		tree, err := e.subtree(top, findLimit)
		if err != nil {
			return nil, err
		}
		defer tree.release()
		matches, err := Match(top.window, tree.elements, container)
		if err != nil {
			return nil, err
		}
		found, err := One(matches, container)
		if err != nil {
			return nil, err
		}
		var grid uintptr
		for _, node := range tree.nodes {
			if slices.Equal(node.path, found.Path) {
				grid = node.ref
			}
		}
		if grid == 0 {
			return nil, fmt.Errorf("%w: the container is not there", ErrNotFound)
		}
		headers := columnHeaders(grid)
		if len(headers) == 0 {
			return nil, fmt.Errorf("%q has no table columns", container.Role)
		}
		indexes, err := columnIndexes(columns, headers)
		if err != nil {
			return nil, err
		}
		rows := axCopy(grid, "AXRows")
		if rows == 0 {
			return nil, fmt.Errorf("%q has no rows to read", container.Role)
		}
		defer cfRelease(rows)
		list := cfArray(rows)
		out := make([][]string, 0, min(len(list), maxRows))
		for r := 0; r < len(list) && r < maxRows; r++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cells := axCopy(list[r], "AXChildren")
			if cells == 0 {
				return nil, fmt.Errorf("row %d has no cells", r+1)
			}
			values := cfArray(cells)
			row := make([]string, len(indexes))
			for i, column := range indexes {
				if column >= len(values) {
					cfRelease(cells)
					return nil, fmt.Errorf("row %d has no cell under %q", r+1, columns[i])
				}
				row[i] = cellText(values[column])
			}
			cfRelease(cells)
			out = append(out, row)
		}
		return out, nil
	})
}

// columnHeaders reads the titles of a table's columns, in order: from its
// header row when it has one, else from the columns themselves.
func columnHeaders(grid uintptr) []string {
	if heads := axCopy(grid, "AXColumnHeaderUIElements"); heads != 0 {
		var headers []string
		for _, head := range cfArray(heads) {
			headers = append(headers, cellText(head))
		}
		cfRelease(heads)
		if len(headers) > 0 {
			return headers
		}
	}
	columns := axCopy(grid, "AXColumns")
	if columns == 0 {
		return nil
	}
	defer cfRelease(columns)
	var headers []string
	for _, column := range cfArray(columns) {
		title := axString(column, "AXTitle")
		if header := axCopy(column, "AXHeader"); header != 0 {
			if text := cellText(header); text != "" {
				title = text
			}
			cfRelease(header)
		}
		headers = append(headers, title)
	}
	return headers
}

// cellText reads what a cell shows: its value or title, or else that of
// the text inside it.
func cellText(cell uintptr) string {
	if text := axString(cell, "AXValue"); text != "" {
		return text
	}
	if text := axString(cell, "AXTitle"); text != "" {
		return text
	}
	children := axCopy(cell, "AXChildren")
	if children == 0 {
		return ""
	}
	defer cfRelease(children)
	for _, child := range cfArray(children) {
		if text := cellText(child); text != "" {
			return text
		}
	}
	return ""
}
