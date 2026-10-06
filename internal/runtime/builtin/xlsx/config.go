// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/jsonschema-go/jsonschema"
)

// config holds every with field of the xlsx actions. Which fields an
// operation accepts is checked in validateConfig.
type config struct {
	Path          string            `mapstructure:"path"`
	Password      string            `mapstructure:"password"`
	Sheet         string            `mapstructure:"sheet"`
	Range         string            `mapstructure:"range"`
	Header        any               `mapstructure:"header"`
	Columns       any               `mapstructure:"columns"`
	Merged        string            `mapstructure:"merged"`
	StopAtBlank   bool              `mapstructure:"stop_at_blank"`
	KeepEmptyRows bool              `mapstructure:"keep_empty_rows"`
	SkipHidden    bool              `mapstructure:"skip_hidden"`
	Trim          bool              `mapstructure:"trim"`
	Formulas      string            `mapstructure:"formulas"`
	Types         map[string]string `mapstructure:"types"`
	OnTypeError   string            `mapstructure:"on_type_error"`
	Where         map[string]any    `mapstructure:"where"`
	MaxRows       int               `mapstructure:"max_rows"`
	Rows          any               `mapstructure:"rows"`
	Input         string            `mapstructure:"input"`
	Format        string            `mapstructure:"format"`
	Mode          string            `mapstructure:"mode"`
	Style         string            `mapstructure:"style"`
	Atomic        bool              `mapstructure:"atomic"`
	DryRun        bool              `mapstructure:"dry_run"`
	WaitForUnlock string            `mapstructure:"wait_for_unlock"`
	Key           string            `mapstructure:"key"`
	Set           map[string]any    `mapstructure:"set"`
	Missing       string            `mapstructure:"missing"`
	Artifact      bool              `mapstructure:"artifact"`
	// validate
	Required    []string       `mapstructure:"required"`
	NotBlank    []string       `mapstructure:"not_blank"`
	Unique      []string       `mapstructure:"unique"`
	Allowed     map[string]any `mapstructure:"allowed"`
	OnProblem   string         `mapstructure:"on_problem"`
	MaxProblems int            `mapstructure:"max_problems"`
	// write_cells and convert
	Cells  map[string]any `mapstructure:"cells"`
	Merge  []string       `mapstructure:"merge"`
	Output string         `mapstructure:"output"`
	// sheet
	Operation string `mapstructure:"operation"`
	To        string `mapstructure:"to"`
	IfExists  string `mapstructure:"if_exists"`
	Position  int    `mapstructure:"position"`
	// csv files: input of write and append, output of convert
	Encoding  string `mapstructure:"encoding"`
	Delimiter string `mapstructure:"delimiter"`
	// extract
	Instruction string         `mapstructure:"instruction"`
	Schema      map[string]any `mapstructure:"schema"`
	SendValues  bool           `mapstructure:"send_values"`
	Cache       bool           `mapstructure:"cache"`
	// LLM is the model block xlsx.extract takes through with.llm; the spec
	// layer moves it to the step before the executor sees it, so here it is
	// only a key that is foreign to every other operation.
	LLM any `mapstructure:"llm"`

	// Parsed forms, filled by validateConfig.
	// extractProperties lists the schema's properties in order and
	// extractTypes the column type each one pins, when it pins one.
	extractProperties []string
	extractTypes      map[string]workbook.ColumnType
	header            workbook.HeaderSpec
	columns           []workbook.ColumnSelect
	types             map[string]workbook.ColumnType
	set               map[string]workbook.SetValue
	wait              time.Duration
	allowed           map[string][]any
	cells             map[string]workbook.CellValue
	convertFormat     workbook.ConvertFormat
	encoding          workbook.Encoding
	delimiter         rune
	present           map[string]bool
	// deferred lists fields whose value is still a reference at build time.
	deferred map[string]bool
}

// provided reports whether a field has a usable value now: it is present
// and not deferred to the run.
func (cfg config) provided(field string) bool {
	return cfg.present[field] && !cfg.deferred[field]
}

func defaultConfig() config {
	return config{Atomic: true, SendValues: true, Cache: true}
}

// decodeConfig fills cfg from a with map. At DAG build time, deferReferences
// is true and a field whose whole value is still a value reference such as
// ${params.DRY_RUN} is left for the run, when it has been resolved; it is
// recorded in deferred so validation skips it. Unknown fields are rejected
// either way.
func decodeConfig(raw map[string]any, cfg *config, deferReferences bool) error {
	if raw == nil {
		raw = map[string]any{}
	}
	cfg.present = make(map[string]bool, len(raw))
	cfg.deferred = map[string]bool{}
	input := make(map[string]any, len(raw))
	for key, v := range raw {
		cfg.present[key] = true
		if deferReferences && holdsReference(v) {
			cfg.deferred[key] = true
			continue
		}
		input[key] = v
	}
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      true,
		TagName:          "mapstructure",
	})
	if err != nil {
		return err
	}
	if err := decoder.Decode(input); err != nil {
		return fmt.Errorf("%w: %v", errConfig, err)
	}
	return nil
}

// holdsReference reports whether a with value, or any string nested in a
// map or list value such as types or where, is still a value reference.
// Such a field is checked at run time, once the reference has a value.
func holdsReference(v any) bool {
	switch x := v.(type) {
	case string:
		return value.HasValueReference(x)
	case map[string]any:
		for _, item := range x {
			if holdsReference(item) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(x, holdsReference)
	}
	return false
}

// fieldsByOperation lists the with fields each operation accepts.
var fieldsByOperation = map[string][]string{
	opRead: {"path", "password", "sheet", "range", "header", "columns", "merged", "stop_at_blank",
		"keep_empty_rows", "skip_hidden", "trim", "formulas", "types", "on_type_error", "where", "max_rows"},
	opInfo:       {"path", "password"},
	opListSheets: {"path", "password"},
	opWrite: {"path", "password", "sheet", "rows", "input", "format", "encoding", "delimiter", "columns", "header",
		"mode", "style", "types", "atomic", "dry_run", "wait_for_unlock", "artifact"},
	opAppend: {"path", "password", "sheet", "rows", "input", "format", "encoding", "delimiter", "columns", "header",
		"types", "atomic", "dry_run", "wait_for_unlock", "artifact"},
	opUpdateRows: {"path", "password", "sheet", "header", "rows", "key", "set", "missing", "atomic",
		"dry_run", "wait_for_unlock", "artifact"},
	opValidate: {"path", "password", "sheet", "range", "header", "columns", "merged", "skip_hidden", "trim", "formulas",
		"required", "not_blank", "unique", "types", "allowed", "on_problem", "max_problems"},
	opWriteCells: {"path", "password", "sheet", "cells", "merge", "output", "atomic", "dry_run", "wait_for_unlock", "artifact"},
	opSheet: {"path", "password", "operation", "sheet", "to", "if_exists", "missing", "position", "atomic",
		"dry_run", "wait_for_unlock", "artifact"},
	opConvert: {"path", "password", "sheet", "range", "header", "columns", "types", "trim", "merged", "formulas",
		"skip_hidden", "output", "format", "encoding", "delimiter", "atomic", "artifact"},
	opExtract: {"path", "password", "sheet", "range", "instruction", "schema", "send_values", "cache", "trim", "formulas"},
}

// extractFixedOutputs are the outputs xlsx.extract publishes beside the
// schema's properties, which a property may not be named after.
var extractFixedOutputs = []string{"cells", "sheet", "warnings", "source"}

func isWriter(operation string) bool {
	switch operation {
	case opWrite, opAppend, opUpdateRows, opWriteCells, opSheet, opConvert:
		return true
	default:
		return false
	}
}

func validateConfig(operation string, cfg *config) error {
	allowed, ok := fieldsByOperation[operation]
	if !ok {
		return fmt.Errorf("%w: unsupported operation %q", errConfig, operation)
	}
	if err := rejectForeignFields(operation, cfg.present, allowed); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Path) == "" && !cfg.deferred["path"] {
		return fmt.Errorf("%w: path is required for %s", errConfig, operation)
	}
	var err error
	if cfg.header, err = workbook.ParseHeader(cfg.Header); err != nil {
		return fmt.Errorf("%w: %v", errConfig, err)
	}
	if cfg.columns, err = workbook.ParseColumns(cfg.Columns); err != nil {
		return fmt.Errorf("%w: %v", errConfig, err)
	}
	switch cfg.Merged {
	case "", string(workbook.MergedFill), string(workbook.MergedFirst):
	default:
		return fmt.Errorf("%w: merged must be fill or first", errConfig)
	}
	switch cfg.Formulas {
	case "", string(workbook.FormulaCached), string(workbook.FormulaText), string(workbook.FormulaCalculate):
	default:
		return fmt.Errorf("%w: formulas must be cached, text, or calculate", errConfig)
	}
	// YAML parses an unquoted null as nil, so a present but empty value is
	// the null spelling of warn.
	if cfg.provided("on_type_error") && cfg.OnTypeError == "" {
		cfg.OnTypeError = string(workbook.TypeErrorWarn)
	}
	switch cfg.OnTypeError {
	case "", string(workbook.TypeErrorFail), string(workbook.TypeErrorWarn), string(workbook.TypeErrorNull):
	default:
		return fmt.Errorf("%w: on_type_error must be fail, warn, or null", errConfig)
	}
	if cfg.MaxRows < 0 {
		return fmt.Errorf("%w: max_rows must be >= 1", errConfig)
	}
	if cfg.provided("max_rows") && cfg.MaxRows == 0 {
		return fmt.Errorf("%w: max_rows must be >= 1", errConfig)
	}
	if len(cfg.Types) > 0 {
		cfg.types = make(map[string]workbook.ColumnType, len(cfg.Types))
		for column, raw := range cfg.Types {
			t, err := workbook.ParseColumnType(raw)
			if err != nil {
				return fmt.Errorf("%w: types.%s: %v", errConfig, column, err)
			}
			cfg.types[column] = t
		}
	}
	if err := workbook.ValidateWhere(cfg.Where); err != nil {
		return fmt.Errorf("%w: where: %v", errConfig, err)
	}
	if err := validateCSVConfig(operation, cfg); err != nil {
		return err
	}
	if operation == opValidate {
		return validateValidateConfig(cfg)
	}
	if operation == opExtract {
		return validateExtractConfig(cfg)
	}
	if isWriter(operation) {
		return validateWriterConfig(operation, cfg)
	}
	return nil
}

// validateCSVConfig parses the encoding and delimiter of a CSV file: the
// input of write and append, or the output of convert.
func validateCSVConfig(operation string, cfg *config) error {
	if cfg.provided("encoding") {
		enc, err := workbook.ParseEncoding(cfg.Encoding)
		if err != nil {
			return fmt.Errorf("%w: encoding must be utf-8, utf-8-bom, or shift_jis", errConfig)
		}
		cfg.encoding = enc
	}
	if cfg.provided("delimiter") {
		runes := []rune(cfg.Delimiter)
		if len(runes) != 1 {
			return fmt.Errorf("%w: delimiter must be a single character", errConfig)
		}
		if !validDelimiter(runes[0]) {
			return fmt.Errorf("%w: delimiter cannot be a quote, a line break, or NUL", errConfig)
		}
		cfg.delimiter = runes[0]
	}
	if operation == opWrite || operation == opAppend {
		for _, field := range []string{"encoding", "delimiter"} {
			if !cfg.present[field] {
				continue
			}
			if !cfg.present["input"] {
				return fmt.Errorf("%w: %s requires with.input", errConfig, field)
			}
			// The input's format is the format option or its extension;
			// only csv has an encoding or a delimiter.
			format := strings.ToLower(strings.TrimSpace(cfg.Format))
			if format == "" && cfg.provided("input") {
				format = strings.TrimPrefix(strings.ToLower(filepath.Ext(cfg.Input)), ".")
			}
			if format != "" && format != "csv" {
				return fmt.Errorf("%w: %s applies to csv only", errConfig, field)
			}
		}
	}
	return nil
}

// validDelimiter mirrors what encoding/csv accepts as a field separator.
func validDelimiter(r rune) bool {
	return r != 0 && r != '"' && r != '\r' && r != '\n' && utf8.ValidRune(r) && r != utf8.RuneError
}

// validateValidateConfig checks the rules of xlsx.validate.
func validateValidateConfig(cfg *config) error {
	// A rule counts when it holds something, or when its value is still a
	// reference that resolves at run time; an empty list or map checks
	// nothing and is not a rule.
	rules := 0
	for field, size := range map[string]int{
		"required": len(cfg.Required), "not_blank": len(cfg.NotBlank), "unique": len(cfg.Unique),
		"types": len(cfg.Types), "allowed": len(cfg.Allowed),
	} {
		if size > 0 || cfg.deferred[field] {
			rules++
		}
	}
	if rules == 0 {
		return fmt.Errorf("%w: validate requires at least one of with.required, with.not_blank, with.unique, with.types, or with.allowed", errConfig)
	}
	switch cfg.OnProblem {
	case "", "warn", "fail":
	default:
		return fmt.Errorf("%w: on_problem must be warn or fail", errConfig)
	}
	if cfg.MaxProblems < 0 || (cfg.provided("max_problems") && cfg.MaxProblems == 0) {
		return fmt.Errorf("%w: max_problems must be >= 1", errConfig)
	}
	if len(cfg.Allowed) > 0 {
		cfg.allowed = make(map[string][]any, len(cfg.Allowed))
		for column, raw := range cfg.Allowed {
			list, ok := raw.([]any)
			if !ok {
				return fmt.Errorf("%w: allowed.%s must be a list of values", errConfig, column)
			}
			cfg.allowed[column] = list
		}
	}
	return nil
}

// validateExtractConfig checks the instruction and the schema of
// xlsx.extract, and reads which column type each property pins.
func validateExtractConfig(cfg *config) error {
	if strings.TrimSpace(cfg.Instruction) == "" && !cfg.deferred["instruction"] {
		return fmt.Errorf("%w: extract requires with.instruction", errConfig)
	}
	if !cfg.present["schema"] {
		return fmt.Errorf("%w: extract requires with.schema", errConfig)
	}
	if !cfg.provided("schema") {
		return nil
	}
	if cfg.Schema["type"] != "object" {
		return fmt.Errorf("%w: schema must have type: object", errConfig)
	}
	var properties map[string]any
	if raw, present := cfg.Schema["properties"]; present {
		var ok bool
		if properties, ok = raw.(map[string]any); !ok {
			return fmt.Errorf("%w: schema.properties must be an object", errConfig)
		}
	}
	cfg.extractProperties = make([]string, 0, len(properties))
	cfg.extractTypes = map[string]workbook.ColumnType{}
	for _, name := range sortedNames(properties) {
		if slices.Contains(extractFixedOutputs, name) {
			return fmt.Errorf("%w: schema property %q collides with an output of xlsx.extract", errConfig, name)
		}
		if name == respondLabels {
			return fmt.Errorf("%w: schema property %q is reserved by xlsx.extract", errConfig, name)
		}
		cfg.extractProperties = append(cfg.extractProperties, name)
		spec, ok := properties[name].(map[string]any)
		if !ok {
			return fmt.Errorf("%w: schema.properties.%s must be an object", errConfig, name)
		}
		t, ok, err := pinnedType(spec)
		if err != nil {
			return fmt.Errorf("%w: schema.properties.%s: %v", errConfig, name, err)
		}
		if ok {
			cfg.extractTypes[name] = t
		}
	}
	return nil
}

// pinnedType maps a property's JSON Schema type to the column type the
// cell is read with: string (date or date-time by format), number,
// integer, or boolean. A property without a type takes the cell's own
// value.
func pinnedType(spec map[string]any) (workbook.ColumnType, bool, error) {
	raw, present := spec["type"]
	if !present {
		return "", false, nil
	}
	kind, _ := raw.(string)
	switch kind {
	case "string":
		switch format, _ := spec["format"].(string); format {
		case "date":
			return workbook.TypeDate, true, nil
		case "date-time":
			return workbook.TypeDateTime, true, nil
		default:
			return workbook.TypeString, true, nil
		}
	case "number":
		return workbook.TypeNumber, true, nil
	case "integer":
		return workbook.TypeInteger, true, nil
	case "boolean":
		return workbook.TypeBoolean, true, nil
	default:
		return "", false, fmt.Errorf("type must be string, number, integer, or boolean")
	}
}

func validateWriterConfig(operation string, cfg *config) error {
	if operation == opWrite || operation == opAppend {
		hasRows := cfg.present["rows"]
		hasInput := strings.TrimSpace(cfg.Input) != "" || cfg.deferred["input"]
		switch {
		case !hasRows && !hasInput:
			return fmt.Errorf("%w: %s requires with.rows or with.input", errConfig, operation)
		case hasRows && hasInput:
			return fmt.Errorf("%w: %s accepts with.rows or with.input, not both", errConfig, operation)
		}
		// Writers take header: true or false; a row-number header is a
		// read concept and would be silently coerced otherwise.
		if cfg.header.Mode == workbook.HeaderRows {
			return fmt.Errorf("%w: header must be true or false for %s", errConfig, operation)
		}
	}
	if operation == opUpdateRows {
		if strings.TrimSpace(cfg.Key) == "" && !cfg.deferred["key"] {
			return fmt.Errorf("%w: key is required for update_rows", errConfig)
		}
		if !cfg.present["rows"] {
			return fmt.Errorf("%w: update_rows requires with.rows", errConfig)
		}
		if cfg.header.Mode == workbook.HeaderNone {
			return fmt.Errorf("%w: update_rows needs a header row; header: false is not supported", errConfig)
		}
		switch cfg.Missing {
		case "", string(workbook.MissingFail), string(workbook.MissingSkip), string(workbook.MissingAppend):
		default:
			return fmt.Errorf("%w: missing must be fail, skip, or append", errConfig)
		}
		if strings.TrimSpace(cfg.Key) == workbook.RowNumberKey && cfg.Missing != "" && cfg.Missing != string(workbook.MissingFail) {
			return fmt.Errorf("%w: missing: %s needs a key column; with key: _row nothing else identifies a row", errConfig, cfg.Missing)
		}
		if cfg.provided("set") {
			set, err := workbook.ParseSet(cfg.Set)
			if err != nil {
				return fmt.Errorf("%w: %v", errConfig, err)
			}
			cfg.set = set
		}
	}
	if operation == opWriteCells {
		if !cfg.present["cells"] {
			return fmt.Errorf("%w: write_cells requires with.cells", errConfig)
		}
		if cfg.provided("cells") {
			cells, err := workbook.ParseCells(cfg.Cells)
			if err != nil {
				return fmt.Errorf("%w: %v", errConfig, err)
			}
			cfg.cells = cells
		}
		if cfg.provided("merge") {
			if len(cfg.Merge) == 0 {
				return fmt.Errorf("%w: merge must not be empty", errConfig)
			}
			for _, ref := range cfg.Merge {
				if err := workbook.CheckMergeRange(ref); err != nil {
					return fmt.Errorf("%w: merge: %v", errConfig, err)
				}
			}
		}
		if cfg.provided("output") {
			if err := workbook.CheckExtension(cfg.Output); err != nil {
				return fmt.Errorf("%w: output: %v", errConfig, err)
			}
		}
	}
	if operation == opSheet {
		if err := validateSheetConfig(cfg); err != nil {
			return err
		}
	}
	if operation == opConvert {
		if strings.TrimSpace(cfg.Output) == "" && !cfg.deferred["output"] {
			return fmt.Errorf("%w: convert requires with.output", errConfig)
		}
		if cfg.provided("output") || cfg.provided("format") {
			format, err := workbook.ParseConvertFormat(cfg.Format, cfg.Output)
			if err != nil && !(cfg.Format == "" && cfg.deferred["output"]) {
				return fmt.Errorf("%w: %v", errConfig, err)
			}
			cfg.convertFormat = format
		}
		if cfg.convertFormat != "" && cfg.convertFormat != workbook.ConvertCSV {
			for _, field := range []string{"encoding", "delimiter"} {
				if cfg.present[field] {
					return fmt.Errorf("%w: %s applies to csv only", errConfig, field)
				}
			}
		}
	}
	switch cfg.Format {
	case "", "json", "jsonl", "csv":
	default:
		return fmt.Errorf("%w: format must be json, jsonl, or csv", errConfig)
	}
	switch cfg.Mode {
	case "", string(workbook.WriteReplace), string(workbook.WriteAppend):
	default:
		return fmt.Errorf("%w: mode must be replace or append", errConfig)
	}
	switch cfg.Style {
	case "", string(workbook.StyleTable), string(workbook.StyleNone):
	default:
		return fmt.Errorf("%w: style must be table or none", errConfig)
	}
	if strings.TrimSpace(cfg.WaitForUnlock) != "" {
		wait, err := spec.ParseDuration(cfg.WaitForUnlock)
		if err != nil || wait < 0 {
			return fmt.Errorf("%w: wait_for_unlock must be a duration such as 30s or 5m", errConfig)
		}
		cfg.wait = wait
	}
	return nil
}

func (cfg config) lockOptions(log func(string)) workbook.LockOptions {
	return workbook.LockOptions{WaitFor: cfg.wait, Log: log}
}

func (cfg config) writeOptions(log func(string)) workbook.WriteOptions {
	// Writers take header: true or false only; a row-number header is a
	// read concept.
	header := cfg.header.Mode != workbook.HeaderNone
	return workbook.WriteOptions{
		Password: cfg.Password,
		Sheet:    cfg.Sheet,
		Mode:     workbook.WriteMode(cfg.Mode),
		Header:   header,
		Style:    workbook.StyleMode(cfg.Style),
		Types:    cfg.types,
		InPlace:  !cfg.Atomic,
		DryRun:   cfg.DryRun,
		Lock:     cfg.lockOptions(log),
	}
}

func rejectForeignFields(operation string, present map[string]bool, allowed []string) error {
	ok := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		ok[name] = true
	}
	var foreign []string
	for name := range present {
		if !ok[name] {
			foreign = append(foreign, name)
		}
	}
	if len(foreign) == 0 {
		return nil
	}
	sort.Strings(foreign)
	return fmt.Errorf("%w: with.%s is not valid for xlsx.%s", errConfig, foreign[0], operation)
}

func (cfg config) readOptions() workbook.ReadOptions {
	return workbook.ReadOptions{
		Password:      cfg.Password,
		Sheet:         cfg.Sheet,
		Range:         cfg.Range,
		Header:        cfg.header,
		Columns:       cfg.columns,
		Merged:        workbook.MergedMode(cfg.Merged),
		StopAtBlank:   cfg.StopAtBlank,
		KeepEmptyRows: cfg.KeepEmptyRows,
		SkipHidden:    cfg.SkipHidden,
		Trim:          cfg.Trim,
		Formulas:      workbook.FormulaMode(cfg.Formulas),
		Types:         cfg.types,
		OnTypeError:   workbook.TypeErrorMode(cfg.OnTypeError),
		Where:         cfg.Where,
		MaxRows:       cfg.MaxRows,
	}
}

// validateSheetConfig checks the fields of xlsx.sheet against its operation.
func validateSheetConfig(cfg *config) error {
	operation := strings.ToLower(strings.TrimSpace(cfg.Operation))
	if operation == "" && !cfg.deferred["operation"] {
		return fmt.Errorf("%w: operation is required for sheet", errConfig)
	}
	switch operation {
	case "", string(workbook.SheetAdd), string(workbook.SheetCopy), string(workbook.SheetRename), string(workbook.SheetDelete):
	default:
		return fmt.Errorf("%w: operation must be add, copy, rename, or delete", errConfig)
	}
	if strings.TrimSpace(cfg.Sheet) == "" && !cfg.deferred["sheet"] {
		return fmt.Errorf("%w: sheet is required for sheet", errConfig)
	}
	known := operation != ""
	needsTo := operation == string(workbook.SheetCopy) || operation == string(workbook.SheetRename)
	if known && needsTo && strings.TrimSpace(cfg.To) == "" && !cfg.deferred["to"] {
		return fmt.Errorf("%w: to is required for %s", errConfig, operation)
	}
	if known && !needsTo && cfg.present["to"] {
		return fmt.Errorf("%w: to is only valid for copy and rename", errConfig)
	}
	switch cfg.IfExists {
	case "", string(workbook.ExistsFail), string(workbook.ExistsSkip), string(workbook.ExistsReplace):
	default:
		return fmt.Errorf("%w: if_exists must be fail, skip, or replace", errConfig)
	}
	if known && operation == string(workbook.SheetDelete) && cfg.present["if_exists"] {
		return fmt.Errorf("%w: if_exists is only valid for add, copy, and rename", errConfig)
	}
	switch cfg.Missing {
	case "", string(workbook.MissingFail), string(workbook.MissingSkip):
	default:
		return fmt.Errorf("%w: missing must be fail or skip for sheet", errConfig)
	}
	if known && operation == string(workbook.SheetAdd) && cfg.present["missing"] {
		return fmt.Errorf("%w: missing is only valid for copy, rename, and delete", errConfig)
	}
	if cfg.provided("position") && cfg.Position < 1 {
		return fmt.Errorf("%w: position must be >= 1", errConfig)
	}
	if known && cfg.present["position"] && operation != string(workbook.SheetAdd) && operation != string(workbook.SheetCopy) {
		return fmt.Errorf("%w: position is only valid for add and copy", errConfig)
	}
	return nil
}

func (cfg config) validateOptions() workbook.ValidateOptions {
	return workbook.ValidateOptions{
		Password:    cfg.Password,
		Sheet:       cfg.Sheet,
		Range:       cfg.Range,
		Header:      cfg.header,
		Columns:     cfg.columns,
		Merged:      workbook.MergedMode(cfg.Merged),
		Trim:        cfg.Trim,
		Formulas:    workbook.FormulaMode(cfg.Formulas),
		SkipHidden:  cfg.SkipHidden,
		Required:    cfg.Required,
		NotBlank:    cfg.NotBlank,
		Unique:      cfg.Unique,
		Types:       cfg.types,
		Allowed:     cfg.allowed,
		MaxProblems: cfg.MaxProblems,
	}
}

func (cfg config) writeCellsOptions(output string, log func(string)) workbook.WriteCellsOptions {
	return workbook.WriteCellsOptions{
		Password: cfg.Password,
		Sheet:    cfg.Sheet,
		Cells:    cfg.cells,
		Merge:    cfg.Merge,
		Output:   output,
		InPlace:  !cfg.Atomic,
		DryRun:   cfg.DryRun,
		Lock:     cfg.lockOptions(log),
	}
}

func (cfg config) sheetOptions(log func(string)) workbook.SheetOptions {
	return workbook.SheetOptions{
		Password:  cfg.Password,
		Operation: workbook.SheetOperation(strings.ToLower(strings.TrimSpace(cfg.Operation))),
		Sheet:     cfg.Sheet,
		To:        cfg.To,
		IfExists:  workbook.ExistsMode(cfg.IfExists),
		Missing:   workbook.MissingMode(cfg.Missing),
		Position:  cfg.Position,
		InPlace:   !cfg.Atomic,
		DryRun:    cfg.DryRun,
		Lock:      cfg.lockOptions(log),
	}
}

func (cfg config) convertOptions(output string) workbook.ConvertOptions {
	return workbook.ConvertOptions{
		Password:   cfg.Password,
		Output:     output,
		Format:     cfg.convertFormat,
		Sheet:      cfg.Sheet,
		Range:      cfg.Range,
		Header:     cfg.header,
		Columns:    cfg.columns,
		Types:      cfg.types,
		Trim:       cfg.Trim,
		Merged:     workbook.MergedMode(cfg.Merged),
		Formulas:   workbook.FormulaMode(cfg.Formulas),
		SkipHidden: cfg.SkipHidden,
		Encoding:   cfg.encoding,
		Delimiter:  cfg.delimiter,
		InPlace:    !cfg.Atomic,
	}
}

func (cfg config) updateOptions(rows []workbook.Row, log func(string)) workbook.UpdateOptions {
	return workbook.UpdateOptions{
		Password: cfg.Password,
		Sheet:    cfg.Sheet,
		Header:   cfg.header,
		Key:      strings.TrimSpace(cfg.Key),
		Rows:     rows,
		Set:      cfg.set,
		Missing:  workbook.MissingMode(cfg.Missing),
		InPlace:  !cfg.Atomic,
		DryRun:   cfg.DryRun,
		Lock:     cfg.lockOptions(log),
	}
}

func boolOrRef(description string) *jsonschema.Schema {
	return &jsonschema.Schema{Description: description}
}

var configSchema = &jsonschema.Schema{
	Type:                 "object",
	AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	Properties: map[string]*jsonschema.Schema{
		"path":     {Type: "string", Description: "Workbook path (.xlsx). Relative paths resolve against the step working directory. Required."},
		"password": {Type: "string", Description: "Password of a protected workbook. Reference a secret or ${env.NAME} rather than writing it here."},
		"sheet":    {Type: "string", Description: "Sheet name; the first sheet by default. Matched exactly, then case-insensitively."},
		"range": {Type: "string", Description: "Cell range such as A2:F or A2:F200, a Sheet!A2:F reference, a named range, or a table name. " +
			"Without it the data block is detected: leading empty rows and columns are skipped."},
		"header": {Description: "true (default) takes the first row of the range as column names, false names columns A, B, C, " +
			"a row number takes that sheet row, and a list such as [3, 4] joins two header rows with a space. " +
			"For append and write with mode: append, true matches each field to the header row by name and false appends rows by position with no header row."},
		"columns": {Description: "Columns to keep, in order: names, or {name: alias} entries to rename, such as [Status, {Invoice No: invoice_no}]."},
		"merged": {Type: "string", Enum: []any{"fill", "first"},
			Description: "How merged cells are read: fill (default) repeats the value into every covered cell; first keeps it in the top-left cell only."},
		"stop_at_blank":   boolOrRef("Stop at the first fully empty row instead of reading to the end of the used range."),
		"keep_empty_rows": boolOrRef("Keep trailing empty rows as rows of nulls."),
		"skip_hidden":     boolOrRef("Leave out rows hidden by a filter or by hand. Defaults to false: hidden rows are read."),
		"trim":            boolOrRef("Trim surrounding white space, including full-width spaces, from text cells."),
		"formulas": {Type: "string", Enum: []any{"cached", "text", "calculate"},
			Description: "What formula cells yield: cached (default) the stored result, text the formula itself, calculate an evaluation."},
		"types": {Type: "object", AdditionalProperties: &jsonschema.Schema{Type: "string", Enum: []any{"string", "number", "integer", "boolean", "date", "datetime"}},
			Description: "Column types to enforce, such as {amount: number, due: date}. A cell that cannot convert fails the step naming the cell."},
		"on_type_error": {Enum: []any{"fail", "warn", "null", nil},
			Description: "What a cell that fails its type does: fail (default) the step, or warn and read the cell as null."},
		"where":    {Type: "object", Description: "Rows to keep: {Status: \"\"} matches empty cells, {Status: {ne: Done}} excludes a value, {Status: {in: [A, B]}} matches a list."},
		"max_rows": {Description: "Most rows to read, 1 or more. Defaults to 5000; truncated is true when more rows exist."},
		"rows": {Description: "Rows to write: a list of objects or arrays, usually ${steps.<id>.outputs.rows}. " +
			"Rows from a step output arrive with keys in alphabetical order; set columns to choose the order, such as columns: ${steps.<id>.outputs.headers}."},
		"input":  {Type: "string", Description: "File to write rows from instead of rows: .json (an array), .jsonl, or .csv with a header line."},
		"format": {Type: "string", Enum: []any{"json", "jsonl", "csv"}, Description: "Format of input when its extension does not say."},
		"mode": {Type: "string", Enum: []any{"replace", "append"},
			Description: "What xlsx.write does to an existing sheet: replace (default) its contents, or append below its last row."},
		"style": {Type: "string", Enum: []any{"table", "none"},
			Description: "How a new sheet looks: table (default) has a bold frozen header, fitted widths, and number formats by column; none writes bare cells."},
		"atomic":  boolOrRef("Save through a temporary file renamed over the workbook. Defaults to true."),
		"dry_run": boolOrRef("Compute and report the changes without saving the workbook."),
		"wait_for_unlock": {Type: "string", Description: "How long to retry a workbook that another program holds open, such as 5m. " +
			"Retries start at two seconds and double to one minute. Without it a locked workbook fails at once."},
		"key": {Type: "string", Description: "Column that identifies a row for xlsx.update_rows, or _row to address rows by the _row each row carries. " +
			"A key column that is no longer in the header row fails the step before any cell changes."},
		"set": {Type: "object", Description: "Columns to write for xlsx.update_rows: {Status: status} takes the status field of each row, " +
			"{Reviewed: {value: yes}} writes one literal to every row. Omitted: every field other than the key and _row goes to the column of the same name. " +
			"A column missing from the header is added at the right."},
		"missing": {Type: "string", Enum: []any{"fail", "skip", "append"},
			Description: "What xlsx.update_rows does with a row whose key is not in the sheet: fail (default), skip it with a warning, or append it below the last row."},
		"artifact": boolOrRef("Keep a copy of the saved workbook with the run's artifacts, under xlsx/<step>/, so it is listed with the run. " +
			"Enables artifact storage for the DAG."),
		"required":  {Description: "xlsx.validate: columns the header row must have, such as [Invoice No, Amount]."},
		"not_blank": {Description: "xlsx.validate: columns no row may leave empty."},
		"unique":    {Description: "xlsx.validate: columns whose values may not repeat; empty cells are skipped."},
		"allowed":   {Type: "object", Description: "xlsx.validate: the values each column may hold, such as {Status: [Open, Done]}; empty cells are skipped."},
		"on_problem": {Type: "string", Enum: []any{"warn", "fail"},
			Description: "What xlsx.validate does when it finds problems: warn (default) succeeds and publishes them, fail fails the step after listing them."},
		"max_problems": {Description: "Most problems xlsx.validate keeps, 1 or more. Defaults to 1000; count still reports every problem found."},
		"cells": {Type: "object", Description: "xlsx.write_cells: cell addresses to write, such as {B2: Acme, Sheet1!D7: 2026-10-01, Total: {formula: SUM(E2:E9)}}. " +
			"A value writes the cell, null clears it, {value: v, type: t} pins its type, {formula: text} writes a formula. " +
			"An address is a cell, Sheet!cell, or a defined name for one cell."},
		"merge": {Description: "xlsx.write_cells: ranges to merge before the cells are written, such as [A1:D1]. " +
			"A merged range keeps only its top-left value; a range covering a cell that holds something, or overlapping another merged region, fails the step."},
		"output": {Type: "string", Description: "xlsx.write_cells: workbook to write the result to, leaving path as it was, so a template can be filled many times. " +
			"xlsx.convert: the csv, json, or jsonl file to write."},
		"operation": {Type: "string", Enum: []any{"add", "copy", "rename", "delete"},
			Description: "What xlsx.sheet does: add a sheet named sheet, copy sheet to to, rename sheet to to, or delete sheet."},
		"to": {Type: "string", Description: "xlsx.sheet: the new name for copy and rename."},
		"if_exists": {Type: "string", Enum: []any{"fail", "skip", "replace"},
			Description: "What xlsx.sheet does when the sheet to create already exists: fail (default), skip with a warning, or replace its contents."},
		"position": {Description: "xlsx.sheet: 1-based position of the sheet add or copy creates; by default an added sheet goes last and a copy right after its source."},
		"encoding": {Type: "string", Enum: []any{"utf-8", "utf-8-bom", "shift_jis", "cp932", "windows-31j", "sjis", "ms932"},
			Description: "Encoding of a csv file: the input of xlsx.write and xlsx.append, or the output of xlsx.convert. utf-8 (default), utf-8-bom, or shift_jis (also cp932, windows-31j)."},
		"delimiter": {Type: "string", Description: "Field separator of a csv file, one character; a comma by default."},
		"instruction": {Type: "string", Description: "xlsx.extract: what to find on the sheet, such as 'A supplier quote; find the quote number, delivery date, and total'. " +
			"Sent to the model with the sheet's cells; must not contain a secret."},
		"schema": {Type: "object", Description: "xlsx.extract: a JSON Schema with type: object whose properties name the fields to find. " +
			"Each property becomes an output holding the typed value of the cell the model names; a property's type (string, number, integer, boolean; string with format date or date-time) pins how the cell is read."},
		"send_values": boolOrRef("xlsx.extract: list every cell's text for the model (default). false lists non-text cells by kind only, so amounts and dates stay on the host."),
		"cache":       boolOrRef("xlsx.extract: keep the cells the model named by sheet layout, so a repeated layout needs no model call (default true)."),
		"llm":         {Type: "object", Description: "xlsx.extract: the model to ask, replacing the DAG-level llm block; the same fields as that block."},
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
