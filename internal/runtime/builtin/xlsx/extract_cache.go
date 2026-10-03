// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/xuri/excelize/v2"
)

// extractEntry is what one extraction records: the cells the model named
// for a layout, and the label beside each, so a later run can tell the
// same form from one whose labels moved or were renamed.
type extractEntry struct {
	// Layout is the shape key of the sheet the answer was given for.
	Layout string `json:"layout"`
	// Instruction is the instruction the answer was given for.
	Instruction string `json:"instruction"`
	// Schema identifies the fields the answer was given for, with their
	// types and descriptions, so a changed meaning asks the model again.
	Schema string `json:"schema"`
	// Cells maps each field to the cell it was read from, as Sheet!B7, or
	// an empty string for a field the sheet lacked.
	Cells map[string]string `json:"cells"`
	// Anchors maps each field with a cell to the label beside that cell.
	Anchors map[string]anchor `json:"anchors,omitempty"`
}

// anchor is the text cell nearest to an answered cell, to its left in the
// same row or else above it in the same column, as it read when the model
// answered. Only a digest of the text is kept, so the cache file never
// holds what the sheet says.
type anchor struct {
	Cell   string `json:"cell"`
	Digest string `json:"digest"`
}

// labelDigest is how an anchor remembers a label's text.
func labelDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// extractCache stores the cells each extraction named, keyed by sheet
// layout, so a form in a layout seen before is read without a model call.
type extractCache = replaycache.Recordings[extractEntry]

func openExtractCache(dataDir, dagName, stepKey string) *extractCache {
	return replaycache.Open[extractEntry](replaycache.New(filepath.Join(dataDir, workbook.DataDirName)).Path(dagName, stepKey))
}

// anchorsFor finds the label beside each answered cell among the sheet's
// text cells: the nearest text cell to the left in the same row, else the
// nearest above in the same column. A cell with no label nearby has none.
func anchorsFor(cells map[string]string, labels map[string]string) map[string]anchor {
	anchors := map[string]anchor{}
	for field, ref := range cells {
		col, row, ok := cellCoordinates(ref)
		if !ok {
			continue
		}
		if a, found := nearestLabel(col, row, labels); found {
			anchors[field] = a
		}
	}
	return anchors
}

func nearestLabel(col, row int, labels map[string]string) (anchor, bool) {
	for c := col - 1; c >= 1; c-- {
		if name, ok := labelAt(labels, c, row); ok {
			return anchor{Cell: name, Digest: labelDigest(labels[name])}, true
		}
	}
	for r := row - 1; r >= 1; r-- {
		if name, ok := labelAt(labels, col, r); ok {
			return anchor{Cell: name, Digest: labelDigest(labels[name])}, true
		}
	}
	return anchor{}, false
}

func labelAt(labels map[string]string, col, row int) (string, bool) {
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		return "", false
	}
	_, ok := labels[name]
	return name, ok
}

// anchorsHold reports whether every anchor an entry recorded still reads
// the same text at the same cell.
func anchorsHold(anchors map[string]anchor, labels map[string]string) bool {
	for _, a := range anchors {
		if text, ok := labels[a.Cell]; !ok || labelDigest(text) != a.Digest {
			return false
		}
	}
	return true
}

// cellCoordinates reads the column and row of a Sheet!B7 or B7 reference.
func cellCoordinates(ref string) (col, row int, ok bool) {
	if i := strings.LastIndex(ref, "!"); i >= 0 {
		ref = ref[i+1:]
	}
	col, row, err := excelize.CellNameToCoordinates(strings.ReplaceAll(ref, "$", ""))
	if err != nil {
		return 0, 0, false
	}
	return col, row, true
}
