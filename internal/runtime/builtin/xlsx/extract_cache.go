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
// for a layout, the labels it watches, and the cells the sheet listed, so
// a later run can tell the same form from one whose labels moved or were
// renamed, and a form of the same template with a box left blank from
// one that holds more.
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
	// Labels maps each watched label cell to a digest of its text: the
	// cells the model named as labels, and the label beside each answered
	// cell. Only digests are kept, so the cache file never holds what the
	// sheet says.
	Labels map[string]string `json:"labels,omitempty"`
	// Listed holds the addresses the sheet listed when the model answered,
	// so a sheet listing a cell this entry never saw is not served by it.
	Listed []string `json:"listed,omitempty"`
}

// labelDigest is how an entry remembers a label's text.
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

// labelsFor collects the label cells an entry watches: every cell the
// model named that is a text cell of the sheet, and the text cell nearest
// each answered cell, to its left in the same row or else above it in the
// same column.
func labelsFor(cells map[string]string, named []string, labels map[string]string) map[string]string {
	watched := map[string]string{}
	for _, ref := range named {
		if addr, ok := labelAddress(ref); ok {
			if text, found := labels[addr]; found {
				watched[addr] = labelDigest(text)
			}
		}
	}
	for _, ref := range cells {
		col, row, ok := cellCoordinates(ref)
		if !ok {
			continue
		}
		if addr, found := nearestLabel(col, row, labels); found {
			watched[addr] = labelDigest(labels[addr])
		}
	}
	return watched
}

// labelAddress reads a label the model named as the bare address of its
// cell: a sheet prefix and dollar signs are dropped, and so is the end of
// a merged span such as A1:D1, whose text lives in its top-left cell.
func labelAddress(ref string) (string, bool) {
	if i := strings.LastIndex(ref, "!"); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	col, row, ok := cellCoordinates(ref)
	if !ok {
		return "", false
	}
	name, err := excelize.CoordinatesToCellName(col, row)
	return name, err == nil
}

func nearestLabel(col, row int, labels map[string]string) (string, bool) {
	for c := col - 1; c >= 1; c-- {
		if name, ok := labelAt(labels, c, row); ok {
			return name, true
		}
	}
	for r := row - 1; r >= 1; r-- {
		if name, ok := labelAt(labels, col, r); ok {
			return name, true
		}
	}
	return "", false
}

func labelAt(labels map[string]string, col, row int) (string, bool) {
	name, err := excelize.CoordinatesToCellName(col, row)
	if err != nil {
		return "", false
	}
	_, ok := labels[name]
	return name, ok
}

// labelsHold reports whether every label an entry watches still reads
// the same text at the same cell.
func labelsHold(watched map[string]string, labels map[string]string) bool {
	for addr, digest := range watched {
		if text, ok := labels[addr]; !ok || labelDigest(text) != digest {
			return false
		}
	}
	return true
}

// listedCovers reports whether an entry saw every cell a sheet lists, so
// the sheet differs from the recorded one only by cells left blank.
func listedCovers(listed, addresses []string) bool {
	seen := make(map[string]bool, len(listed))
	for _, addr := range listed {
		seen[addr] = true
	}
	for _, addr := range addresses {
		if !seen[addr] {
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
