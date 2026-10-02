// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

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

	// Parsed forms, filled by validateConfig.
	header  workbook.HeaderSpec
	columns []workbook.ColumnSelect
	types   map[string]workbook.ColumnType
	set     map[string]workbook.SetValue
	wait    time.Duration
	present map[string]bool
	// deferred lists fields whose value is still a reference at build time.
	deferred map[string]bool
}

// provided reports whether a field has a usable value now: it is present
// and not deferred to the run.
func (cfg config) provided(field string) bool {
	return cfg.present[field] && !cfg.deferred[field]
}

func defaultConfig() config {
	return config{Atomic: true}
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
		"keep_empty_rows", "trim", "formulas", "types", "on_type_error", "where", "max_rows"},
	opInfo:       {"path", "password"},
	opListSheets: {"path", "password"},
	opWrite: {"path", "password", "sheet", "rows", "input", "format", "columns", "header", "mode", "style",
		"types", "atomic", "dry_run", "wait_for_unlock", "artifact"},
	opAppend: {"path", "password", "sheet", "rows", "input", "format", "columns", "types", "atomic",
		"dry_run", "wait_for_unlock", "artifact"},
	opUpdateRows: {"path", "password", "sheet", "header", "rows", "key", "set", "missing", "atomic",
		"dry_run", "wait_for_unlock", "artifact"},
}

func isWriter(operation string) bool {
	switch operation {
	case opWrite, opAppend, opUpdateRows:
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
	if isWriter(operation) {
		return validateWriterConfig(operation, cfg)
	}
	return nil
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
		if cfg.present["set"] {
			set, err := workbook.ParseSet(cfg.Set)
			if err != nil {
				return fmt.Errorf("%w: %v", errConfig, err)
			}
			cfg.set = set
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
		Trim:          cfg.Trim,
		Formulas:      workbook.FormulaMode(cfg.Formulas),
		Types:         cfg.types,
		OnTypeError:   workbook.TypeErrorMode(cfg.OnTypeError),
		Where:         cfg.Where,
		MaxRows:       cfg.MaxRows,
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
			"a row number takes that sheet row, and a list such as [3, 4] joins two header rows with a space."},
		"columns": {Description: "Columns to keep, in order: names, or {name: alias} entries to rename, such as [Status, {Invoice No: invoice_no}]."},
		"merged": {Type: "string", Enum: []any{"fill", "first"},
			Description: "How merged cells are read: fill (default) repeats the value into every covered cell; first keeps it in the top-left cell only."},
		"stop_at_blank":   boolOrRef("Stop at the first fully empty row instead of reading to the end of the used range."),
		"keep_empty_rows": boolOrRef("Keep trailing empty rows as rows of nulls."),
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
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
