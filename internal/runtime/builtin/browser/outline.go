// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// outlineTextRunes and outlineLinkRunes cut a text or an address in the
	// outline.
	outlineTextRunes = 100
	outlineLinkRunes = 200
	// outlineOptionCount is how many options of a select the outline lists.
	outlineOptionCount = 15
	// outlineGroupMin and outlineGroupKeep collapse a run of more than
	// outlineGroupMin siblings of the same shape to its first
	// outlineGroupKeep.
	outlineGroupMin  = 5
	outlineGroupKeep = 3
	// outlineFindLimit bounds the matches a search shows.
	outlineFindLimit = 50
)

// outlineOptions selects what an outline shows.
type outlineOptions struct {
	// Find shows only the entries containing it, case-insensitively, with
	// the entries they sit in.
	Find string
	// MaxChars bounds the outline; zero leaves it unbounded.
	MaxChars int
}

// snapshotLine matches one node of a snapshot tree: indentation, ID, and
// the rest of the line.
var snapshotLine = regexp.MustCompile(`^( *)\[([^\]]+)\] (.*)$`)

// snapshotFlags matches the states a snapshot appends to a node, such as
// " [checked]".
var snapshotFlags = regexp.MustCompile(`( \[[a-z]+\])+$`)

// outlineNode is one node of a snapshot tree.
type outlineNode struct {
	id, role, name string
	flags          []string
	children       []*outlineNode
}

// parseSnapshotTree reads a snapshot tree into its root nodes.
func parseSnapshotTree(tree string) []*outlineNode {
	var roots []*outlineNode
	// open holds the last node seen at each depth.
	var open []*outlineNode
	for line := range strings.SplitSeq(tree, "\n") {
		m := snapshotLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if m == nil {
			continue
		}
		node := &outlineNode{id: m[2]}
		rest := m[3]
		if flags := snapshotFlags.FindString(rest); flags != "" {
			rest = strings.TrimSuffix(rest, flags)
			for flag := range strings.SplitSeq(strings.TrimSpace(flags), " ") {
				node.flags = append(node.flags, strings.Trim(flag, "[]"))
			}
		}
		node.role, node.name, _ = strings.Cut(rest, ": ")
		node.name = strings.TrimSpace(node.name)
		depth := min(len(m[1])/2, len(open))
		open = open[:depth]
		if depth == 0 {
			roots = append(roots, node)
		} else {
			parent := open[depth-1]
			parent.children = append(parent.children, node)
		}
		open = append(open, node)
	}
	return roots
}

// outlineEntry is one line of an outline and the lines under it.
type outlineEntry struct {
	line string
	// shape is the same for entries a reader would call alike, such as the
	// rows of one table.
	shape    string
	children []*outlineEntry
}

// renderOutline describes a page for someone deciding what to do on it:
// its headings, fields, buttons, links, and messages, its tables and lists
// with repeated rows collapsed, and its text, without the values typed into
// fields. It reports whether the outline was cut to fit opts.MaxChars and,
// with opts.Find, how many entries matched.
func renderOutline(snap pageSnapshot, opts outlineOptions) (string, bool, int) {
	b := outlineBuilder{urls: snap.URLs}
	b.page, _ = url.Parse(snap.URL)
	var entries []*outlineEntry
	for _, root := range snap.roots() {
		entries = append(entries, b.build(root)...)
	}
	entries = contentFirst(entries)
	var lines []string
	matches := 0
	if opts.Find != "" {
		lines, matches = findOutlineLines(entries, strings.ToLower(opts.Find))
	} else {
		lines = flattenOutline(collapseOutline(entries), 0, nil)
	}
	text, truncated := fitOutline(lines, opts.MaxChars)
	return text, truncated, matches
}

type outlineBuilder struct {
	urls map[string]string
	// page is the address of the page outlined, against which links to the
	// same site are shown by their path.
	page *url.URL
}

// containerRoles group what they hold under their own line, and are left out
// when they hold nothing the outline shows. Other roles that hold nodes only
// wrap them, and the outline shows what they hold in their place.
var containerRoles = []string{
	"banner", "navigation", "main", "contentinfo", "complementary", "region", "search", "form",
	"dialog", "alertdialog", "Iframe", "list", "listbox", "menu", "menubar", "tablist", "group",
	"radiogroup", "toolbar", "article", "tree",
}

// furnitureRoles are the site's own parts that every page repeats: its
// header, menus, sidebars, and footer.
var furnitureRoles = []string{"banner", "navigation", "complementary", "contentinfo"}

var messageRoles = []string{"alert", "status", "log", "marquee", "timer"}

var fieldRoles = []string{"textbox", "searchbox", "spinbutton", "slider"}

var toggleRoles = []string{"checkbox", "radio", "switch", "menuitemcheckbox", "menuitemradio"}

var pressableRoles = []string{"button", "tab", "menuitem", "treeitem", "option"}

var choiceRoles = []string{"select", "combobox", "listbox"}

// fileInputRole is the role the tree gives a file input.
const fileInputRole = "input, file"

var cellRoles = []string{"cell", "gridcell", "columnheader", "rowheader"}

// tableRoles are the roles of the tables that hold rows of cells.
var tableRoles = []string{"table", "grid", "treegrid"}

// droppedRoles carry nothing a reader of the outline needs.
var droppedRoles = []string{"ListMarker", "LineBreak", "InlineTextBox", "none", "presentation"}

func (b outlineBuilder) build(n *outlineNode) []*outlineEntry {
	switch {
	case slices.Contains(droppedRoles, n.role):
		return nil
	case n.role == "StaticText":
		if n.name == "" {
			return nil
		}
		return []*outlineEntry{{line: "text: " + clip(n.name, outlineTextRunes), shape: "text"}}
	case n.role == "link":
		return []*outlineEntry{b.link(n)}
	case slices.Contains(choiceRoles, n.role) && len(optionsOf(n)) > 0:
		return []*outlineEntry{choiceEntry(n)}
	case slices.Contains(fieldRoles, n.role), n.role == "combobox":
		// A field's text is what was typed into it, which may be a secret.
		return []*outlineEntry{{line: withRef(n, withName(n.role, n.name)), shape: n.role}}
	case n.role == fileInputRole:
		return []*outlineEntry{{line: withRef(n, withName(roleWord(n.role), n.name)), shape: n.role}}
	case slices.Contains(toggleRoles, n.role), slices.Contains(pressableRoles, n.role):
		return []*outlineEntry{{line: withRef(n, withFlags(withName(n.role, n.name), n.flags)), shape: n.role}}
	case n.role == "heading":
		return []*outlineEntry{{line: "heading: " + clip(n.name, outlineTextRunes), shape: "heading"}}
	case slices.Contains(messageRoles, n.role):
		return []*outlineEntry{{line: n.role + ": " + clip(textOf(n), outlineTextRunes), shape: n.role}}
	case slices.Contains(tableRoles, n.role):
		return b.table(n)
	case n.role == "row":
		return b.row(n)
	case n.role == "listitem":
		return []*outlineEntry{b.item(n)}
	case n.role == "LabelText":
		// A label's text names the control it holds, which the control's
		// line already shows.
		if controls := b.controls(n); len(controls) > 0 {
			return controls
		}
		return b.children(n)
	case slices.Contains(containerRoles, n.role):
		children := b.children(n)
		if len(children) == 0 {
			return nil
		}
		role := n.role
		if role == "Iframe" {
			// A frame is a page of its own.
			role, children = "iframe", contentFirst(children)
		}
		return []*outlineEntry{{line: withName(role, n.name), shape: role, children: children}}
	default:
		return b.children(n)
	}
}

// contentFirst puts what the page itself shows before the site's furniture,
// so a limit on the outline leaves out menus rather than the content. Only
// the page's own parts move; a menu inside the content, such as its pages,
// stays beside what it belongs to.
func contentFirst(entries []*outlineEntry) []*outlineEntry {
	var content, furniture []*outlineEntry
	for _, entry := range entries {
		if slices.Contains(furnitureRoles, entry.shape) {
			furniture = append(furniture, entry)
		} else {
			content = append(content, entry)
		}
	}
	return append(content, furniture...)
}

func (b outlineBuilder) children(n *outlineNode) []*outlineEntry {
	var entries []*outlineEntry
	for _, child := range n.children {
		entries = append(entries, b.build(child)...)
	}
	return entries
}

func (b outlineBuilder) link(n *outlineNode) *outlineEntry {
	line := withRef(n, withName("link", n.name))
	if target := b.urls[n.id]; target != "" && !strings.HasPrefix(strings.ToLower(target), "javascript:") {
		line += " -> " + clip(b.address(target), outlineLinkRunes)
	}
	return &outlineEntry{line: line, shape: "link"}
}

// address shows a link to the page's own site by its path, which the page's
// address completes, and any other in full.
func (b outlineBuilder) address(target string) string {
	parsed, err := url.Parse(target)
	if err != nil || b.page == nil || parsed.Scheme != b.page.Scheme || parsed.Host != b.page.Host || parsed.Opaque != "" {
		return target
	}
	return parsed.RequestURI()
}

// table describes a table by its size and columns, with a line per row.
func (b outlineBuilder) table(n *outlineNode) []*outlineEntry {
	var columns []string
	var rows []*outlineEntry
	for _, row := range descendants(n, "row") {
		cells := cellsOf(row)
		if len(cells) > 0 && allHeaders(row) {
			if columns == nil {
				columns = cells
			}
			continue
		}
		rows = append(rows, b.row(row)...)
	}
	line := fmt.Sprintf("%s: %d rows", n.role, len(rows))
	if n.name != "" {
		line = fmt.Sprintf("%s %s: %d rows", n.role, strconv.Quote(clip(n.name, outlineTextRunes)), len(rows))
	}
	if len(columns) > 0 {
		line += "; columns: " + clip(strings.Join(columns, " | "), outlineTextRunes)
	}
	return []*outlineEntry{{line: line, shape: n.role, children: rows}}
}

// row describes a table row by its cells, with the controls in it under it.
func (b outlineBuilder) row(n *outlineNode) []*outlineEntry {
	cells := cellsOf(n)
	if len(cells) == 0 {
		return b.children(n)
	}
	controls := b.controls(n)
	return []*outlineEntry{{line: "row: " + clip(strings.Join(cells, " | "), outlineTextRunes), shape: "row" + shapeOf(controls), children: controls}}
}

// item describes a list item by its text, with the controls in it under it.
// An item that is one link, as a list of results often is, is that link.
func (b outlineBuilder) item(n *outlineNode) *outlineEntry {
	controls := b.controls(n)
	if links := descendants(n, "link"); len(controls) == 1 && len(links) == 1 && links[0].name == textOf(n) {
		return controls[0]
	}
	return &outlineEntry{line: "item: " + clip(textOf(n), outlineTextRunes), shape: "item" + shapeOf(controls), children: controls}
}

// controls returns the entries of the links, buttons, fields, and other
// controls under n, leaving out its text.
func (b outlineBuilder) controls(n *outlineNode) []*outlineEntry {
	var entries []*outlineEntry
	for _, entry := range b.children(n) {
		if entry.shape != "text" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func choiceEntry(n *outlineNode) *outlineEntry {
	options := optionsOf(n)
	names := make([]string, 0, min(len(options), outlineOptionCount))
	var chosen []string
	for i, option := range options {
		if slices.Contains(option.flags, "selected") {
			chosen = append(chosen, option.name)
		}
		if i < outlineOptionCount {
			names = append(names, option.name)
		}
	}
	line := withRef(n, withName(n.role, n.name))
	if len(chosen) > 0 {
		line += " = " + strings.Join(chosen, ", ")
	}
	line += "; options: " + strings.Join(names, ", ")
	if more := len(options) - len(names); more > 0 {
		line += fmt.Sprintf(" (+%d more)", more)
	}
	return &outlineEntry{line: line, shape: n.role}
}

func optionsOf(n *outlineNode) []*outlineNode {
	return descendants(n, "option")
}

// descendants returns the nodes with role under n, not looking inside them.
func descendants(n *outlineNode, role string) []*outlineNode {
	var found []*outlineNode
	for _, child := range n.children {
		if child.role == role {
			found = append(found, child)
			continue
		}
		found = append(found, descendants(child, role)...)
	}
	return found
}

// cellsOf returns the text of each cell of a row.
func cellsOf(row *outlineNode) []string {
	var cells []string
	for _, cell := range row.children {
		if !slices.Contains(cellRoles, cell.role) {
			continue
		}
		text := cell.name
		if text == "" {
			text = textOf(cell)
		}
		cells = append(cells, text)
	}
	return cells
}

func allHeaders(row *outlineNode) bool {
	for _, cell := range row.children {
		if slices.Contains(cellRoles, cell.role) && cell.role != "columnheader" {
			return false
		}
	}
	return true
}

// textOf joins the text a person reads in n, leaving out what was typed
// into fields.
func textOf(n *outlineNode) string {
	var parts []string
	var walk func(*outlineNode)
	walk = func(node *outlineNode) {
		switch {
		case slices.Contains(fieldRoles, node.role), node.role == "combobox", slices.Contains(droppedRoles, node.role):
			return
		case node.role == "StaticText":
			if node.name != "" {
				parts = append(parts, node.name)
			}
			return
		case node.role == "link", node.role == "button", slices.Contains(cellRoles, node.role):
			if node.name != "" {
				parts = append(parts, node.name)
				return
			}
		}
		for _, child := range node.children {
			walk(child)
		}
	}
	for _, child := range n.children {
		walk(child)
	}
	return strings.Join(parts, " ")
}

func shapeOf(entries []*outlineEntry) string {
	shapes := make([]string, 0, len(entries))
	for _, entry := range entries {
		shapes = append(shapes, entry.shape)
	}
	return "(" + strings.Join(shapes, ",") + ")"
}

// withRef puts the node's ID before its line, so an operation can name the
// node by it.
func withRef(n *outlineNode, line string) string {
	return "[" + n.id + "] " + line
}

func withName(role, name string) string {
	if name == "" {
		return role
	}
	return role + " " + strconv.Quote(clip(name, outlineTextRunes))
}

func withFlags(line string, flags []string) string {
	for _, flag := range flags {
		line += " [" + flag + "]"
	}
	return line
}

// clip cuts text to limit runes on one line, marking the cut.
func clip(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit-1]) + "…"
}

// collapseOutline keeps the first few of every run of more than
// outlineGroupMin siblings of the same shape and counts the rest.
func collapseOutline(entries []*outlineEntry) []*outlineEntry {
	var kept []*outlineEntry
	for i := 0; i < len(entries); {
		end := i + 1
		for end < len(entries) && entries[end].shape == entries[i].shape {
			end++
		}
		run := entries[i:end]
		if len(run) > outlineGroupMin {
			for _, entry := range run[:outlineGroupKeep] {
				kept = append(kept, collapsedEntry(entry))
			}
			kept = append(kept, &outlineEntry{line: fmt.Sprintf("… %d more %s like these", len(run)-outlineGroupKeep, plural(run[0].line))})
		} else {
			for _, entry := range run {
				kept = append(kept, collapsedEntry(entry))
			}
		}
		i = end
	}
	return kept
}

func collapsedEntry(entry *outlineEntry) *outlineEntry {
	return &outlineEntry{line: entry.line, shape: entry.shape, children: collapseOutline(entry.children)}
}

// plural names what a line is, in the plural: "row: …" gives "rows".
func plural(line string) string {
	// The element's ID comes before its kind.
	if strings.HasPrefix(line, "[") {
		_, line, _ = strings.Cut(line, "] ")
	}
	kind, _, _ := strings.Cut(line, " ")
	kind = strings.TrimSuffix(kind, ":")
	switch {
	case kind == "text":
		return "lines"
	case strings.HasSuffix(kind, "s"), strings.HasSuffix(kind, "x"):
		return kind + "es"
	}
	return kind + "s"
}

func flattenOutline(entries []*outlineEntry, depth int, lines []string) []string {
	indent := strings.Repeat("  ", depth)
	for _, entry := range entries {
		lines = append(lines, indent+entry.line)
		lines = flattenOutline(entry.children, depth+1, lines)
	}
	return lines
}

// findOutlineLines returns the entries whose line contains find, each with
// the entries it sits in and the entries under it, and how many matched.
func findOutlineLines(entries []*outlineEntry, find string) ([]string, int) {
	var lines []string
	matches := 0
	var walk func(entries []*outlineEntry, path []string)
	walk = func(entries []*outlineEntry, path []string) {
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.line), find) {
				matches++
				if matches > outlineFindLimit {
					continue
				}
				for depth, ancestor := range path {
					line := strings.Repeat("  ", depth) + ancestor
					// Matches under one entry share its line.
					if !slices.Contains(lines, line) {
						lines = append(lines, line)
					}
				}
				lines = append(lines, strings.Repeat("  ", len(path))+entry.line)
				lines = flattenOutline(collapseOutline(entry.children), len(path)+1, lines)
				continue
			}
			walk(entry.children, append(slices.Clone(path), entry.line))
		}
	}
	walk(entries, nil)
	if matches > outlineFindLimit {
		lines = append(lines, fmt.Sprintf("… %d more matches; narrow the search", matches-outlineFindLimit))
	}
	return lines, matches
}

// fitOutline joins lines, cutting whole lines to stay within maxChars.
func fitOutline(lines []string, maxChars int) (string, bool) {
	var b strings.Builder
	chars := 0
	for i, line := range lines {
		chars += utf8.RuneCountInString(line) + 1
		if maxChars > 0 && chars > maxChars {
			fmt.Fprintf(&b, "… outline cut: %d more lines; narrow it with find or allow more characters", len(lines)-i)
			return b.String(), true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n"), false
}
