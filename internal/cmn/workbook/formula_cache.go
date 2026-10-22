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
	"strings"

	"github.com/xuri/excelize/v2"
)

// invalidateFormulaCaches runs once when a writer saves changes. Every
// formula cache is discarded: references through names, tables and functions
// cannot be safely inferred from formula text. Formula groups also own cached
// cells that have no formula element of their own.
func (w *file) invalidateFormulaCaches() error {
	props, err := w.f.GetCalcProps()
	if err != nil {
		return err
	}
	full, completed := true, false
	props.FullCalcOnLoad, props.ForceFullCalc, props.CalcCompleted = &full, &full, &completed
	if err := w.f.SetCalcProps(&props); err != nil {
		return err
	}
	writer := w.f.ZipWriter
	w.f.SetZipWriter(func(out io.Writer) excelize.ZipWriter {
		return &formulaCacheZipWriter{ZipWriter: writer(out), workbook: w.f}
	})
	return nil
}

// The serialization hook keeps formula attributes, styles and metadata intact,
// and runs before Excelize encrypts a password-protected workbook. Only the
// current worksheet is buffered; other package parts pass through unchanged.
type formulaCacheZipWriter struct {
	excelize.ZipWriter
	workbook   *excelize.File
	parts      map[string]bool
	extensions map[string]bool
	part       *formulaCachePart
}

func (w *formulaCacheZipWriter) Create(name string) (io.Writer, error) {
	if w.parts == nil {
		if err := w.loadParts(); err != nil {
			return nil, err
		}
	}
	if err := w.flush(); err != nil {
		return nil, err
	}
	out, err := w.ZipWriter.Create(name)
	if err != nil {
		return nil, err
	}
	worksheet, override := w.parts[name]
	if !override {
		worksheet = w.extensions[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
	}
	if worksheet {
		w.part = &formulaCachePart{name: name, out: out}
		return &w.part.data, nil
	}
	return out, nil
}

// Excelize updates the package content types before its first Create call.
// Select by those types, not by a worksheet's arbitrary part URI or extension.
func (w *formulaCacheZipWriter) loadParts() error {
	content, ok := w.workbook.Pkg.Load("[Content_Types].xml")
	if !ok {
		return errors.New("invalidate formula caches: package content types are missing")
	}
	var types struct {
		XMLName  xml.Name `xml:"http://schemas.openxmlformats.org/package/2006/content-types Types"`
		Defaults []struct {
			Extension   string `xml:"Extension,attr"`
			ContentType string `xml:"ContentType,attr"`
		} `xml:"http://schemas.openxmlformats.org/package/2006/content-types Default"`
		Overrides []struct {
			PartName    string `xml:"PartName,attr"`
			ContentType string `xml:"ContentType,attr"`
		} `xml:"http://schemas.openxmlformats.org/package/2006/content-types Override"`
	}
	if err := xml.Unmarshal(content.([]byte), &types); err != nil {
		return fmt.Errorf("invalidate formula caches: read package content types: %w", err)
	}
	const worksheetType = "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"
	w.parts, w.extensions = map[string]bool{}, map[string]bool{}
	for _, part := range types.Overrides {
		w.parts[strings.TrimPrefix(part.PartName, "/")] = part.ContentType == worksheetType
	}
	for _, part := range types.Defaults {
		w.extensions[strings.ToLower(part.Extension)] = part.ContentType == worksheetType
	}
	return nil
}

func (w *formulaCacheZipWriter) flush() error {
	if w.part == nil {
		return nil
	}
	part := w.part
	w.part = nil
	data, err := clearFormulaCaches(part.data.Bytes())
	if err != nil {
		return fmt.Errorf("invalidate formula caches in %s: %w", part.name, err)
	}
	_, err = part.out.Write(data)
	return err
}

func (w *formulaCacheZipWriter) Close() error {
	return errors.Join(w.flush(), w.ZipWriter.Close())
}

type formulaCachePart struct {
	name string
	out  io.Writer
	data bytes.Buffer
}

func worksheetElement(name xml.Name) bool {
	return name.Local == "worksheet" && (name.Space == "http://schemas.openxmlformats.org/spreadsheetml/2006/main" ||
		name.Space == "http://purl.oclc.org/ooxml/spreadsheetml/main")
}

type cachedFormulaCell struct {
	Address string
	TagEnd  int
	Values  [][2]int
	Formula *struct {
		Ref string
	}
}

func (c *cachedFormulaCell) UnmarshalXML(decoder *xml.Decoder, start xml.StartElement) error {
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
			if element.Name.Space == start.Name.Space && element.Name.Local == "f" {
				c.Formula = &struct{ Ref string }{}
				for _, attr := range element.Attr {
					if attr.Name.Space == "" && attr.Name.Local == "ref" {
						c.Formula.Ref = attr.Value
					}
				}
			}
			if err := decoder.Skip(); err != nil {
				return err
			}
			if element.Name.Space == start.Name.Space && element.Name.Local == "v" {
				c.Values = append(c.Values, [2]int{pos, int(decoder.InputOffset())})
			}
		case xml.EndElement:
			return nil
		}
	}
}

// visitFormulaCells reads only worksheet cells, not similarly named elements in
// extensions. Offsets delimit the original XML so the cache edit need not
// re-encode namespaces, formula attributes or unrelated cell metadata.
func visitFormulaCells(data []byte, visit func(cachedFormulaCell, int, int) error) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth, sheetDataDepth, rowDepth := 0, 0, 0
	namespace := ""
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
			}
			if rowDepth == 0 || depth != rowDepth+1 || element.Name.Space != namespace || element.Name.Local != "c" {
				continue
			}
			var cell cachedFormulaCell
			if err := decoder.DecodeElement(&cell, &element); err != nil {
				return err
			}
			depth--
			if err := visit(cell, start, int(decoder.InputOffset())); err != nil {
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

func clearFormulaCaches(data []byte) ([]byte, error) {
	var groups []region
	hasFormula := false
	if err := visitFormulaCells(data, func(cell cachedFormulaCell, _, _ int) error {
		if cell.Formula == nil {
			return nil
		}
		hasFormula = true
		if cell.Formula.Ref == "" {
			return nil
		}
		start, end, found := strings.Cut(cell.Formula.Ref, ":")
		if !found {
			end = start
		}
		c1, r1, err := excelize.CellNameToCoordinates(start)
		if err != nil {
			return err
		}
		c2, r2, err := excelize.CellNameToCoordinates(end)
		if err != nil {
			return err
		}
		groups = append(groups, region{C1: min(c1, c2), R1: min(r1, r2), C2: max(c1, c2), R2: max(r1, r2)})
		return nil
	}); err != nil {
		return nil, err
	}
	if !hasFormula {
		return data, nil
	}
	var out bytes.Buffer
	last := 0
	err := visitFormulaCells(data, func(cell cachedFormulaCell, start, end int) error {
		clear := cell.Formula != nil
		if !clear && len(groups) > 0 {
			col, row, err := excelize.CellNameToCoordinates(cell.Address)
			if err != nil {
				return err
			}
			for _, group := range groups {
				if col >= group.C1 && col <= group.C2 && row >= group.R1 && row <= group.R2 {
					clear = true
					break
				}
			}
		}
		if !clear {
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
