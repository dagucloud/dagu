// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/desktop"
)

// maxExtractOutline bounds the elements sent to the model beside the
// screenshot, so a window with a huge tree does not overrun the request.
const maxExtractOutline = 400

// extractMiss is the failure of an extract that may not call the model: a
// field it must read has no binding, or the binding no longer fits the
// screen. The binding is kept for a run that lets the model repair it.
type extractMiss struct {
	err error
	via string
}

func (m extractMiss) Error() string { return "the extract cannot run without AI: " + m.err.Error() }
func (m extractMiss) Unwrap() error { return m.err }

// bindableKind returns the column type a scalar schema property binds as,
// and whether it can bind at all. Objects and arrays never bind; a boolean
// reads a checkbox's state.
func bindableKind(property any) (workbook.ColumnType, bool) {
	prop, ok := property.(map[string]any)
	if !ok {
		return "", false
	}
	switch prop["type"] {
	case "string":
		return workbook.TypeString, true
	case "number":
		return workbook.TypeNumber, true
	case "integer":
		return workbook.TypeInteger, true
	case "boolean":
		return workbook.TypeBoolean, true
	default:
		return "", false
	}
}

// tableColumns returns the column type of each item property of an
// array-of-objects field, and whether the field binds to a table: an array
// whose items are objects of scalar properties. Its item properties are
// the table's columns.
func tableColumns(property any) (map[string]workbook.ColumnType, bool) {
	prop, ok := property.(map[string]any)
	if !ok || prop["type"] != "array" {
		return nil, false
	}
	items, ok := prop["items"].(map[string]any)
	if !ok || items["type"] != "object" {
		return nil, false
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok || len(itemProps) == 0 {
		return nil, false
	}
	columns := make(map[string]workbook.ColumnType, len(itemProps))
	for name, p := range itemProps {
		kind, ok := bindableKind(p)
		if !ok {
			return nil, false
		}
		columns[name] = kind
	}
	return columns, true
}

// readsFromElements reports whether a field can be read without the model:
// a scalar from one element, or a table of scalars from a container.
func readsFromElements(property any) bool {
	if _, ok := bindableKind(property); ok {
		return true
	}
	_, ok := tableColumns(property)
	return ok
}

// schemaProperties returns the top-level properties of an extract schema.
func schemaProperties(schema map[string]any) map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	return properties
}

// bindableFields names the schema properties an extract can read from the
// window's elements, scalar or table, in sorted order.
func bindableFields(schema map[string]any) []string {
	var names []string
	for name, property := range schemaProperties(schema) {
		if readsFromElements(property) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// unbindableFields names the schema properties an extract cannot read
// without the model: objects, and arrays that are not tables of scalars.
func unbindableFields(schema map[string]any) []string {
	var names []string
	for name, property := range schemaProperties(schema) {
		if !readsFromElements(property) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// readingSchema turns an extract's schema into the one the model answers
// when an extract binds: each bindable field becomes an object giving the
// value and the id of the element it was read from, so the run can bind it;
// every other field is passed through and read by the model alone.
func readingSchema(schema map[string]any) map[string]any {
	properties := schemaProperties(schema)
	reading := make(map[string]any, len(properties))
	for name, property := range properties {
		prop, _ := property.(map[string]any)
		if _, ok := bindableKind(property); ok {
			reading[name] = map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value":   map[string]any{"type": prop["type"]},
					"element": map[string]any{"type": "string", "description": "the id of the element the value was read from, or \"\" if read from the screen"},
				},
				"required": []any{"value", "element"},
			}
			continue
		}
		if _, ok := tableColumns(property); ok {
			reading[name] = tableReadingSchema(prop)
			continue
		}
		reading[name] = property
	}
	out := maps.Clone(schema)
	out["properties"] = reading
	return out
}

// tableReadingSchema is the object the model answers for a table field:
// the container to read, the column each item property came from, and the
// rows themselves, so the run can bind the container and verify the rows.
func tableReadingSchema(array map[string]any) map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"container": map[string]any{
				"type":        "object",
				"description": "the grid, table, or list the rows came from",
				"properties": map[string]any{
					"role": map[string]any{"type": "string"},
					"name": map[string]any{"type": "string"},
					"id":   map[string]any{"type": "string"},
				},
				"required": []any{"role"},
			},
			"columns": map[string]any{
				"type":                 "object",
				"description":          "for each item property, the column header it was read from",
				"additionalProperties": map[string]any{"type": "string"},
			},
			"rows": array,
		},
		"required": []any{"container", "columns", "rows"},
	}
}

// extractInstruction appends the window's elements to the instruction, so
// the model names the element each value came from, which tells a value it
// read from an element from one it inferred. An outline the host cannot
// read leaves the instruction as it is, and the extract reads by the model
// alone.
func (r *run) extractInstruction(instruction string, front desktop.Element, outline []desktop.Element) string {
	var lines []string
	for _, e := range outline {
		lines = append(lines, fmt.Sprintf("e%d\t%s\t%s\t%s", len(lines)+1, e.Role, e.Name, elementText(e)))
		if len(lines) >= maxExtractOutline {
			break
		}
	}
	if len(lines) == 0 {
		return instruction
	}
	return instruction + "\n\nThe window in front is " + front.Name +
		". Its elements are listed below as id, role, name, value, one per line. " +
		"For each field, set element to the id you read the value from, or \"\" if you read it from the screen.\n" +
		strings.Join(lines, "\n")
}

// elementText is the text an element shows: its value, or its name when it
// has no value, such as a static label.
func elementText(e desktop.Element) string {
	if e.Value != "" {
		return e.Value
	}
	return e.Name
}

// convertField converts text to a field's type, as a later run reads it.
func convertField(value any, kind workbook.ColumnType) (any, error) {
	return workbook.CoerceValue(value, kind)
}

// sameValue reports whether an element's text and the model's value are the
// same once converted to the field's type. Strings compare as a person
// reads them; numbers and integers compare by value.
func sameValue(elemText string, modelValue any, kind workbook.ColumnType) bool {
	ev, err := convertField(elemText, kind)
	if err != nil {
		return false
	}
	mv, err := convertField(modelValue, kind)
	if err != nil {
		return false
	}
	if kind == workbook.TypeString {
		es, _ := ev.(string)
		ms, _ := mv.(string)
		return desktop.NormalizeText(es) == desktop.NormalizeText(ms)
	}
	return ev == mv
}

// elementIndex returns the outline position a model source id names, such
// as "e3", or -1 when the id is empty, malformed, or out of range.
func elementIndex(source string, n int) int {
	num, err := strconv.Atoi(strings.TrimPrefix(source, "e"))
	if err != nil || num < 1 || num > n {
		return -1
	}
	return num - 1
}

// bindField binds a field to the element the model named, when that
// element's value is the field's value and no other element in the window
// gives that value, so the binding is both the element the model read and
// an unambiguous one. front names the application to record.
func (r *run) bindField(name string, kind workbook.ColumnType, modelValue any, named desktop.Element, outline []desktop.Element, front desktop.Element) (fieldBinding, any, bool) {
	if !sameValue(elementText(named), modelValue, kind) {
		return fieldBinding{}, nil, false
	}
	count := 0
	for i := range outline {
		if sameValue(elementText(outline[i]), modelValue, kind) {
			count++
		}
	}
	if count != 1 {
		return fieldBinding{}, nil, false
	}
	rec := r.recordElement(named, nil, front.App)
	if rec == nil {
		return fieldBinding{}, nil, false
	}
	// The bound run publishes the element's converted value, so it reads
	// the same as every later run that reads the binding.
	value, err := convertField(elementText(named), kind)
	if err != nil {
		return fieldBinding{}, nil, false
	}
	return fieldBinding{Name: name, Kind: string(kind), Element: *rec}, value, true
}

// unwrapAnswer turns a model answer under the reading schema into the
// values to publish: a scalar field's {value, element} becomes its value,
// a table field's {container, columns, rows} becomes its rows, and any
// other field is its own value. A model that answered the plain value
// anyway still publishes. schema is the original extract schema.
func unwrapAnswer(values map[string]any, schema map[string]any, wrapped bool) map[string]any {
	out := make(map[string]any, len(values))
	properties := schemaProperties(schema)
	for name, value := range values {
		inner, ok := value.(map[string]any)
		switch {
		case wrapped && ok && hasBindableAnswer(properties[name], inner):
			out[name] = inner["value"]
		case wrapped && ok && hasTableAnswer(properties[name], inner):
			out[name] = inner["rows"]
		default:
			out[name] = value
		}
	}
	return out
}

// hasBindableAnswer reports whether a scalar field's answer is the
// {value, element} object the reading schema asked for.
func hasBindableAnswer(property any, answer map[string]any) bool {
	if _, bindable := bindableKind(property); !bindable {
		return false
	}
	_, hasValue := answer["value"]
	_, hasElement := answer["element"]
	return hasValue && hasElement
}

// hasTableAnswer reports whether a table field's answer is the
// {container, columns, rows} object the reading schema asked for.
func hasTableAnswer(property any, answer map[string]any) bool {
	if _, ok := tableColumns(property); !ok {
		return false
	}
	_, hasContainer := answer["container"]
	_, hasRows := answer["rows"]
	return hasContainer && hasRows
}

// publishExtract writes an extract's values to the step outputs, keeping
// only the fields the schema lists, as the output names known when the DAG
// loaded.
func (r *run) publishExtract(schema map[string]any, values map[string]any) {
	properties, listed := schema["properties"].(map[string]any)
	for name, value := range values {
		if _, ok := properties[name]; listed && !ok {
			continue
		}
		r.outputs[name] = value
	}
}

// bindExtract binds every field the model read from the window: a scalar
// to the element it named, a table to its container. It returns the
// bindings and, for each bound field, the value read through the binding,
// which the model run publishes so it reads the same as every later run. A
// field the model read from the screen does not bind.
func (r *run) bindExtract(ctx context.Context, values map[string]any, schema map[string]any, outline []desktop.Element, front desktop.Element, els desktop.Elements, maxRows int) ([]fieldBinding, map[string]any) {
	var fields []fieldBinding
	published := map[string]any{}
	properties := schemaProperties(schema)
	for _, name := range bindableFields(schema) {
		answer, ok := values[name].(map[string]any)
		if !ok {
			continue
		}
		if kind, scalar := bindableKind(properties[name]); scalar {
			if binding, bound, ok := r.bindScalar(name, kind, answer, outline, front); ok {
				fields = append(fields, binding)
				published[name] = bound
			}
			continue
		}
		if columns, table := tableColumns(properties[name]); table {
			if binding, rows, ok := r.bindTable(ctx, name, columns, answer, els, front, maxRows); ok {
				fields = append(fields, binding)
				published[name] = rows
			}
		}
	}
	return fields, published
}

// bindScalar binds a scalar field to the element the model named in its
// {value, element} answer.
func (r *run) bindScalar(name string, kind workbook.ColumnType, answer map[string]any, outline []desktop.Element, front desktop.Element) (fieldBinding, any, bool) {
	source, _ := answer["element"].(string)
	index := elementIndex(source, len(outline))
	if index < 0 {
		return fieldBinding{}, nil, false
	}
	return r.bindField(name, kind, answer["value"], outline[index], outline, front)
}

// bindTable binds a table field to its container, when the rows read
// through the binding reproduce the rows the model returned, so a binding
// to the wrong container does not hold.
func (r *run) bindTable(ctx context.Context, name string, columns map[string]workbook.ColumnType, answer map[string]any, els desktop.Elements, front desktop.Element, maxRows int) (fieldBinding, []any, bool) {
	container, ok := selectorFromAnswer(answer["container"])
	if !ok {
		return fieldBinding{}, nil, false
	}
	headers, _ := answer["columns"].(map[string]any)
	modelRows, _ := answer["rows"].([]any)
	props := slices.Sorted(maps.Keys(columns))
	var cols []columnBinding
	var wanted []string
	for _, prop := range props {
		header, _ := headers[prop].(string)
		if header == "" {
			return fieldBinding{}, nil, false
		}
		cols = append(cols, columnBinding{Property: prop, Header: header, Kind: string(columns[prop])})
		wanted = append(wanted, header)
	}
	read, err := els.Rows(ctx, container, wanted, maxRows)
	if err != nil || !tableReproduces(read, modelRows, props, columns) {
		return fieldBinding{}, nil, false
	}
	values, err := rowsToValues(read, props, columns)
	if err != nil {
		return fieldBinding{}, nil, false
	}
	binding := fieldBinding{Name: name, Kind: "table", Table: &tableBinding{
		Container: container, App: front.App, Window: front.Window, Columns: cols,
	}}
	return binding, values, true
}

// selectorFromAnswer builds a selector from the model's container object.
func selectorFromAnswer(value any) (desktop.Selector, bool) {
	answer, ok := value.(map[string]any)
	if !ok {
		return desktop.Selector{}, false
	}
	role, _ := answer["role"].(string)
	if role == "" {
		return desktop.Selector{}, false
	}
	name, _ := answer["name"].(string)
	id, _ := answer["id"].(string)
	return desktop.Selector{Role: role, Name: name, ID: id}, true
}

// tableReproduces reports whether the rows read through a binding give the
// rows the model returned, each cell converted to its column's type. The
// read must hold at least the rows the model returned.
func tableReproduces(read [][]string, modelRows []any, props []string, columns map[string]workbook.ColumnType) bool {
	if len(modelRows) == 0 || len(read) < len(modelRows) {
		return false
	}
	for i, raw := range modelRows {
		row, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		for j, prop := range props {
			if !sameValue(read[i][j], row[prop], columns[prop]) {
				return false
			}
		}
	}
	return true
}

// rowsToValues converts a table read into step-output rows: one object per
// row, each property converted to its column's type. A cell that does not
// convert is an error, as a scalar field's would be, so a wrong-typed value
// is a miss, not published raw.
func rowsToValues(read [][]string, props []string, columns map[string]workbook.ColumnType) ([]any, error) {
	out := make([]any, 0, len(read))
	for i, row := range read {
		object := make(map[string]any, len(props))
		for j, prop := range props {
			value, err := convertField(row[j], columns[prop])
			if err != nil {
				return nil, fmt.Errorf("row %d column %q reads %q, which is not %s", i+1, prop, row[j], columns[prop])
			}
			object[prop] = value
		}
		out = append(out, object)
	}
	return out, nil
}

// readBinding reads every bound field from the screen now, converting each
// to its type. It returns the values, or the reason a field could not be
// read, looking again until findWithin passes.
func (r *run) readBinding(ctx context.Context, index int, els desktop.Elements, entry recording, findWithin time.Duration, maxRows int) (map[string]any, string, error) {
	values := make(map[string]any, len(entry.Fields))
	for _, field := range entry.Fields {
		if field.Table != nil {
			// A table is read straight from the front window, so a screen a
			// handler clears is cleared first.
			if _, err := r.clearScreens(ctx, index); err != nil {
				return nil, "", err
			}
			rows, reason, err := r.readTable(ctx, els, field, maxRows)
			if err != nil || reason != "" {
				return nil, reason, err
			}
			values[field.Name] = rows
			continue
		}
		found, reason, err := r.resolveElement(ctx, index, els, field.Element, findWithin)
		if err != nil {
			return nil, "", err
		}
		if reason != "" {
			return nil, fmt.Sprintf("field %q was not found: %s", field.Name, reason), nil
		}
		kind, err := workbook.ParseColumnType(field.Kind)
		if err != nil {
			return nil, fmt.Sprintf("field %q has an unknown type %q", field.Name, field.Kind), nil
		}
		value, err := convertField(elementText(found), kind)
		if err != nil {
			return nil, fmt.Sprintf("field %q reads %q, which is not %s", field.Name, elementText(found), field.Kind), nil
		}
		values[field.Name] = value
	}
	return values, "", nil
}

// readTable reads a bound table's rows, after the recorded application is
// in front, up to the current max rows so a changed setting takes effect. A
// container that is gone, a column header that moved away, or a cell that
// does not convert is a miss.
func (r *run) readTable(ctx context.Context, els desktop.Elements, field fieldBinding, maxRows int) ([]any, string, error) {
	tbl := field.Table
	front, err := els.FrontWindow()
	if errors.Is(err, desktop.ErrNoWindow) {
		return nil, fmt.Sprintf("table %q cannot be read: %v", field.Name, err), nil
	}
	if err != nil {
		return nil, "", err
	}
	if ok, reason := r.frontMatches(front, tbl.App, tbl.Window); !ok {
		return nil, fmt.Sprintf("table %q cannot be read: %s", field.Name, reason), nil
	}
	props := make([]string, len(tbl.Columns))
	headers := make([]string, len(tbl.Columns))
	columns := make(map[string]workbook.ColumnType, len(tbl.Columns))
	for i, column := range tbl.Columns {
		props[i] = column.Property
		headers[i] = column.Header
		kind, err := workbook.ParseColumnType(column.Kind)
		if err != nil {
			return nil, fmt.Sprintf("table %q column %q has an unknown type %q", field.Name, column.Property, column.Kind), nil
		}
		columns[column.Property] = kind
	}
	read, err := els.Rows(ctx, substituteSelector(tbl.Container, r.variables), headers, maxRows)
	switch {
	case errors.Is(err, desktop.ErrNotFound) || errors.Is(err, desktop.ErrNoWindow):
		return nil, fmt.Sprintf("table %q cannot be read: %s", field.Name, err.Error()), nil
	case err != nil:
		return nil, "", err
	}
	values, err := rowsToValues(read, props, columns)
	if err != nil {
		return nil, fmt.Sprintf("table %q cannot be read: %s", field.Name, err.Error()), nil
	}
	return values, "", nil
}

// boundNames lists the fields a recording binds, sorted.
func boundNames(entry recording) []string {
	names := make([]string, 0, len(entry.Fields))
	for _, field := range entry.Fields {
		names = append(names, field.Name)
	}
	slices.Sort(names)
	return names
}

// missingBindings names the bindable fields a recording does not bind, so a
// never extract can say what it cannot read.
func missingBindings(schema map[string]any, entry recording) []string {
	bound := map[string]bool{}
	for _, field := range entry.Fields {
		bound[field.Name] = true
	}
	var missing []string
	for _, name := range bindableFields(schema) {
		if !bound[name] {
			missing = append(missing, name)
		}
	}
	return missing
}
