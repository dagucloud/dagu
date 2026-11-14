// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/runtime/builtin/internal/agentstep"
)

const (
	// selectOptionMethod is the method of an action that picks an option of
	// a select.
	selectOptionMethod = "selectOptionFromDropdown"
	// nativeSelectRole is the role the tree gives a native select, which
	// shows all of its options.
	nativeSelectRole = "select"
)

// pageTarget is the element at an action's location on a page.
type pageTarget struct {
	roots []*outlineNode
	// path runs from a root of the tree down to the element.
	path []*outlineNode
}

// targetOf returns the element of snap at the XPath a selector names, and
// false when no node of the tree is there. With named, the controls the
// tree leaves unnamed carry the names roots gives them; else every node
// keeps the name the tree gives it.
func targetOf(snap pageSnapshot, selector string, named bool) (pageTarget, bool) {
	xpath := strings.TrimPrefix(selector, "xpath=")
	for id, path := range snap.XPaths {
		if path != xpath {
			continue
		}
		roots := parseSnapshotTree(snap.Tree)
		if named {
			roots = snap.roots()
		}
		if found := pathTo(roots, id); found != nil {
			return pageTarget{roots: roots, path: found}, true
		}
	}
	return pageTarget{}, false
}

// identifyingTarget returns the element of snap at the XPath a selector
// names, as a recording identifies it: by the name roots gives it, or,
// when nothing names it, among the names the tree gives. A target recorded
// with no name is checked among those, as stillTargets explains.
func identifyingTarget(snap pageSnapshot, selector string) (pageTarget, bool) {
	target, ok := targetOf(snap, selector, true)
	if ok && target.node().name == "" {
		return targetOf(snap, selector, false)
	}
	return target, ok
}

// pathTo returns the nodes from one of nodes down to the node with id, or
// nil when none holds it.
func pathTo(nodes []*outlineNode, id string) []*outlineNode {
	for _, n := range nodes {
		if n.id == id {
			return []*outlineNode{n}
		}
		if path := pathTo(n.children, id); path != nil {
			return append([]*outlineNode{n}, path...)
		}
	}
	return nil
}

func (t pageTarget) node() *outlineNode { return t.path[len(t.path)-1] }

// lookalikes counts the elements of the page with the role and name of
// the element, itself included.
func (t pageTarget) lookalikes() int { return lookalikes(t.roots, t.node()) }

// distinctDepth is how many levels above the element its largest ancestor
// holding none of its lookalikes sits, such as its table row.
func (t pageTarget) distinctDepth() int {
	depth := 0
	for i := len(t.path) - 2; i >= 0 && lookalikes(t.path[i:i+1], t.node()) == 1; i-- {
		depth++
	}
	return depth
}

// context is a digest of contextText.
func (t pageTarget) context(depth int, variables map[string]string) string {
	if depth >= len(t.path) {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(t.contextText(depth, variables)), " ")))
	return hex.EncodeToString(sum[:])
}

// contextText is the text of the element's ancestor depth levels up, with
// variables' values as their placeholders, or "" above the page.
func (t pageTarget) contextText(depth int, variables map[string]string) string {
	if depth >= len(t.path) {
		return ""
	}
	return withPlaceholders(textOf(t.path[len(t.path)-1-depth]), variables)
}

// column is the header of the table column the element sits in, with
// variables' values as their placeholders, and "" when it is not in a
// table cell or the table has no header row.
func (t pageTarget) column(variables map[string]string) string {
	for i := len(t.path) - 2; i > 0; i-- {
		cell, row := t.path[i], t.path[i-1]
		if !slices.Contains(cellRoles, cell.role) || cell.role == "columnheader" {
			continue
		}
		index := 0
		for _, sibling := range row.children {
			if sibling == cell {
				break
			}
			if slices.Contains(cellRoles, sibling.role) {
				index++
			}
		}
		for _, table := range slices.Backward(t.path[:i-1]) {
			if !slices.Contains(tableRoles, table.role) {
				continue
			}
			for _, candidate := range descendants(table, "row") {
				if headers := cellsOf(candidate); len(headers) > 0 && allHeaders(candidate) {
					if index < len(headers) {
						return strings.Join(strings.Fields(withPlaceholders(headers[index], variables)), " ")
					}
					return ""
				}
			}
			return ""
		}
		return ""
	}
	return ""
}

// matches reports whether the element is the one want recorded, in the
// column it was. Without a context to tell it from its lookalikes, an element that gained more of
// them since cannot be told from the new ones, so it does not match.
func (t pageTarget) matches(want recordedTarget, variables map[string]string) bool {
	node := t.node()
	if node.role != want.Role || !sameText(withPlaceholders(node.name, variables), want.Name) {
		return false
	}
	if want.Column != "" && t.column(variables) != want.Column {
		return false
	}
	if want.Depth > 0 {
		return t.context(want.Depth, variables) == want.Context
	}
	return t.lookalikes() <= max(want.Lookalikes, 1)
}

// lookalikes counts the nodes under nodes with the role and name of node.
func lookalikes(nodes []*outlineNode, node *outlineNode) int {
	count := 0
	for _, n := range nodes {
		if n.role == node.role && sameText(n.name, node.name) {
			count++
		}
		count += lookalikes(n.children, node)
	}
	return count
}

// identified returns action carrying the element it targets, so a replay
// can tell the element is still there. An element with lookalikes on the
// page, such as a button in each row of a list, is also identified by the
// text around it. A form control, or an element with lookalikes, in a table
// cell is also identified by the header of its column. A variable's value in
// any of them is replaced by its placeholder, so they match on a later run
// with another value.
func identified(action recordedAction, target pageTarget, variables map[string]string) recordedAction {
	node := target.node()
	recorded := &recordedTarget{Role: node.role, Name: withPlaceholders(node.name, variables)}
	if slices.Contains(controlRoles, node.role) || target.lookalikes() > 1 {
		recorded.Column = target.column(variables)
	}
	if count := target.lookalikes(); count > 1 {
		recorded.Lookalikes = count
		if depth := target.distinctDepth(); depth > 0 {
			recorded.Context, recorded.Depth = target.context(depth, variables), depth
			if names := agentstep.VariableReferences(target.contextText(depth, variables)); len(names) > 0 {
				slices.Sort(names)
				recorded.ContextVariables = slices.Compact(names)
			}
		}
	}
	action.Target = recorded
	return action
}

// identifyOn identifies each action by the element it targets on snap,
// the page before the actions ran. An action whose element is not in the
// tree is left unidentified.
func identifyOn(snap pageSnapshot, actions []recordedAction, variables map[string]string) {
	for i, action := range actions {
		if target, ok := identifyingTarget(snap, action.Selector); ok {
			actions[i] = identified(action, target, variables)
		}
	}
}

// stillTargets reports whether the element at the location action recorded
// on snap is the one it acted on, and for a select, one offering the option
// it picks. An action recorded without its element is checked for the
// option only. An element recorded with no name is compared among the
// names the tree gives, without those roots adds, so the recording of a
// control that nothing named when it was recorded still finds the control
// once its table row names it.
func stillTargets(action recordedAction, snap pageSnapshot, variables map[string]string) bool {
	named := action.Target == nil || action.Target.Name != ""
	target, found := targetOf(snap, action.Selector, named)
	if action.Target != nil && (!found || !target.matches(*action.Target, variables)) {
		return false
	}
	return !found || offersOption(target.node(), action, variables)
}

// offersOption reports whether node, the element of a select action, has
// the option it picks. Any other action passes, as does an element that is
// not a native select and whose options the tree does not show.
func offersOption(node *outlineNode, action recordedAction, variables map[string]string) bool {
	if action.Method != selectOptionMethod || len(action.Arguments) == 0 {
		return true
	}
	options := optionsOf(node)
	if len(options) == 0 && node.role != nativeSelectRole {
		return true
	}
	want := agentstep.SubstituteVariables(action.Arguments[0], variables)
	return slices.ContainsFunc(options, func(option *outlineNode) bool { return sameText(option.name, want) })
}

// pickedOption names argument, the option a select action picks, as
// offersOption compares it: with its variables' values, followed by the
// argument as written. A variable that secret reports, or any variable when
// secret is nil, leaves only the argument as written.
func pickedOption(argument string, variables map[string]string, secret func(string) bool) string {
	var names []string
	for _, name := range agentstep.VariableReferences(argument) {
		if _, ok := variables[name]; ok {
			names = append(names, name)
		}
	}
	switch {
	case len(names) == 0:
		return strconv.Quote(argument)
	case secret == nil || slices.ContainsFunc(names, secret):
		return "for the secret value of " + strconv.Quote(argument)
	default:
		return fmt.Sprintf("%q, the value of %q", agentstep.SubstituteVariables(argument, variables), argument)
	}
}

// offeredOptions lists the options of node, a select, up to as many as the
// outline shows.
func offeredOptions(node *outlineNode) string {
	options := optionsOf(node)
	if len(options) == 0 {
		return "it has no options"
	}
	shown := options[:min(len(options), outlineOptionCount)]
	names := make([]string, 0, len(shown))
	for _, option := range shown {
		names = append(names, strconv.Quote(clip(option.name, outlineTextRunes)))
	}
	list := "it offers " + strings.Join(names, ", ")
	if more := len(options) - len(shown); more > 0 {
		list += fmt.Sprintf(" (+%d more)", more)
	}
	return list
}

// withPlaceholders replaces each variable value of at least
// minRecordedValueRunes in text with its %name%, longer values first.
func withPlaceholders(text string, variables map[string]string) string {
	names := slices.SortedFunc(maps.Keys(variables), func(a, b string) int {
		return cmp.Or(len(variables[b])-len(variables[a]), strings.Compare(a, b))
	})
	for _, name := range names {
		if value := variables[name]; len([]rune(value)) >= minRecordedValueRunes {
			text = strings.ReplaceAll(text, value, "%"+name+"%")
		}
	}
	return text
}

// sameText reports whether two names are equal apart from their spacing.
func sameText(a, b string) bool {
	return strings.Join(strings.Fields(a), " ") == strings.Join(strings.Fields(b), " ")
}
