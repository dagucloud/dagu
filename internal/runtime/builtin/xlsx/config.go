// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/executor/registry"
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

	// Parsed forms, filled by validateConfig.
	header  workbook.HeaderSpec
	columns []workbook.ColumnSelect
	types   map[string]workbook.ColumnType
	present map[string]bool
}

func defaultConfig() config {
	return config{}
}

func decodeConfig(raw map[string]any, cfg *config) error {
	if raw == nil {
		raw = map[string]any{}
	}
	cfg.present = make(map[string]bool, len(raw))
	for key := range raw {
		cfg.present[key] = true
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
	if err := decoder.Decode(raw); err != nil {
		return fmt.Errorf("%w: %v", errConfig, err)
	}
	return nil
}

// fieldsByOperation lists the with fields each operation accepts.
var fieldsByOperation = map[string][]string{
	opRead: {"path", "password", "sheet", "range", "header", "columns", "merged", "stop_at_blank",
		"keep_empty_rows", "trim", "formulas", "types", "on_type_error", "where", "max_rows"},
	opInfo:       {"path", "password"},
	opListSheets: {"path", "password"},
}

func validateConfig(operation string, cfg *config) error {
	allowed, ok := fieldsByOperation[operation]
	if !ok {
		return fmt.Errorf("%w: unsupported operation %q", errConfig, operation)
	}
	if err := rejectForeignFields(operation, cfg.present, allowed); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Path) == "" {
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
	if cfg.present["on_type_error"] && cfg.OnTypeError == "" {
		cfg.OnTypeError = string(workbook.TypeErrorWarn)
	}
	switch cfg.OnTypeError {
	case "", string(workbook.TypeErrorFail), string(workbook.TypeErrorWarn), string(workbook.TypeErrorNull):
	default:
		return fmt.Errorf("%w: on_type_error must be fail or warn", errConfig)
	}
	if cfg.MaxRows < 0 {
		return fmt.Errorf("%w: max_rows must be >= 1", errConfig)
	}
	if cfg.present["max_rows"] && cfg.MaxRows == 0 {
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
	return nil
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
	},
}

func init() {
	registry.RegisterExecutorConfigSchema(executorType, configSchema)
}
