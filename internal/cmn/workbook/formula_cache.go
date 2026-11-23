// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package workbook

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// A writer records the cells it changes. On save, the zip writer hook holds
// back the worksheets that contain formulas, finds the formulas that can
// depend on a change, and clears only their cached values:
//
//	writer ──edits──► invalidateFormulaCaches ──► Excelize save
//	                                                   │ each package part
//	                                                   ▼
//	                                      formulaCacheZipWriter
//	                    worksheet without formulas ──► written as is
//	                    worksheet with formulas    ──► held until Close
//	Close: footprints ─► markDependents ─► clear their caches ─► write held parts

// cacheEdits holds the regions a writer changed, by folded sheet name.
type cacheEdits map[string][]region

// wholeSheet is every cell of a sheet.
var wholeSheet = region{C1: 1, R1: 1, C2: excelize.MaxColumns, R2: excelize.TotalRows}

// editCell records a cell whose value a writer changed.
func (w *file) editCell(sheet string, col, row int) {
	w.edit(sheet, region{C1: col, R1: row, C2: col, R2: row})
}

// editSheet records a sheet whose every cell may have changed: one that was
// added, replaced, renamed, or deleted.
func (w *file) editSheet(sheet string) {
	w.edit(sheet, wholeSheet)
}

func (w *file) edit(sheet string, reg region) {
	if w.edits == nil {
		w.edits = cacheEdits{}
	}
	key := sheetKey(sheet)
	reg.Sheet = key
	w.edits[key] = append(w.edits[key], reg)
}

// invalidateFormulaCaches runs before a writer saves. When the writer
// changed something, the saved workbook asks to be recalculated on open,
// and the formulas that can depend on a change lose their cached values,
// so a read evaluates them instead of returning a stale result.
func (w *file) invalidateFormulaCaches() error {
	if len(w.edits) == 0 {
		return nil
	}
	props, err := w.f.GetCalcProps()
	if err != nil {
		return err
	}
	full, completed := true, false
	props.FullCalcOnLoad, props.CalcCompleted = &full, &completed
	if err := w.f.SetCalcProps(&props); err != nil {
		return err
	}
	writer := w.f.ZipWriter
	edits, names := w.edits, w.f.GetDefinedName()
	w.f.SetZipWriter(func(out io.Writer) excelize.ZipWriter {
		return &formulaCacheZipWriter{ZipWriter: writer(out), workbook: w.f, edits: edits, names: names}
	})
	return nil
}

// formulaCacheZipWriter is the save hook. It runs before Excelize encrypts
// a password-protected workbook.
type formulaCacheZipWriter struct {
	excelize.ZipWriter
	workbook *excelize.File
	edits    cacheEdits
	names    []excelize.DefinedName
	// layout is read at the first Create: Excelize stores the workbook and
	// its relationships in the package before it writes any part.
	layout *bookLayout
	part   *heldPart
	held   []*heldPart
}

// heldPart is a worksheet as Excelize wrote it, and what the hook does to it.
type heldPart struct {
	name     string
	sheet    string
	data     bytes.Buffer
	formulas []formulaCell
	clear    map[[2]int]bool
	groups   []region
}

func (w *formulaCacheZipWriter) Create(name string) (io.Writer, error) {
	if w.layout == nil {
		layout, err := readBookLayout(w.workbook)
		if err != nil {
			return nil, err
		}
		w.layout = layout
	}
	if err := w.settle(); err != nil {
		return nil, err
	}
	if sheet, ok := w.layout.sheets[name]; ok {
		w.part = &heldPart{name: name, sheet: sheet}
		return &w.part.data, nil
	}
	return w.ZipWriter.Create(name)
}

// settle writes the worksheet just completed when it has no formula and
// holds it otherwise, since a formula on any sheet may read a change.
func (w *formulaCacheZipWriter) settle() error {
	part := w.part
	if part == nil {
		return nil
	}
	w.part = nil
	formulas, err := readFormulas(part.data.Bytes())
	if err != nil {
		return fmt.Errorf("invalidate formula caches in %s: %w", part.name, err)
	}
	if len(formulas) == 0 {
		return w.write(part.name, part.data.Bytes())
	}
	part.formulas = formulas
	w.held = append(w.held, part)
	return nil
}

func (w *formulaCacheZipWriter) Close() error {
	err := w.settle()
	if err == nil {
		err = w.writeHeld()
	}
	return errors.Join(err, w.ZipWriter.Close())
}

func (w *formulaCacheZipWriter) writeHeld() error {
	resolver := &refResolver{names: w.names, tables: w.layout.tables}
	nodes := formulaNodes(w.held, resolver)
	dirty := dirtyCells{}
	for _, regs := range w.edits {
		for _, reg := range regs {
			dirty.add(reg)
		}
	}
	markDependents(nodes, dirty)
	for _, n := range nodes {
		if !n.cleared {
			continue
		}
		if n.part.clear == nil {
			n.part.clear = map[[2]int]bool{}
		}
		n.part.clear[[2]int{n.col, n.row}] = true
		if n.group.Sheet != "" {
			n.part.groups = append(n.part.groups, n.group)
		}
	}
	for _, part := range w.held {
		data := part.data.Bytes()
		if len(part.clear) > 0 {
			var err error
			if data, err = clearCaches(data, part.clear, part.groups); err != nil {
				return fmt.Errorf("invalidate formula caches in %s: %w", part.name, err)
			}
		}
		if err := w.write(part.name, data); err != nil {
			return err
		}
	}
	return nil
}

func (w *formulaCacheZipWriter) write(name string, data []byte) error {
	out, err := w.ZipWriter.Create(name)
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

// formulaNodes reads the footprint of every held formula. A shared formula
// that carries no text of its own reads what its group's first formula
// reads, shifted to its cell; a data table is unknown.
func formulaNodes(parts []*heldPart, resolver *refResolver) []*formulaNode {
	var nodes []*formulaNode
	for _, part := range parts {
		masters := map[string]formulaCell{}
		for _, f := range part.formulas {
			if f.kind == "shared" && f.ref != "" && f.text != "" {
				masters[f.shared] = f
			}
		}
		parsed := map[string]formulaRefs{}
		for _, f := range part.formulas {
			n := &formulaNode{part: part, col: f.col, row: f.row}
			switch {
			case f.kind == "dataTable":
				n.unknown = true
			case f.kind == "shared" && f.text == "":
				master, ok := masters[f.shared]
				if !ok {
					n.unknown = true
					break
				}
				refs, ok := parsed[f.shared]
				if !ok {
					refs = parseFormulaRefs(master.text)
					parsed[f.shared] = refs
				}
				n.deps, n.unknown = resolver.footprint(refs, part.sheet, f.col, f.row, f.col-master.col, f.row-master.row)
			default:
				n.deps, n.unknown = resolver.footprint(parseFormulaRefs(f.text), part.sheet, f.col, f.row, 0, 0)
			}
			if f.kind == "array" || f.kind == "dataTable" {
				n.group = groupRange(f.ref, part.sheet)
			}
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// groupRange is the range an array or data-table formula fills, when it is
// more than one cell; otherwise its Sheet is empty.
func groupRange(ref, sheet string) region {
	start, end, found := strings.Cut(ref, ":")
	if !found {
		return region{}
	}
	c1, r1, err := excelize.CellNameToCoordinates(start)
	if err != nil {
		return region{}
	}
	c2, r2, err := excelize.CellNameToCoordinates(end)
	if err != nil || c1 == c2 && r1 == r2 {
		return region{}
	}
	return region{Sheet: sheetKey(sheet), C1: min(c1, c2), R1: min(r1, r2), C2: max(c1, c2), R2: max(r1, r2)}
}

// bookLayout is what the analysis reads from the package: the sheet each
// worksheet part holds, and each table's range.
type bookLayout struct {
	sheets map[string]string
	tables []bookTable
}

// bookTable is a table by the name formulas use; reg.Sheet is folded.
type bookTable struct {
	name string
	reg  region
}

type packageRels struct {
	Rels []struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	} `xml:"Relationship"`
}

// readBookLayout follows the package relationships rather than part names,
// which a workbook may choose freely.
func readBookLayout(f *excelize.File) (*bookLayout, error) {
	bookPart := "xl/workbook.xml"
	if rels, ok := loadRels(f, "_rels/.rels"); ok {
		for _, rel := range rels.Rels {
			if strings.HasSuffix(rel.Type, "/officeDocument") {
				bookPart = partPath("", rel.Target)
				break
			}
		}
	}
	data, ok := loadPart(f, bookPart)
	if !ok {
		return nil, fmt.Errorf("invalidate formula caches: workbook part %s is missing", bookPart)
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(data, &book); err != nil {
		return nil, fmt.Errorf("invalidate formula caches: read %s: %w", bookPart, err)
	}
	rels, _ := loadRels(f, relsPath(bookPart))
	layout := &bookLayout{sheets: map[string]string{}}
	for _, sheet := range book.Sheets {
		for _, rel := range rels.Rels {
			if rel.ID == sheet.ID && strings.HasSuffix(rel.Type, "/worksheet") {
				part := partPath(path.Dir(bookPart), rel.Target)
				layout.sheets[part] = sheet.Name
				layout.tables = append(layout.tables, readTables(f, part, sheet.Name)...)
			}
		}
	}
	return layout, nil
}

func readTables(f *excelize.File, sheetPart, sheet string) []bookTable {
	rels, _ := loadRels(f, relsPath(sheetPart))
	var tables []bookTable
	for _, rel := range rels.Rels {
		if !strings.HasSuffix(rel.Type, "/table") {
			continue
		}
		data, ok := loadPart(f, partPath(path.Dir(sheetPart), rel.Target))
		if !ok {
			continue
		}
		var table struct {
			Name        string `xml:"name,attr"`
			DisplayName string `xml:"displayName,attr"`
			Ref         string `xml:"ref,attr"`
		}
		if xml.Unmarshal(data, &table) != nil {
			continue
		}
		reg := groupRange(table.Ref, sheet)
		if reg.Sheet == "" {
			continue
		}
		name := table.DisplayName
		if name == "" {
			name = table.Name
		}
		tables = append(tables, bookTable{name: name, reg: reg})
	}
	return tables
}

func loadPart(f *excelize.File, name string) ([]byte, bool) {
	content, ok := f.Pkg.Load(name)
	if !ok {
		return nil, false
	}
	data, ok := content.([]byte)
	return data, ok
}

func loadRels(f *excelize.File, name string) (packageRels, bool) {
	var rels packageRels
	data, ok := loadPart(f, name)
	if !ok || xml.Unmarshal(data, &rels) != nil {
		return packageRels{}, false
	}
	return rels, true
}

func relsPath(part string) string {
	return path.Join(path.Dir(part), "_rels", path.Base(part)+".rels")
}

// partPath resolves a relationship target: absolute from the package root,
// or relative to the directory of the part that holds the relationship.
func partPath(dir, target string) string {
	if strings.HasPrefix(target, "/") {
		return strings.TrimPrefix(target, "/")
	}
	return path.Join(dir, target)
}

// formulaCell is a cell's formula element as the worksheet stores it.
type formulaCell struct {
	col, row int
	kind     string // "", "shared", "array", or "dataTable"
	ref      string
	shared   string // the si of a shared formula
	text     string
}

// readFormulas returns the formula cells of a worksheet. A part with no
// element named f, with or without a prefix, is not parsed.
func readFormulas(data []byte) ([]formulaCell, error) {
	if !bytes.Contains(data, []byte("<f")) && !bytes.Contains(data, []byte(":f")) {
		return nil, nil
	}
	var formulas []formulaCell
	err := visitCells(data, func(cell cachedCell, col, row, _, _ int) error {
		if f := cell.Formula; f != nil {
			formulas = append(formulas, formulaCell{col: col, row: row, kind: f.Type, ref: f.Ref, shared: f.Shared, text: f.Text})
		}
		return nil
	})
	return formulas, err
}

func worksheetElement(name xml.Name) bool {
	return name.Local == "worksheet" && (name.Space == "http://schemas.openxmlformats.org/spreadsheetml/2006/main" ||
		name.Space == "http://purl.oclc.org/ooxml/spreadsheetml/main")
}

// cachedCell is a worksheet cell with the offsets of its start tag's end
// and of its cached values in the original XML.
type cachedCell struct {
	Address string
	TagEnd  int
	Values  [][2]int
	Formula *struct {
		Ref, Type, Shared, Text string
	}
}

func (c *cachedCell) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
	c.TagEnd = int(decoder.InputOffset())
	for _, attr := range start.Attr {
		if attr.Name.Space == "" && attr.Name.Local == "r" {
			c.Address = attr.Value
		}
	}
	for {
		pos := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Space != start.Name.Space || element.Name.Local != "f" {
				if err := decoder.Skip(); err != nil {
					return err
				}
				if element.Name.Space == start.Name.Space && element.Name.Local == "v" {
					c.Values = append(c.Values, [2]int{pos, int(decoder.InputOffset())})
				}
				continue
			}
			c.Formula = &struct{ Ref, Type, Shared, Text string }{}
			for _, attr := range element.Attr {
				if attr.Name.Space != "" {
					continue
				}
				switch attr.Name.Local {
				case "ref":
					c.Formula.Ref = attr.Value
				case "t":
					c.Formula.Type = attr.Value
				case "si":
					c.Formula.Shared = attr.Value
				}
			}
			var body struct {
				Text string `xml:",chardata"`
			}
			if err := decoder.DecodeElement(&body, &element); err != nil {
				return err
			}
			c.Formula.Text = body.Text
		case xml.EndElement:
			return nil
		}
	}
}

// visitCells reads only worksheet cells, not similarly named elements in
// extensions, and passes each with its column, row, and offsets in data. A
// cell or row without an r attribute follows the one before it.
func visitCells(data []byte, visit func(cell cachedCell, col, row, start, end int) error) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, sheetDataDepth, rowDepth := 0, 0, 0
	namespace := ""
	col, row := 0, 0
	for {
		start := int(decoder.InputOffset())
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				if !worksheetElement(element.Name) {
					return nil
				}
				namespace = element.Name.Space
			}
			if depth == 2 && element.Name.Space == namespace && element.Name.Local == "sheetData" {
				sheetDataDepth = depth
			}
			if sheetDataDepth > 0 && depth == sheetDataDepth+1 && element.Name.Space == namespace && element.Name.Local == "row" {
				rowDepth = depth
				col, row = 0, nextRow(element, row)
			}
			if rowDepth == 0 || depth != rowDepth+1 || element.Name.Space != namespace || element.Name.Local != "c" {
				continue
			}
			var cell cachedCell
			if err := decoder.DecodeElement(&cell, &element); err != nil {
				return err
			}
			depth--
			col++
			if cell.Address != "" {
				if col, row, err = excelize.CellNameToCoordinates(cell.Address); err != nil {
					return err
				}
			}
			if err := visit(cell, col, row, start, int(decoder.InputOffset())); err != nil {
				return err
			}
		case xml.EndElement:
			if depth == rowDepth {
				rowDepth = 0
			}
			if depth == sheetDataDepth {
				sheetDataDepth = 0
			}
			depth--
		}
	}
}

func nextRow(element xml.StartElement, previous int) int {
	for _, attr := range element.Attr {
		if attr.Name.Space == "" && attr.Name.Local == "r" {
			if n, err := strconv.Atoi(attr.Value); err == nil {
				return n
			}
		}
	}
	return previous + 1
}

// clearCaches removes the cached values of the formula cells in clear and of
// the cells inside groups, the ranges that cleared array and data-table
// formulas fill. Offsets delimit the original XML, so namespaces, formula
// attributes, and other cell metadata are not re-encoded.
func clearCaches(data []byte, clear map[[2]int]bool, groups []region) ([]byte, error) {
	var out bytes.Buffer
	last := 0
	err := visitCells(data, func(cell cachedCell, col, row, start, end int) error {
		if cell.Formula != nil && !clear[[2]int{col, row}] ||
			cell.Formula == nil && !slices.ContainsFunc(groups, func(g region) bool { return g.contains(col, row) }) {
			return nil
		}
		out.Write(data[last:start])
		out.Write(withoutCachedCellType(data[start:cell.TagEnd]))
		pos := cell.TagEnd
		for _, span := range cell.Values {
			out.Write(data[pos:span[0]])
			pos = span[1]
		}
		out.Write(data[pos:end])
		last = end
		return nil
	})
	if err != nil {
		return nil, err
	}
	if last == 0 {
		return data, nil
	}
	out.Write(data[last:])
	return out.Bytes(), nil
}

var cellAttributes = regexp.MustCompile(`[\t\n\r ]+([^\s=/>]+)\s*=\s*(?:"[^"]*"|'[^']*')`)

func withoutCachedCellType(tag []byte) []byte {
	// Match complete attributes so text resembling t= inside another
	// attribute's quoted value is not removed.
	for _, match := range cellAttributes.FindAllSubmatchIndex(tag, -1) {
		if string(tag[match[2]:match[3]]) == "t" {
			return append(bytes.Clone(tag[:match[0]]), tag[match[1]:]...)
		}
	}
	return tag
}
