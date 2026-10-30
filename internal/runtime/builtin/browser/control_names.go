// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"cmp"
	"slices"
)

// controlRoles are the roles of the controls of a form, which a form laid
// out in a table names by the label cells of its rows rather than by labels
// the tree knows.
var controlRoles = slices.Concat(fieldRoles, choiceRoles, toggleRoles, []string{fileInputRole})

// rowRoles and labelCellRoles are the roles of a table's rows and of the
// cells that may label a control, in tables of data and of layout alike.
var (
	rowRoles       = []string{"row", "LayoutTableRow"}
	labelCellRoles = append(slices.Clone(cellRoles), "LayoutTableCell")
)

// controlAttributesExpression returns, for each XPath of a JSON array
// substituted for %s, the name attribute of the element there, or else its
// id, or "". An XPath runs into a frame of the same origin as the browser
// runtime writes it, the frame's document following the frame element.
const controlAttributesExpression = `((xpaths) => xpaths.map((xpath) => {
	let doc = document, path = "", element = null;
	for (const step of xpath.split("/").filter(Boolean)) {
		if (element && (element.localName === "frame" || element.localName === "iframe")) {
			doc = element.contentDocument;
			path = "";
			if (!doc) return "";
		}
		path += "/" + step;
		element = doc.evaluate(path, doc, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue;
		if (!element) return "";
	}
	return (element && (element.getAttribute("name") || element.id)) || "";
}))(%s)`

// roots reads the tree of snap into its root nodes, naming each control
// the tree leaves unnamed by the label cell before it in its table row, or
// else by its name or id attribute.
func (snap pageSnapshot) roots() []*outlineNode {
	roots := parseSnapshotTree(snap.Tree)
	nameControls(roots, nil, snap.Attributes)
	return roots
}

// unnamedControls returns the IDs of the controls under nodes the tree
// leaves unnamed.
func unnamedControls(nodes []*outlineNode) []string {
	var ids []string
	for _, n := range nodes {
		if n.name == "" && slices.Contains(controlRoles, n.role) {
			ids = append(ids, n.id)
		}
		ids = append(ids, unnamedControls(n.children)...)
	}
	return ids
}

// nameControls names the unnamed controls under nodes, which sit in
// ancestors, by their row's label cell or else by attributes.
func nameControls(nodes, ancestors []*outlineNode, attributes map[string]string) {
	for _, n := range nodes {
		path := append(slices.Clip(ancestors), n)
		if n.name == "" && slices.Contains(controlRoles, n.role) {
			n.name = cmp.Or(rowLabel(path), attributes[n.id])
		}
		nameControls(n.children, path, attributes)
	}
}

// rowLabel returns the text of the cell that labels the last node of path:
// the nearest cell before the one holding it in its table row that has
// text and holds no control, in the nearest row that has one. It returns
// "" when no row does.
func rowLabel(path []*outlineNode) string {
	for i := len(path) - 2; i >= 0; i-- {
		if !slices.Contains(rowRoles, path[i].role) {
			continue
		}
		row := path[i]
		before := row.children[:slices.Index(row.children, path[i+1])]
		for _, cell := range slices.Backward(before) {
			if !slices.Contains(labelCellRoles, cell.role) || holdsControl(cell) {
				continue
			}
			if text := cmp.Or(cell.name, textOf(cell)); text != "" {
				return text
			}
		}
	}
	return ""
}

// holdsControl reports whether a control is under n.
func holdsControl(n *outlineNode) bool {
	return slices.ContainsFunc(n.children, func(child *outlineNode) bool {
		return slices.Contains(controlRoles, child.role) || holdsControl(child)
	})
}
