// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/xuri/excelize/v2"
)

// ColumnType pins how a column's cells are typed.
type ColumnType string

// Column types accepted by types.
const (
	TypeString   ColumnType = "string"
	TypeNumber   ColumnType = "number"
	TypeInteger  ColumnType = "integer"
	TypeBoolean  ColumnType = "boolean"
	TypeDate     ColumnType = "date"
	TypeDateTime ColumnType = "datetime"
)

// ParseColumnType validates a types value.
func ParseColumnType(s string) (ColumnType, error) {
	switch t := ColumnType(strings.ToLower(strings.TrimSpace(s))); t {
	case TypeString, TypeNumber, TypeInteger, TypeBoolean, TypeDate, TypeDateTime:
		return t, nil
	case "bool":
		return TypeBoolean, nil
	case "int":
		return TypeInteger, nil
	default:
		return "", fmt.Errorf("unknown column type %q: use string, number, integer, boolean, date, or datetime", s)
	}
}

// FormulaMode selects what a formula cell yields.
type FormulaMode string

// Formula modes.
const (
	FormulaCached    FormulaMode = "cached"
	FormulaText      FormulaMode = "text"
	FormulaCalculate FormulaMode = "calculate"
)

const (
	dateLayout     = "2006-01-02"
	dateTimeLayout = "2006-01-02T15:04:05"
	timeLayout     = "15:04:05"
	maxExactInt    = 1 << 53
)

// rawValues asks excelize for stored values rather than text rendered
// through the cell's number format, so an evaluated formula stays a number.
var rawValues = excelize.Options{RawCellValue: true}

var excelErrors = map[string]bool{
	"#N/A": true, "#DIV/0!": true, "#VALUE!": true, "#REF!": true,
	"#NAME?": true, "#NUM!": true, "#NULL!": true, "#SPILL!": true,
	"#CALC!": true, "#GETTING_DATA": true,
}

// cellValue converts one raw cell into its typed value.
func (w *file) cellValue(sheet string, col, row int, raw string, opts ReadOptions, warn func(string)) (any, error) {
	cell := cellName(col, row)
	formula := ""
	if opts.Formulas == FormulaText || opts.Formulas == FormulaCalculate || raw == "" {
		formula, _ = w.f.GetCellFormula(sheet, cell)
	}
	if formula != "" && opts.Formulas == FormulaText {
		return "=" + formula, nil
	}
	if raw == "" {
		if formula == "" {
			return nil, nil
		}
		calculated, err := w.f.CalcCellValue(sheet, cell, rawValues)
		if err != nil {
			warn(fmt.Sprintf("%s!%s: formula %s could not be evaluated: %v", sheet, cell, formula, err))
			return nil, nil
		}
		warn(fmt.Sprintf("%s!%s: formula had no cached value; evaluated", sheet, cell))
		if calculated == "" {
			return nil, nil
		}
		if excelErrors[calculated] {
			warn(fmt.Sprintf("%s!%s: formula evaluated to %s", sheet, cell, calculated))
			return nil, nil
		}
		if f, err := strconv.ParseFloat(calculated, 64); err == nil {
			return w.numericValue(sheet, cell, f), nil
		}
		return w.text(calculated, opts), nil
	}
	if opts.Formulas == FormulaCalculate && formula != "" {
		if calculated, err := w.f.CalcCellValue(sheet, cell, rawValues); err == nil {
			raw = calculated
		}
	}
	cellType, err := w.f.GetCellType(sheet, cell)
	if err != nil {
		return nil, w.cellError(sheet, col, row, err.Error())
	}
	switch cellType {
	case excelize.CellTypeBool:
		return raw == "1" || strings.EqualFold(raw, "true"), nil
	case excelize.CellTypeError:
		warn(fmt.Sprintf("%s!%s: error cell %s", sheet, cell, raw))
		return nil, nil
	case excelize.CellTypeDate:
		return w.text(raw, opts), nil
	case excelize.CellTypeSharedString, excelize.CellTypeInlineString, excelize.CellTypeFormula:
		if excelErrors[raw] {
			warn(fmt.Sprintf("%s!%s: error cell %s", sheet, cell, raw))
			return nil, nil
		}
		return w.text(raw, opts), nil
	case excelize.CellTypeNumber, excelize.CellTypeUnset:
		if excelErrors[raw] {
			warn(fmt.Sprintf("%s!%s: error cell %s", sheet, cell, raw))
			return nil, nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return w.text(raw, opts), nil
		}
		return w.numericValue(sheet, cell, f), nil
	default:
		return w.text(raw, opts), nil
	}
}

// numericValue applies the cell's number format: dates become ISO strings,
// integral values become int64, and the rest stay float64.
func (w *file) numericValue(sheet, cell string, f float64) any {
	kind := kindNumber
	if styleID, err := w.f.GetCellStyle(sheet, cell); err == nil && styleID != 0 {
		kind = w.styleKind(styleID)
	}
	switch kind {
	case kindDate, kindDateTime, kindTime:
		t, err := excelize.ExcelDateToTime(f, w.date1904)
		if err != nil {
			return numberValue(f)
		}
		return formatTime(t, kind, f)
	case kindNumber:
		return numberValue(f)
	default:
		return numberValue(f)
	}
}

func formatTime(t time.Time, kind cellKind, serial float64) string {
	switch {
	case kind == kindTime:
		return t.Format(timeLayout)
	case kind == kindDateTime, serial != math.Trunc(serial):
		return t.Format(dateTimeLayout)
	default:
		return t.Format(dateLayout)
	}
}

func numberValue(f float64) any {
	if f == math.Trunc(f) && math.Abs(f) <= maxExactInt {
		return int64(f)
	}
	return f
}

func (w *file) text(s string, opts ReadOptions) string {
	if opts.Trim {
		return trimSpace(s)
	}
	return s
}

// trimSpace trims Unicode white space, which includes the ideographic space
// U+3000 common in Japanese workbooks.
func trimSpace(s string) string {
	return strings.TrimFunc(s, unicode.IsSpace)
}

// numericText returns the number a string denotes when it is written the
// canonical way: an optional leading minus, digits with no leading zero
// unless the integer part is 0, and an optional fraction of one or more
// digits. A value interpolated from a reference arrives as text, and this
// is how such a number comes back. Anything else, a plus sign, an
// exponent, a comma, white space, full-width digits, or leading zeros
// such as 007, is returned as it is, so codes stay text.
func numericText(s string) any {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	integer := s[start:i]
	if integer == "" || (len(integer) > 1 && integer[0] == '0') {
		return s
	}
	fraction := false
	if i < len(s) && s[i] == '.' {
		i++
		digits := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == digits {
			return s
		}
		fraction = true
	}
	if i != len(s) {
		return s
	}
	if !fraction {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	return f
}

// coerce applies a pinned column type to a typed cell value. date1904 says
// which epoch a numeric date serial counts from.
func coerce(v any, t ColumnType, date1904 bool) (any, error) {
	if v == nil {
		return nil, nil
	}
	switch t {
	case TypeString:
		return valueString(v), nil
	case TypeNumber:
		f, ok := toFloat(v)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("expected number, found %s", describe(v))
		}
		return numberValue(f), nil
	case TypeInteger:
		f, ok := toFloat(v)
		if !ok || f != math.Trunc(f) || f < -math.Exp2(63) || f >= math.Exp2(63) {
			return nil, fmt.Errorf("expected integer, found %s", describe(v))
		}
		return int64(f), nil
	case TypeBoolean:
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true", "yes", "1":
				return true, nil
			case "false", "no", "0":
				return false, nil
			}
		case int64:
			if x == 0 || x == 1 {
				return x == 1, nil
			}
		case float64:
			if x == 0 || x == 1 {
				return x == 1, nil
			}
		}
		return nil, fmt.Errorf("expected boolean, found %s", describe(v))
	case TypeDate, TypeDateTime:
		tm, ok := toTime(v, date1904)
		if !ok {
			return nil, fmt.Errorf("expected %s, found %s", t, describe(v))
		}
		if t == TypeDate {
			return tm.Format(dateLayout), nil
		}
		return tm.Format(dateTimeLayout), nil
	default:
		return v, nil
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		s := strings.ReplaceAll(strings.TrimSpace(x), ",", "")
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	default:
		return 0, false
	}
}

var timeLayouts = []string{
	dateTimeLayout, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04",
	dateLayout, "2006/01/02", "2006/1/2", "2006-1-2",
}

// toTime reads a time from a time, an ISO or slash-separated date string,
// or a date serial counted from the 1900 or 1904 epoch.
func toTime(v any, date1904 bool) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		s := strings.TrimSpace(x)
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	case int64, float64, int:
		f, _ := toFloat(v)
		t, err := excelize.ExcelDateToTime(f, date1904)
		return t, err == nil
	default:
		return time.Time{}, false
	}
}

func describe(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	default:
		return fmt.Sprint(v)
	}
}

// valueString renders a typed value the way a person would write it.
func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.Format(dateTimeLayout)
	default:
		return fmt.Sprint(v)
	}
}

// detectKind reports the kind a typed value would be written as.
func detectKind(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case bool:
		return string(TypeBoolean)
	case int64, int:
		return string(TypeInteger)
	case float64:
		return string(TypeNumber)
	case time.Time:
		return string(TypeDateTime)
	case string:
		if _, err := time.Parse(dateLayout, x); err == nil {
			return string(TypeDate)
		}
		if _, err := time.Parse(dateTimeLayout, x); err == nil {
			return string(TypeDateTime)
		}
		return string(TypeString)
	default:
		return string(TypeString)
	}
}
