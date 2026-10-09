// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	// elementsTimeout bounds a request to the automation thread. A call
	// that outlives it is abandoned, not cancelled: UI Automation offers no
	// way to stop one.
	elementsTimeout = 5 * time.Second
	// closeTimeout bounds the wait for the automation thread to finish.
	closeTimeout = time.Second
	// findLimit is how many elements a search reads from a window.
	findLimit = 5000
	// chromiumWait is how long to let a Chromium window build its
	// accessibility tree, which it does when a client first asks.
	chromiumWait = 300 * time.Millisecond
	// maxDepth bounds the walk from an element up to its window.
	maxDepth = 64
)

var errElementsClosed = errors.New("elements closed")

// elementProperties are cached for every element read.
var elementProperties = []int32{
	uiaRuntimeIdProperty, uiaBoundingRectangleProperty, uiaControlTypeProperty, uiaNameProperty,
	uiaAutomationIdProperty, uiaClassNameProperty, uiaLabeledByProperty, uiaIsPasswordProperty,
	uiaNativeWindowHandleProperty, uiaIsOffscreenProperty, uiaFrameworkIdProperty,
	uiaValueValueProperty, uiaLegacyIAccessibleValueProperty,
}

// parentProperties are cached for an element's ancestors and their
// children, which only need to be told apart and placed.
var parentProperties = []int32{
	uiaRuntimeIdProperty, uiaControlTypeProperty, uiaNameProperty, uiaNativeWindowHandleProperty,
}

// roles maps UI Automation control types onto roles.
var roles = map[int32]string{
	uiaButton: RoleButton, uiaSplitButton: RoleButton,
	uiaEdit: RoleTextField, uiaDocument: RoleTextField,
	uiaText:        RoleText,
	uiaCheckBox:    RoleCheckbox,
	uiaRadioButton: RoleRadio,
	uiaComboBox:    RoleComboBox,
	uiaListItem:    RoleListItem, uiaTreeItem: RoleListItem,
	uiaMenuItem:  RoleMenuItem,
	uiaTabItem:   RoleTab,
	uiaHyperlink: RoleLink,
	uiaDataItem:  RoleCell, uiaHeaderItem: RoleCell,
	uiaGroup:  RoleGroup,
	uiaWindow: RoleWindow,
}

// roleOf maps a control type onto a role. A top-level element is a window
// whatever its control type, since some frameworks expose a pane.
func roleOf(controlType int32, topLevel bool) string {
	if topLevel {
		return RoleWindow
	}
	if role, ok := roles[controlType]; ok {
		return role
	}
	return RoleOther
}

// uiaElements reads elements through UI Automation. Every call runs on
// one thread that owns the COM apartment and every COM pointer; callers
// receive plain values.
type uiaElements struct {
	requests  chan request
	quit      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

type request struct {
	fn     func(*uiaWorker) (any, error)
	result chan response
}

type response struct {
	value any
	err   error
}

// uiaWorker is the automation thread's state.
type uiaWorker struct {
	auto         *automation
	root         *element
	walker       *treeWalker
	cacheElement *cacheRequest
	cacheParent  *cacheRequest
	cacheSubtree *cacheRequest
}

// OpenElements starts reading elements through UI Automation. It needs
// no desktop lease; reading elements beside a running step is harmless.
func OpenElements() (Elements, error) {
	makeDPIAware()
	e := &uiaElements{requests: make(chan request), quit: make(chan struct{}), done: make(chan struct{})}
	ready := make(chan error, 1)
	go e.serve(ready)
	if err := <-ready; err != nil {
		return nil, fmt.Errorf("UI Automation is unavailable: %w", err)
	}
	return e, nil
}

// serve runs the automation thread. The thread is locked for the COM
// apartment and never unlocked, so Go lets it end with the goroutine.
func (e *uiaElements) serve(ready chan<- error) {
	runtime.LockOSThread()
	defer close(e.done)
	if err := windows.CoInitializeEx(0, windows.COINIT_MULTITHREADED); err != nil {
		ready <- err
		return
	}
	defer windows.CoUninitialize()
	w, err := newWorker()
	if err != nil {
		ready <- err
		return
	}
	defer w.release()
	ready <- nil
	for {
		select {
		case <-e.quit:
			return
		case req := <-e.requests:
			value, err := req.fn(w)
			req.result <- response{value: value, err: err}
		}
	}
}

// call performs fn on the automation thread and waits for its answer.
// The answer travels only through the channel, so a call abandoned at the
// deadline cannot write into anything its caller still holds. The send is
// bounded too, since a hung application keeps the thread busy.
func call[T any](e *uiaElements, what string, fn func(*uiaWorker) (T, error)) (T, error) {
	var zero T
	req := request{
		fn:     func(w *uiaWorker) (any, error) { return fn(w) },
		result: make(chan response, 1),
	}
	deadline := time.NewTimer(elementsTimeout)
	defer deadline.Stop()
	select {
	case e.requests <- req:
	case <-e.quit:
		return zero, errElementsClosed
	case <-deadline.C:
		return zero, fmt.Errorf("%s: the automation thread is still busy after %s: %w", what, elementsTimeout, context.DeadlineExceeded)
	}
	select {
	case res := <-req.result:
		if res.err != nil {
			return zero, res.err
		}
		value, _ := res.value.(T)
		return value, nil
	case <-deadline.C:
		return zero, fmt.Errorf("%s: no answer within %s: %w", what, elementsTimeout, context.DeadlineExceeded)
	}
}

// run performs fn on the automation thread for its error alone.
func (e *uiaElements) run(what string, fn func(*uiaWorker) error) error {
	_, err := call(e, what, func(w *uiaWorker) (struct{}, error) { return struct{}{}, fn(w) })
	return err
}

func (e *uiaElements) Close() error {
	e.closeOnce.Do(func() { close(e.quit) })
	select {
	case <-e.done:
		return nil
	case <-time.After(closeTimeout):
		return errors.New("UI Automation is still busy; its thread is left behind")
	}
}

func (e *uiaElements) At(x, y int) (Element, error) {
	return call(e, "element at point", func(w *uiaWorker) (Element, error) {
		el, err := w.auto.elementFromPoint(x, y, w.cacheElement)
		if err != nil {
			return Element{}, err
		}
		defer el.release()
		return w.place(el)
	})
}

func (e *uiaElements) Focused() (Element, error) {
	return call(e, "focused element", func(w *uiaWorker) (Element, error) {
		el, err := w.auto.focusedElement(w.cacheElement)
		if err != nil {
			return Element{}, err
		}
		defer el.release()
		return w.place(el)
	})
}

func (e *uiaElements) FrontWindow() (Element, error) {
	return call(e, "front window", func(w *uiaWorker) (Element, error) {
		top, err := w.frontWindow()
		if err != nil {
			return Element{}, err
		}
		defer top.el.release()
		return top.window, nil
	})
}

func (e *uiaElements) Find(sel Selector) ([]Element, error) {
	return call(e, "find", func(w *uiaWorker) ([]Element, error) {
		top, err := w.frontWindow()
		if err != nil {
			return nil, err
		}
		defer top.el.release()
		tree, err := w.subtree(top, findLimit)
		if err != nil {
			return nil, err
		}
		defer tree.release()
		return Match(top.window, tree.elements, sel)
	})
}

func (e *uiaElements) Outline(window Element, limit int) ([]Element, error) {
	return call(e, "outline", func(w *uiaWorker) ([]Element, error) {
		top, err := w.resolveWindow(window)
		if err != nil {
			return nil, err
		}
		defer top.el.release()
		tree, err := w.subtree(top, limit)
		if err != nil {
			return nil, err
		}
		defer tree.release()
		return tree.elements, nil
	})
}

// Focus finds the element again, under its centre first and then by its
// path in the front window, and gives it the keyboard focus.
func (e *uiaElements) Focus(target Element) error {
	return e.run("focus", func(w *uiaWorker) error {
		if !target.Bounds.Empty() {
			centre := target.Bounds.Min.Add(target.Bounds.Size().Div(2))
			if el, err := w.auto.elementFromPoint(centre.X, centre.Y, w.cacheElement); err == nil {
				defer el.release()
				found, err := w.convert(el, false)
				if err == nil && found.Role == target.Role && found.Name == target.Name && found.ID == target.ID {
					return el.setFocus()
				}
			}
		}
		if len(target.Path) == 0 {
			return fmt.Errorf("%w: the element is not where it was", ErrNotFound)
		}
		top, err := w.frontWindow()
		if err != nil {
			return err
		}
		defer top.el.release()
		tree, err := w.subtree(top, findLimit)
		if err != nil {
			return err
		}
		defer tree.release()
		for _, node := range tree.nodes {
			if slices.Equal(node.path, target.Path) {
				return node.el.setFocus()
			}
		}
		return fmt.Errorf("%w: no element has the path %v in %q", ErrNotFound, target.Path, top.window.Name)
	})
}

// newWorker creates the automation client and what every call shares.
func newWorker() (*uiaWorker, error) {
	obj, err := coCreateInstance(&clsidCUIAutomation8, &iidIUIAutomation)
	if err != nil {
		if obj, err = coCreateInstance(&clsidCUIAutomation, &iidIUIAutomation); err != nil {
			return nil, fmt.Errorf("create the automation client: %w", err)
		}
	}
	w := &uiaWorker{auto: (*automation)(obj)}
	if err := w.setup(); err != nil {
		w.release()
		return nil, err
	}
	return w, nil
}

func (w *uiaWorker) setup() error {
	filter, err := w.auto.controlViewCondition()
	if err != nil {
		return err
	}
	defer filter.release()
	if w.cacheElement, err = w.newCache(filter, treeScopeElement, elementProperties); err != nil {
		return err
	}
	if w.cacheParent, err = w.newCache(filter, treeScopeElement|treeScopeChildren, parentProperties); err != nil {
		return err
	}
	if w.cacheSubtree, err = w.newCache(filter, treeScopeSubtree, elementProperties); err != nil {
		return err
	}
	if w.walker, err = w.auto.controlViewWalker(); err != nil {
		return err
	}
	w.root, err = w.auto.rootElement(w.cacheParent)
	return err
}

func (w *uiaWorker) newCache(filter *condition, scope int32, properties []int32) (*cacheRequest, error) {
	cache, err := w.auto.createCacheRequest()
	if err != nil {
		return nil, err
	}
	for _, id := range properties {
		if err := cache.addProperty(id); err != nil {
			cache.release()
			return nil, err
		}
	}
	if err := errors.Join(cache.setTreeScope(scope), cache.setTreeFilter(filter), cache.setElementMode(automationElementModeFull)); err != nil {
		cache.release()
		return nil, err
	}
	return cache, nil
}

func (w *uiaWorker) release() {
	w.cacheElement.release()
	w.cacheParent.release()
	w.cacheSubtree.release()
	w.walker.release()
	w.root.release()
	w.auto.release()
}

// topWindow is a top-level window: its element with every property
// cached, which the holder releases, its value, and its class name.
type topWindow struct {
	el        *element
	window    Element
	className string
}

// frontWindow reads the window in front.
func (w *uiaWorker) frontWindow() (*topWindow, error) {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return nil, errors.New("no window is in front")
	}
	el, err := w.auto.elementFromHandle(uintptr(hwnd), w.cacheElement)
	if err != nil {
		return nil, err
	}
	top, err := w.asWindow(el)
	if err != nil {
		el.release()
		return nil, err
	}
	return top, nil
}

// asWindow describes a top-level element whose properties are cached. The
// caller keeps ownership of el.
func (w *uiaWorker) asWindow(el *element) (*topWindow, error) {
	window, err := w.convert(el, true)
	if err != nil {
		return nil, err
	}
	window.Window = window.Name
	hwnd, _ := el.cachedInt(uiaNativeWindowHandleProperty)
	window.App = appName(windows.HWND(hwnd)) //nolint:gosec // A window handle fits in 32 bits.
	className, _ := el.cachedClassName()
	return &topWindow{el: el, window: window, className: className}, nil
}

// placement is where an element sits: its top-level window, which the
// holder releases, the path down from it, and the window's title.
type placement struct {
	top   *element
	path  []PathStep
	title string
}

// resolveWindow finds the window an element describes: the one in front,
// or the top-level window under the element's centre.
func (w *uiaWorker) resolveWindow(window Element) (*topWindow, error) {
	front, err := w.frontWindow()
	if err != nil {
		return nil, err
	}
	if window.Name == "" || window.Name == front.window.Name {
		return front, nil
	}
	front.el.release()
	if window.Bounds.Empty() {
		return nil, fmt.Errorf("window %q is not in front", window.Name)
	}
	centre := window.Bounds.Min.Add(window.Bounds.Size().Div(2))
	at, err := w.auto.elementFromPoint(centre.X, centre.Y, w.cacheElement)
	if err != nil {
		return nil, err
	}
	defer at.release()
	place, err := w.ancestry(at)
	if err != nil {
		return nil, err
	}
	defer place.top.release()
	// The walk cached only what places an element; the window needs
	// everything.
	full, err := place.top.buildUpdatedCache(w.cacheElement)
	if err != nil {
		return nil, err
	}
	found, err := w.asWindow(full)
	if err != nil {
		full.release()
		return nil, err
	}
	if found.window.Name != window.Name {
		full.release()
		return nil, fmt.Errorf("window %q is not on screen; %q is there", window.Name, found.window.Name)
	}
	return found, nil
}

// place describes an element whose properties are cached, with its window
// and path.
func (w *uiaWorker) place(el *element) (Element, error) {
	place, err := w.ancestry(el)
	if err != nil {
		return Element{}, err
	}
	defer place.top.release()
	if len(place.path) == 0 {
		window, err := w.asWindow(el)
		if err != nil {
			return Element{}, err
		}
		return window.window, nil
	}
	found, err := w.convert(el, false)
	if err != nil {
		return Element{}, err
	}
	found.Path, found.Window = place.path, place.title
	return found, nil
}

// ancestry walks from an element up to its top-level window. The caller
// releases the window it returns. An element that is itself a window has
// an empty path.
func (w *uiaWorker) ancestry(el *element) (placement, error) {
	var steps []PathStep
	current, owned := el, false
	drop := func() {
		if owned {
			current.release()
		}
	}
	for range maxDepth {
		parent, err := w.walker.parent(current, w.cacheParent)
		if err != nil {
			drop()
			return placement{}, err
		}
		if parent == nil {
			drop()
			return placement{}, errors.New("the element has no window")
		}
		isRoot, err := w.auto.compareElements(parent, w.root)
		if err != nil {
			parent.release()
			drop()
			return placement{}, err
		}
		if isRoot {
			parent.release()
			title, err := current.cachedName()
			if err != nil {
				drop()
				return placement{}, err
			}
			if !owned {
				// The caller's element is its own window; give the caller a
				// reference of its own to release.
				current.com().addRef()
			}
			slices.Reverse(steps)
			return placement{top: current, path: steps, title: title}, nil
		}
		step, err := w.stepOf(parent, current)
		if err != nil {
			parent.release()
			drop()
			return placement{}, err
		}
		steps = append(steps, step)
		drop()
		current, owned = parent, true
	}
	drop()
	return placement{}, errors.New("the element is nested too deep")
}

// stepOf describes child's place among parent's cached children.
func (w *uiaWorker) stepOf(parent, child *element) (PathStep, error) {
	controlType, err := child.cachedControlType()
	if err != nil {
		return PathStep{}, err
	}
	name, err := child.cachedName()
	if err != nil {
		return PathStep{}, err
	}
	step := PathStep{Role: roleOf(controlType, false), Name: name}
	children, err := parent.cachedChildren()
	if err != nil {
		return PathStep{}, err
	}
	if children == nil {
		return PathStep{}, errors.New("the element's parent lists no children")
	}
	defer children.release()
	n, err := children.length()
	if err != nil {
		return PathStep{}, err
	}
	for i := range n {
		sibling, err := children.element(i)
		if err != nil {
			return PathStep{}, err
		}
		same, err := w.auto.compareElements(sibling, child)
		if err == nil && same {
			sibling.release()
			return step, nil
		}
		siblingType, err := sibling.cachedControlType()
		sibling.release()
		if err != nil {
			return PathStep{}, err
		}
		if roleOf(siblingType, false) == step.Role {
			step.Index++
		}
	}
	return step, errors.New("the element is not among its parent's children")
}

// convert reads an element's cached properties.
func (w *uiaWorker) convert(el *element, topLevel bool) (Element, error) {
	controlType, err := el.cachedControlType()
	if err != nil {
		return Element{}, err
	}
	name, err := el.cachedName()
	if err != nil {
		return Element{}, err
	}
	id, err := el.cachedAutomationID()
	if err != nil {
		return Element{}, err
	}
	bounds, err := el.cachedBoundingRectangle()
	if err != nil {
		return Element{}, err
	}
	found := Element{Role: roleOf(controlType, topLevel), Name: name, ID: id, Bounds: bounds}
	if password, err := el.cachedIsPassword(); err == nil && !password {
		found.Value = w.value(el)
	}
	if label, err := el.cachedLabeledBy(); err == nil && label != nil {
		found.Label, _ = label.currentName()
		label.release()
	}
	return found, nil
}

// value reads what an element holds: its value pattern first, then what
// the older accessibility interface reports.
func (w *uiaWorker) value(el *element) string {
	if text, err := el.cachedText(uiaValueValueProperty); err == nil && text != "" {
		return text
	}
	text, _ := el.cachedText(uiaLegacyIAccessibleValueProperty)
	return text
}

// subtree reads the whole control tree of a window in one request and
// walks it. A Chromium window builds its tree when a client first asks,
// so a sparse answer from one is read again after a moment.
func (w *uiaWorker) subtree(top *topWindow, limit int) (*subtree, error) {
	tree, err := w.readSubtree(top, limit)
	if err != nil {
		return nil, err
	}
	if len(tree.nodes) <= 2 && strings.HasPrefix(top.className, "Chrome_WidgetWin") {
		tree.release()
		time.Sleep(chromiumWait)
		if tree, err = w.readSubtree(top, limit); err != nil {
			return nil, err
		}
	}
	if len(tree.nodes) == 0 {
		tree.release()
		return nil, fmt.Errorf("%w: %q may belong to a process run as administrator, or draw its own controls", ErrNoElements, top.window.Name)
	}
	return tree, nil
}

func (w *uiaWorker) readSubtree(top *topWindow, limit int) (*subtree, error) {
	rootEl, err := top.el.buildUpdatedCache(w.cacheSubtree)
	if err != nil {
		return nil, err
	}
	tree := &subtree{worker: w, pool: []*element{rootEl}}
	root := &subtreeNode{tree: tree, el: rootEl}
	tree.elements = outlineTree(root, top.window.Name, limit)
	if tree.err != nil {
		tree.release()
		return nil, tree.err
	}
	return tree, nil
}

// subtree is one window's cached control tree. Every element it created
// is released together.
type subtree struct {
	worker   *uiaWorker
	pool     []*element
	nodes    []*subtreeNode
	elements []Element
	err      error
}

func (t *subtree) release() {
	for _, el := range t.pool {
		el.release()
	}
	t.pool = nil
}

// subtreeNode adapts a cached element to the tree walk.
type subtreeNode struct {
	tree    *subtree
	el      *element
	path    []PathStep
	loaded  bool
	value   Element
	visibly bool
}

func (n *subtreeNode) load() {
	if n.loaded {
		return
	}
	n.loaded = true
	found, err := n.tree.worker.convert(n.el, false)
	if err != nil {
		n.tree.err = errors.Join(n.tree.err, err)
		return
	}
	n.value = found
	offscreen, _ := n.el.cachedIsOffscreen()
	n.visibly = !offscreen && !found.Bounds.Empty()
}

func (n *subtreeNode) element() Element {
	n.load()
	return n.value
}

func (n *subtreeNode) visible() bool {
	n.load()
	return n.visibly
}

func (n *subtreeNode) placed(path []PathStep) {
	n.path = path
}

func (n *subtreeNode) children() []treeNode {
	children, err := n.el.cachedChildren()
	if err != nil || children == nil {
		return nil
	}
	defer children.release()
	count, err := children.length()
	if err != nil {
		n.tree.err = errors.Join(n.tree.err, err)
		return nil
	}
	out := make([]treeNode, 0, count)
	for i := range count {
		el, err := children.element(i)
		if err != nil {
			n.tree.err = errors.Join(n.tree.err, err)
			continue
		}
		n.tree.pool = append(n.tree.pool, el)
		child := &subtreeNode{tree: n.tree, el: el}
		n.tree.nodes = append(n.tree.nodes, child)
		out = append(out, child)
	}
	return out
}

// appName returns the image name of the process that owns a window,
// without its extension, or "" when it cannot be read.
func appName(hwnd windows.HWND) string {
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
		return ""
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(process) }()
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf)) //nolint:gosec // The buffer is small.
	if err := windows.QueryFullProcessImageName(process, 0, &buf[0], &size); err != nil {
		return ""
	}
	base := filepath.Base(windows.UTF16ToString(buf[:size]))
	return strings.TrimSuffix(base, filepath.Ext(base))
}
