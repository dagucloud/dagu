// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package data

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"gopkg.in/yaml.v3"
)

const (
	executorType = "data"

	opConvert = "convert"
	opPick    = "pick"

	formatJSON = "json"
	formatYAML = "yaml"
	formatCSV  = "csv"
	formatTSV  = "tsv"
	formatText = "text"
)

var (
	errConfig      = errors.New("data: configuration error")
	errUnsupported = errors.New("data: unsupported operation")
)

var _ executor.Executor = (*executorImpl)(nil)

type executorImpl struct {
	mu      sync.Mutex
	stdout  io.Writer
	stderr  io.Writer
	cfg     config
	op      string
	workDir string
}

func init() {
	executor.RegisterExecutor(executorType, newExecutor, validateStep, core.ExecutorCapabilities{Command: true})
}

func newExecutor(ctx context.Context, step core.Step) (executor.Executor, error) {
	var cfg config
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return nil, err
	}

	op := stepOperation(step)
	if err := validateConfig(op, cfg); err != nil {
		return nil, err
	}

	env := runtime.GetEnv(ctx)
	return &executorImpl{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		cfg:     cfg,
		op:      op,
		workDir: env.WorkingDir,
	}, nil
}

func validateStep(step core.Step) error {
	if step.ExecutorConfig.Type != executorType {
		return nil
	}
	var cfg config
	if err := decodeConfig(step.ExecutorConfig.Config, &cfg); err != nil {
		return err
	}
	return validateConfig(stepOperation(step), cfg)
}

func stepOperation(step core.Step) string {
	if len(step.Commands) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(step.Commands[0].Command))
}

func (e *executorImpl) SetStdout(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stdout = out
}

func (e *executorImpl) SetStderr(out io.Writer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stderr = out
}

func (*executorImpl) Kill(_ os.Signal) error { return nil }

func (e *executorImpl) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	switch e.op {
	case opConvert:
		return e.runConvert(ctx)
	case opPick:
		return e.runPick(ctx)
	default:
		return fmt.Errorf("%w: %q", errUnsupported, e.op)
	}
}

func (e *executorImpl) runConvert(ctx context.Context) error {
	value, err := e.readValue(ctx)
	if err != nil {
		return err
	}

	parsed, err := parseValue(e.cfg, value)
	if err != nil {
		return err
	}

	return writeValue(e.stdout, e.cfg, parsed)
}

func (e *executorImpl) runPick(ctx context.Context) error {
	value, err := e.readValue(ctx)
	if err != nil {
		return err
	}

	parsed, err := parseValue(e.cfg, value)
	if err != nil {
		return err
	}

	selected, ok := eval.ResolveDataPath(ctx, "data.pick", parsed, e.cfg.Select)
	if !ok {
		return fmt.Errorf("data pick: failed to resolve select path %q", e.cfg.Select)
	}

	if e.cfg.Raw {
		return writeRawValue(e.stdout, selected)
	}

	outCfg := e.cfg
	if strings.TrimSpace(outCfg.To) == "" {
		outCfg.To = formatJSON
	}
	return writeValue(e.stdout, outCfg, selected)
}

func (e *executorImpl) readValue(ctx context.Context) (any, error) {
	if e.cfg.hasData {
		return e.cfg.Data, nil
	}

	inputPath, err := runtime.EvalStepString(ctx, e.cfg.Input)
	if err != nil {
		return nil, fmt.Errorf("data: failed to evaluate input path: %w", err)
	}
	if !filepath.IsAbs(inputPath) {
		inputPath = filepath.Join(e.workDir, inputPath)
	}
	inputPath = filepath.Clean(inputPath)

	data, err := os.ReadFile(inputPath) //nolint:gosec // input path is workflow-controlled local file input.
	if err != nil {
		return nil, fmt.Errorf("data: reading input file %q: %w", inputPath, err)
	}
	return string(data), nil
}

func parseValue(cfg config, value any) (any, error) {
	format := strings.ToLower(cfg.From)
	switch format {
	case formatJSON:
		return parseJSON(value)
	case formatYAML:
		return parseYAML(value)
	case formatCSV, formatTSV:
		raw, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("data: %s input must be a string or input file", format)
		}
		return parseDelimited(raw, cfg)
	case formatText:
		return stringifyText(value), nil
	default:
		return nil, fmt.Errorf("%w: unsupported from format %q", errConfig, cfg.From)
	}
}

func parseJSON(value any) (any, error) {
	raw, ok := value.(string)
	if !ok {
		return value, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("data: failed to decode JSON: %w", err)
	}
	return decoded, nil
}

func parseYAML(value any) (any, error) {
	raw, ok := value.(string)
	if !ok {
		return value, nil
	}
	var decoded any
	if err := yaml.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, fmt.Errorf("data: failed to decode YAML: %w", err)
	}
	return normalizeYAMLValue(decoded), nil
}

func parseDelimited(raw string, cfg config) (any, error) {
	reader := csv.NewReader(strings.NewReader(raw))
	reader.Comma = delimiterFor(cfg)
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("data: failed to decode %s: %w", cfg.From, err)
	}
	if len(records) == 0 {
		return []any{}, nil
	}

	hasHeader := true
	if cfg.HasHeader != nil {
		hasHeader = *cfg.HasHeader
	}

	var columns []string
	var rows [][]string
	switch {
	case hasHeader:
		columns = records[0]
		rows = records[1:]
	case len(cfg.Columns) > 0:
		columns = cfg.Columns
		rows = records
	default:
		result := make([]any, len(records))
		for i, record := range records {
			row := make([]any, len(record))
			for j, field := range record {
				row[j] = field
			}
			result[i] = row
		}
		return result, nil
	}

	result := make([]any, len(rows))
	for i, record := range rows {
		row := make(map[string]any, len(columns))
		for colIdx, column := range columns {
			value := ""
			if colIdx < len(record) {
				value = record[colIdx]
			}
			row[column] = value
		}
		result[i] = row
	}
	return result, nil
}

func writeValue(w io.Writer, cfg config, value any) error {
	switch strings.ToLower(cfg.To) {
	case formatJSON:
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return fmt.Errorf("data: failed to encode JSON: %w", err)
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	case formatYAML:
		data, err := yaml.Marshal(value)
		if err != nil {
			return fmt.Errorf("data: failed to encode YAML: %w", err)
		}
		_, err = w.Write(data)
		return err
	case formatCSV, formatTSV:
		return writeDelimited(w, cfg, value)
	case formatText:
		_, err := fmt.Fprint(w, stringifyText(value))
		return err
	default:
		return fmt.Errorf("%w: unsupported to format %q", errConfig, cfg.To)
	}
}

func writeDelimited(w io.Writer, cfg config, value any) error {
	writer := csv.NewWriter(w)
	writer.Comma = delimiterFor(config{To: cfg.To, Delimiter: cfg.Delimiter})

	rows, columns, err := tabularRows(value, cfg.Columns)
	if err != nil {
		return err
	}

	headers := true
	if cfg.Headers != nil {
		headers = *cfg.Headers
	}
	if headers && len(columns) > 0 {
		if err := writer.Write(columns); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func tabularRows(value any, preferredColumns []string) ([][]string, []string, error) {
	items, ok := asSlice(value)
	if !ok {
		return nil, nil, fmt.Errorf("data: %s output requires an array input", formatCSV)
	}
	if len(items) == 0 {
		return nil, preferredColumns, nil
	}

	if _, ok := asMap(items[0]); ok {
		columns := preferredColumns
		if len(columns) == 0 {
			columns = collectColumns(items)
		}
		rows := make([][]string, 0, len(items))
		for _, item := range items {
			rowMap, ok := asMap(item)
			if !ok {
				return nil, nil, fmt.Errorf("data: cannot mix object and array rows in delimited output")
			}
			row := make([]string, len(columns))
			for i, column := range columns {
				row[i] = cellString(rowMap[column])
			}
			rows = append(rows, row)
		}
		return rows, columns, nil
	}

	rows := make([][]string, 0, len(items))
	for _, item := range items {
		fields, ok := asSlice(item)
		if !ok {
			return nil, nil, fmt.Errorf("data: delimited output rows must be objects or arrays")
		}
		row := make([]string, len(fields))
		for i, field := range fields {
			row[i] = cellString(field)
		}
		rows = append(rows, row)
	}
	return rows, preferredColumns, nil
}

func collectColumns(items []any) []string {
	seen := make(map[string]struct{})
	for _, item := range items {
		rowMap, ok := asMap(item)
		if !ok {
			continue
		}
		for key := range rowMap {
			seen[key] = struct{}{}
		}
	}
	columns := make([]string, 0, len(seen))
	for key := range seen {
		columns = append(columns, key)
	}
	sort.Strings(columns)
	return columns
}

func asMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		result := make(map[string]any, len(typed))
		for key, val := range typed {
			result[fmt.Sprint(key)] = val
		}
		return result, true
	default:
		return nil, false
	}
}

func asSlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []map[string]any:
		result := make([]any, len(typed))
		for i, val := range typed {
			result[i] = val
		}
		return result, true
	case [][]any:
		result := make([]any, len(typed))
		for i, val := range typed {
			result[i] = val
		}
		return result, true
	default:
		return nil, false
	}
}

func delimiterFor(cfg config) rune {
	if cfg.Delimiter != "" {
		return []rune(cfg.Delimiter)[0]
	}
	switch strings.ToLower(firstNonEmpty(cfg.From, cfg.To)) {
	case formatTSV:
		return '\t'
	default:
		return ','
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func stringifyText(value any) string {
	if value == nil {
		return ""
	}
	if str, ok := value.(string); ok {
		return str
	}
	data, err := json.Marshal(value)
	if err == nil {
		return string(data)
	}
	return fmt.Sprint(value)
}

func cellString(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case map[string]any, []any:
		data, err := json.Marshal(typed)
		if err == nil {
			return string(data)
		}
	}
	return fmt.Sprint(value)
}

func writeRawValue(w io.Writer, value any) error {
	switch typed := value.(type) {
	case nil:
		_, err := fmt.Fprintln(w)
		return err
	case string:
		_, err := fmt.Fprintln(w, typed)
		return err
	case bool:
		_, err := fmt.Fprintln(w, typed)
		return err
	case float64, float32, int, int64, int32, uint, uint64, uint32:
		_, err := fmt.Fprintln(w, typed)
		return err
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Errorf("data pick: failed to encode raw value: %w", err)
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	}
}

func normalizeYAMLValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, val := range typed {
			result[key] = normalizeYAMLValue(val)
		}
		return result
	case map[any]any:
		result := make(map[string]any, len(typed))
		for key, val := range typed {
			result[fmt.Sprint(key)] = normalizeYAMLValue(val)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, val := range typed {
			result[i] = normalizeYAMLValue(val)
		}
		return result
	default:
		return typed
	}
}
