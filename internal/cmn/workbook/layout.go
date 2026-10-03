// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

// DataDirName is the directory under the Dagu data directory that holds
// what the xlsx actions keep between runs, such as extract recordings.
const DataDirName = "xlsx"

// DefaultMaxLayoutCells caps the cells a layout lists for a model.
const DefaultMaxLayoutCells = 2000

// DefaultMaxLayoutKB caps the size of the listing sent to a model, in KB, so
// a sheet that cannot fit a model context is refused before any request.
const DefaultMaxLayoutKB = 200

// maxLayoutText is the longest cell text a layout line carries, in runes.
const maxLayoutText = 200

// maxLayoutArea bounds the rectangle a layout walks, so two far-apart
// cells do not make a sheet of empty cells to visit.
const maxLayoutArea = 1_000_000

// LayoutOptions selects the sheet a layout describes and what it shows.
type LayoutOptions struct {
	Password string
	Sheet    string
	// Range limits the cells listed; empty lists the used range.
	Range string
	// SendValues lists the text of every cell; false lists non-text cells
	// by their kind alone, so amounts and dates stay on the host.
	SendValues bool
	Trim       bool
	Formulas   FormulaMode
	// MaxCells caps the listed cells; zero means DefaultMaxLayoutCells.
	MaxCells int
	// MaxKB caps the listing size in KB; zero means DefaultMaxLayoutKB.
	MaxKB int
}

// SheetLayout describes the non-empty cells of a sheet for a model, and
// identifies the sheet's shape so a layout seen before is recognized.
type SheetLayout struct {
	// Sheet is the resolved sheet name.
	Sheet string
	// Range is the listed area, such as Sheet1!A1:D12.
	Range string
	// Cells is how many cells are listed.
	Cells int
	// Listing is the text sent to the model, one cell per line.
	Listing string
	// Key identifies the shape of the sheet: which cells hold something,
	// of what kind and emphasis, and the merged regions. Cell text is not
	// part of it, so two forms of one template share a key.
	Key string
	// Labels maps the address of every text cell to its text, so a cached
	// answer can check that the label beside a cell still reads the same.
	Labels map[string]string
	// Addresses lists the top-left address of every listed cell, in the
	// order listed, so a cached answer can tell a sheet of the same
	// template with a box left blank from one that holds more.
	Addresses []string
}

// Layout lists the non-empty cells of a sheet the way a model is shown
// them: one line per cell as `B3 [text,bold]: 見積番号`, with the address,
// the kind the cell's type and number format give, the bold and fill
// hints, and the text cut to 200 characters. A merged region is listed once
// by its range, such as `A1:D1 [text,bold]: 御見積書`.
func Layout(ctx context.Context, path string, opts LayoutOptions) (*SheetLayout, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w, err := open(path, opts.Password)
	if err != nil {
		return nil, err
	}
	defer w.close()
	sheet, err := w.resolveSheet(opts.Sheet)
	if err != nil {
		return nil, err
	}
	var reg region
	if strings.TrimSpace(opts.Range) != "" {
		reg, err = w.parseRange(sheet, opts.Range)
		if err != nil {
			return nil, err
		}
		if reg, err = w.closeRegion(reg); err != nil {
			return nil, err
		}
		sheet = reg.Sheet
	} else if reg, err = w.usedRange(sheet); err != nil {
		return nil, err
	}
	if area := (reg.R2 - reg.R1 + 1) * (reg.C2 - reg.C1 + 1); area > maxLayoutArea {
		return nil, w.sheetError(sheet, fmt.Sprintf("%s spans more than %d cells; set range to the part of the sheet that holds the fields", reg.String(), maxLayoutArea))
	}
	grid, err := w.grid(sheet)
	if err != nil {
		return nil, err
	}
	merges, err := w.mergeMap(sheet)
	if err != nil {
		return nil, err
	}
	limit := opts.MaxCells
	if limit <= 0 {
		limit = DefaultMaxLayoutCells
	}
	maxKB := opts.MaxKB
	if maxKB <= 0 {
		maxKB = DefaultMaxLayoutKB
	}
	readOpts := ReadOptions{Trim: opts.Trim, Formulas: opts.Formulas}
	discard := func(string) {}

	layout := &SheetLayout{Sheet: sheet, Range: reg.String(), Labels: map[string]string{}}
	var listing strings.Builder
	shape := sha256.New()
	shape.Write([]byte(sheet + "\x00" + layout.Range + "\x00"))
	for _, m := range merges {
		shape.Write([]byte(m.String() + "\x00"))
	}
	for row := reg.R1; row <= reg.R2; row++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for col := reg.C1; col <= reg.C2; col++ {
			if oc, or := merges.origin(col, row); oc != col || or != row {
				// A covered cell shows its region's top-left value, which
				// is listed once, under the region.
				continue
			}
			raw := cellAt(grid, col, row)
			value, err := w.cellValue(sheet, col, row, raw, readOpts, discard)
			if err != nil {
				return nil, err
			}
			text := ""
			switch {
			case value != nil:
				text = valueString(value)
			case excelErrors[raw]:
				// An error cell is a cell all the same; the model sees
				// what Excel shows.
				text = raw
			default:
				continue
			}
			layout.Cells++
			if layout.Cells > limit {
				continue
			}
			addr := cellName(col, row)
			layout.Addresses = append(layout.Addresses, addr)
			span := addr
			for _, m := range merges {
				if m.C1 == col && m.R1 == row && (m.C2 > m.C1 || m.R2 > m.R1) {
					span = addr + ":" + cellName(m.C2, m.R2)
					break
				}
			}
			kind := w.layoutKind(sheet, col, row, raw, value)
			hints := w.layoutHints(sheet, col, row)
			shape.Write([]byte(span + "\x00" + kind + "\x00" + hints + "\x00"))
			text = layoutText(text)
			if kind == "text" {
				layout.Labels[addr] = text
			}
			listing.WriteString(span + " [" + kind + hints + "]")
			if kind == "text" || opts.SendValues {
				listing.WriteString(": " + cutText(text))
			}
			listing.WriteByte('\n')
		}
	}
	if layout.Cells > limit {
		return nil, w.sheetError(sheet, fmt.Sprintf("%d cells in %s is more than %d; set range to the part of the sheet that holds the fields", layout.Cells, layout.Range, limit))
	}
	layout.Listing = strings.TrimSuffix(listing.String(), "\n")
	if sizeKB := (len(layout.Listing) + 1023) / 1024; sizeKB > maxKB {
		return nil, w.sheetError(sheet, fmt.Sprintf("the listing of %s is %d KB, more than %d KB; set range to the part of the sheet that holds the fields", layout.Range, sizeKB, maxKB))
	}
	layout.Key = hex.EncodeToString(shape.Sum(nil))
	return layout, nil
}

// layoutKind names what a cell holds: text, number, date, datetime, time,
// or bool. A number under a date format reads back as a date string, so
// the cell's stored type and format decide, not the typed value alone.
func (w *file) layoutKind(sheet string, col, row int, raw string, value any) string {
	switch value.(type) {
	case bool:
		return "bool"
	case int64, float64:
		return "number"
	case string:
		if _, err := strconv.ParseFloat(raw, 64); err != nil {
			return "text"
		}
		cell := cellName(col, row)
		if t, err := w.f.GetCellType(sheet, cell); err != nil || (t != excelize.CellTypeNumber && t != excelize.CellTypeUnset && t != excelize.CellTypeFormula) {
			return "text"
		}
		id, err := w.f.GetCellStyle(sheet, cell)
		if err != nil || id == 0 {
			return "number"
		}
		switch w.styleKind(id) {
		case kindDate:
			return "date"
		case kindDateTime:
			return "datetime"
		case kindTime:
			return "time"
		case kindNumber:
			return "number"
		default:
			return "number"
		}
	default:
		return "text"
	}
}

// layoutHints names the emphasis a cell carries: ",bold" and ",fill", in
// that order, or nothing.
func (w *file) layoutHints(sheet string, col, row int) string {
	id, err := w.f.GetCellStyle(sheet, cellName(col, row))
	if err != nil || id == 0 {
		return ""
	}
	style, err := w.f.GetStyle(id)
	if err != nil || style == nil {
		return ""
	}
	hints := ""
	if style.Font != nil && style.Font.Bold {
		hints += ",bold"
	}
	if style.Fill.Pattern != 0 && len(style.Fill.Color) > 0 {
		hints += ",fill"
	}
	return hints
}

// layoutText flattens a cell's text to one line.
func layoutText(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	return trimSpace(s)
}

// cutText keeps the first maxLayoutText runes.
func cutText(s string) string {
	if utf8.RuneCountInString(s) <= maxLayoutText {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxLayoutText])
}
