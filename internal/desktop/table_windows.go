// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows && (amd64 || arm64)

package desktop

import (
	"context"
	"fmt"
	"slices"
)

// A grid, table, or list exposes its cells through the Grid and Table
// control patterns: Grid gives a cell by row and column and the counts,
// Table names the columns by their headers. Indexes come from
// UIAutomationClient.h in Windows SDK 10.0.26100, as in patterns_windows.go.

const (
	uiaGridPatternID  = 10006
	uiaTablePatternID = 10012
)

var (
	iidGridPattern  = mustGUID("{414C3CDC-856B-4F5B-8538-3131C6302550}")
	iidTablePattern = mustGUID("{620E691C-EA96-4710-A850-754B24CE2417}")
)

// IUIAutomationGridPattern and IUIAutomationTablePattern methods; IUnknown
// holds 0 to 2.
const (
	uiaGridGetItem                = 3
	uiaGridGetCurrentRowCount     = 4
	uiaTableGetCurrentColumnHeads = 4
)

// rowCount reads how many rows a grid has.
func (e *element) rowCount() (int, error) { return e.gridInt(uiaGridGetCurrentRowCount) }

func (e *element) gridInt(index int) (int, error) {
	p, err := e.patternAs(uiaGridPatternID, &iidGridPattern)
	if err != nil {
		return 0, err
	}
	defer p.release()
	var n int32
	if err := check("grid count", p.call(index, out(&n))); err != nil {
		return 0, err
	}
	return int(n), nil
}

// gridItem returns the element in a cell. The caller releases it.
func (e *element) gridItem(row, column int) (*element, error) {
	p, err := e.patternAs(uiaGridPatternID, &iidGridPattern)
	if err != nil {
		return nil, err
	}
	defer p.release()
	var cell *element
	if err := check("GetItem", p.call(uiaGridGetItem, uintptr(row), uintptr(column), out(&cell))); err != nil { //nolint:gosec // Row and column are small non-negative indexes.
		return nil, err
	}
	if cell == nil {
		return nil, fmt.Errorf("%w: the cell at row %d column %d is empty", ErrNotFound, row, column)
	}
	return cell, nil
}

// columnHeaders returns the text of a table's column headers, in order.
func (e *element) columnHeaders() ([]string, error) {
	p, err := e.patternAs(uiaTablePatternID, &iidTablePattern)
	if err != nil {
		return nil, err
	}
	defer p.release()
	var arr *elementArray
	if err := check("GetCurrentColumnHeaders", p.call(uiaTableGetCurrentColumnHeads, out(&arr))); err != nil {
		return nil, err
	}
	if arr == nil {
		return nil, nil
	}
	defer arr.release()
	n, err := arr.length()
	if err != nil {
		return nil, err
	}
	headers := make([]string, 0, n)
	for i := range n {
		head, err := arr.element(i)
		if err != nil {
			return nil, err
		}
		name, err := head.currentName()
		head.release()
		if err != nil {
			return nil, err
		}
		headers = append(headers, name)
	}
	return headers, nil
}

// cellText reads a cell's text: its value, or its name.
func (w *uiaWorker) cellText(cell *element) string {
	if text := w.value(cell); text != "" {
		return text
	}
	name, _ := cell.currentName()
	return name
}

// Rows reads a container's rows as text through its Grid and Table
// patterns: one []string per row, each cell the text under the column
// whose header names it, up to max rows. It reads on a thread of its own
// bounded by ctx, not the reader's five seconds, since a large table takes
// longer than one element read. A container with no grid, a column with no
// header, or a cell it cannot read is a miss.
func (e *uiaElements) Rows(ctx context.Context, container Selector, columns []string, maxRows int) ([][]string, error) {
	select {
	case <-e.quit:
		return nil, errElementsClosed
	default:
	}
	return readAlone(ctx, "rows", func(w *uiaWorker) ([][]string, error) {
		return w.readRows(ctx, container, columns, maxRows)
	})
}

// readRows resolves the container in the front window and reads its cells,
// stopping if ctx ends so an abandoned read does not keep calling the
// application.
func (w *uiaWorker) readRows(ctx context.Context, container Selector, columns []string, maxRows int) ([][]string, error) {
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
	matches, err := Match(top.window, tree.elements, container)
	if err != nil {
		return nil, err
	}
	found, err := One(matches, container)
	if err != nil {
		return nil, err
	}
	grid := nodeByPath(tree, found.Path)
	if grid == nil {
		return nil, fmt.Errorf("%w: the container is not there", ErrNotFound)
	}
	headers, err := grid.columnHeaders()
	if err != nil {
		return nil, fmt.Errorf("%q has no table columns: %w", container.Role, err)
	}
	indexes, err := columnIndexes(columns, headers)
	if err != nil {
		return nil, err
	}
	rows, err := grid.rowCount()
	if err != nil {
		return nil, err
	}
	out := make([][]string, 0, min(rows, maxRows))
	for r := 0; r < rows && r < maxRows; r++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]string, len(indexes))
		for i, column := range indexes {
			cell, err := grid.gridItem(r, column)
			if err != nil {
				return nil, err
			}
			row[i] = w.cellText(cell)
			cell.release()
		}
		out = append(out, row)
	}
	return out, nil
}

// nodeByPath returns the live element at a path in a read subtree.
func nodeByPath(tree *subtree, path []PathStep) *element {
	for _, node := range tree.nodes {
		if slices.Equal(node.path, path) {
			return node.el
		}
	}
	return nil
}
