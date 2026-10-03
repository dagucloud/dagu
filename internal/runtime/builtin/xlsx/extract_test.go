// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package xlsx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	cmnconfig "github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

// quoteBook writes a supplier quote laid out as a form: labels in column
// A, values in column B.
func quoteBook(t *testing.T, dir string, cells map[string]any) string {
	t.Helper()
	f := excelize.NewFile()
	date, err := f.NewStyle(&excelize.Style{CustomNumFmt: strPtr("yyyy-mm-dd")})
	require.NoError(t, err)
	values := map[string]any{
		"A1": "御見積書",
		"A3": "見積番号", "B3": "Q-2026-001",
		"A5": "納期", "B5": time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
		"A7": "合計金額", "B7": 123000,
	}
	for cell, v := range cells {
		values[cell] = v
	}
	for cell, v := range values {
		require.NoError(t, f.SetCellValue("Sheet1", cell, v))
	}
	require.NoError(t, f.SetCellStyle("Sheet1", "B5", "B5", date))
	path := filepath.Join(dir, "quote.xlsx")
	require.NoError(t, f.SaveAs(path))
	require.NoError(t, f.Close())
	return path
}

func strPtr(s string) *string { return &s }

// scriptedProvider answers every request through answer and records the
// requests it saw.
type scriptedProvider struct {
	mu       sync.Mutex
	requests []*llmpkg.ChatRequest
	answer   func(req *llmpkg.ChatRequest) map[string]any
	fail     error
}

func (p *scriptedProvider) Chat(_ context.Context, req *llmpkg.ChatRequest) (*llmpkg.ChatResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if p.fail != nil {
		return nil, p.fail
	}
	args, err := json.Marshal(p.answer(req))
	if err != nil {
		return nil, err
	}
	return &llmpkg.ChatResponse{
		ToolCalls: []llmpkg.ToolCall{{ID: "call_1", Type: "function", Function: llmpkg.ToolCallFunction{Name: "respond", Arguments: string(args)}}},
		Usage:     llmpkg.Usage{PromptTokens: 10, CompletionTokens: 2},
	}, nil
}

func (p *scriptedProvider) ChatStream(context.Context, *llmpkg.ChatRequest) (<-chan llmpkg.StreamEvent, error) {
	return nil, errors.New("not streamed")
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *scriptedProvider) lastUserMessage() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	req := p.requests[len(p.requests)-1]
	for _, m := range req.Messages {
		if m.Role == llmpkg.RoleUser {
			return m.Content
		}
	}
	return ""
}

// labelRight answers each field by finding the listing line whose text
// equals the field's description and naming the cell to its right.
func labelRight(descriptions map[string]string) func(req *llmpkg.ChatRequest) map[string]any {
	return func(req *llmpkg.ChatRequest) map[string]any {
		listing := ""
		for _, m := range req.Messages {
			if m.Role == llmpkg.RoleUser {
				listing = m.Content
			}
		}
		answer := map[string]any{}
		for field, label := range descriptions {
			answer[field] = nil
			for _, line := range strings.Split(listing, "\n") {
				addr, text, ok := strings.Cut(line, " [")
				if !ok {
					continue
				}
				_, text, ok = strings.Cut(text, "]: ")
				if !ok || text != label {
					continue
				}
				col, row, err := excelize.CellNameToCoordinates(addr)
				if err != nil {
					continue
				}
				next, _ := excelize.CoordinatesToCellName(col+1, row)
				answer[field] = next
			}
		}
		return answer
	}
}

var quoteSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"quote_no": map[string]any{"type": "string", "description": "見積番号"},
		"delivery": map[string]any{"type": "string", "format": "date", "description": "納期"},
		"total":    map[string]any{"type": "number", "description": "合計金額"},
	},
}

var quoteAnswer = labelRight(map[string]string{"quote_no": "見積番号", "delivery": "納期", "total": "合計金額"})

type extractRun struct {
	t        *testing.T
	dir      string
	dataDir  string
	secrets  map[string]string
	provider *scriptedProvider
}

type extractExecution struct {
	exec   *extractExecutor
	stdout bytes.Buffer
	stderr bytes.Buffer
	err    error
}

func (r *extractRun) execute(cfg map[string]any, llm *ir.LLMConfig) *extractExecution {
	r.t.Helper()
	step := ir.Step{
		ID:             "fields",
		Name:           "fields",
		Commands:       []ir.CommandEntry{{Command: opExtract}},
		ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg},
		LLM:            llm,
	}
	scope := value.NewEnvScope(nil, false)
	for name, secret := range r.secrets {
		scope = scope.WithEntry(name, secret, value.EnvSourceSecret)
	}
	dag := &ir.DAG{Name: "quotes", WorkingDir: r.dir, WorkingDirExplicit: true}
	ctx := cmnconfig.WithConfig(context.Background(), &cmnconfig.Config{Paths: cmnconfig.PathsConfig{DataDir: r.dataDir}})
	ctx = runtime.NewContext(ctx, dag, "run-1", "")
	env := runtime.NewEnv(ctx, step)
	env.Scope = scope
	ctx = runtime.WithEnv(ctx, env)
	created, err := newExecutor(ctx, step)
	if err != nil {
		return &extractExecution{err: err}
	}
	execution := &extractExecution{exec: created.(*extractExecutor)}
	execution.exec.newProvider = func(context.Context, *ir.LLMConfig) (llmpkg.Provider, error) { return r.provider, nil }
	execution.exec.SetStdout(&execution.stdout)
	execution.exec.SetStderr(&execution.stderr)
	execution.err = execution.exec.Run(ctx)
	return execution
}

func newExtractRun(t *testing.T) *extractRun {
	t.Helper()
	return &extractRun{t: t, dir: t.TempDir(), dataDir: t.TempDir(), provider: &scriptedProvider{answer: quoteAnswer}}
}

var testModel = &ir.LLMConfig{Provider: "openai", Model: "test-model"}

func quoteConfig(extra map[string]any) map[string]any {
	cfg := map[string]any{"path": "quote.xlsx", "instruction": "A supplier quote; find the quote number, delivery date, and total", "schema": quoteSchema}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func TestExtractValidation(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		cfg  map[string]any
		llm  *ir.LLMConfig
		want string
	}{
		"no instruction":     {cfg: map[string]any{"path": "q.xlsx", "schema": quoteSchema}, llm: testModel, want: "extract requires with.instruction"},
		"no schema":          {cfg: map[string]any{"path": "q.xlsx", "instruction": "find"}, llm: testModel, want: "extract requires with.schema"},
		"schema not object":  {cfg: map[string]any{"path": "q.xlsx", "instruction": "find", "schema": map[string]any{"type": "array"}}, llm: testModel, want: "schema must have type: object"},
		"bad property type":  {cfg: map[string]any{"path": "q.xlsx", "instruction": "find", "schema": map[string]any{"type": "object", "properties": map[string]any{"items": map[string]any{"type": "array"}}}}, llm: testModel, want: "schema.properties.items: type must be string, number, integer, or boolean"},
		"collides":           {cfg: map[string]any{"path": "q.xlsx", "instruction": "find", "schema": map[string]any{"type": "object", "properties": map[string]any{"sheet": map[string]any{"type": "string"}}}}, llm: testModel, want: `schema property "sheet" collides with an output of xlsx.extract`},
		"foreign field":      {cfg: quoteConfig(map[string]any{"cells": map[string]any{"B2": 1}}), llm: testModel, want: "with.cells is not valid for xlsx.extract"},
		"missing model":      {cfg: quoteConfig(nil), llm: nil, want: errMissingModel},
		"deferred reference": {cfg: quoteConfig(map[string]any{"schema": "${params.SCHEMA}"}), llm: testModel, want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			step := ir.Step{
				Name:           "fields",
				Commands:       []ir.CommandEntry{{Command: opExtract}},
				ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: tc.cfg},
				LLM:            tc.llm,
			}
			err := validateStep(step)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestExtractReadsAnsweredCells(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	run := r.execute(quoteConfig(nil), testModel)
	require.NoError(t, run.err)

	outputs := run.exec.GetOutputs()
	assert.Equal(t, "Q-2026-001", outputs["quote_no"])
	assert.Equal(t, "2026-10-15", outputs["delivery"])
	assert.Equal(t, int64(123000), outputs["total"], "a property typed number reads the cell as a number")
	assert.Equal(t, map[string]string{"quote_no": "Sheet1!B3", "delivery": "Sheet1!B5", "total": "Sheet1!B7"}, outputs["cells"])
	assert.Equal(t, "Sheet1", outputs["sheet"])
	assert.Equal(t, sourceModel, outputs["source"])
	assert.Equal(t, []string{}, outputs["warnings"])
	assert.Equal(t, "Extracted 3 fields from quote.xlsx Sheet1 (model, 12 tokens)\n", run.stdout.String())
	assert.True(t, run.exec.PublishesDeclaredOutputs())

	require.Equal(t, 1, r.provider.count())
	req := r.provider.requests[0]
	assert.Equal(t, "required", req.ToolChoice)
	require.Len(t, req.Tools, 1)
	assert.Equal(t, "respond", req.Tools[0].Function.Name)
	properties := req.Tools[0].Function.Parameters["properties"].(map[string]any)
	assert.Len(t, properties, 3)
	assert.Equal(t, []any{"string", "null"}, properties["total"].(map[string]any)["type"])
	user := r.provider.lastUserMessage()
	assert.True(t, strings.HasPrefix(user, "A supplier quote; find the quote number, delivery date, and total\n\nSheet: Sheet1 (Sheet1!A1:B7, 7 cells)\n"), user)
	assert.Contains(t, user, "A7 [text]: 合計金額\nB7 [number]: 123000")
	assert.Contains(t, user, "B5 [date]: 2026-10-15")
}

func TestExtractNullAnswerIsAbsent(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	r.provider.answer = func(*llmpkg.ChatRequest) map[string]any {
		return map[string]any{"quote_no": "B3", "delivery": nil}
	}
	run := r.execute(quoteConfig(nil), testModel)
	require.NoError(t, run.err)
	outputs := run.exec.GetOutputs()
	assert.Equal(t, "Q-2026-001", outputs["quote_no"])
	assert.Nil(t, outputs["delivery"])
	assert.Nil(t, outputs["total"], "a field the model left out is absent")
	assert.Equal(t, map[string]string{"quote_no": "Sheet1!B3", "delivery": "", "total": ""}, outputs["cells"])
}

func TestExtractRejectsBadAnswers(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		answer map[string]any
		want   string
	}{
		"not an address": {answer: map[string]any{"quote_no": "B3", "delivery": "B5", "total": "123000"}, want: `xlsx: model answered field "total" with "123000", not a cell address`},
		"other sheet":    {answer: map[string]any{"quote_no": "B3", "delivery": "B5", "total": "Other!B2"}, want: `xlsx: model answered field "total" with "Other!B2", which is not on sheet Sheet1`},
		"outside range":  {answer: map[string]any{"quote_no": "B3", "delivery": "B5", "total": "B7"}, want: `xlsx: model answered field "total" with "B7", which is outside Sheet1!A1:B5`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := newExtractRun(t)
			path := quoteBook(t, r.dir, nil)
			f, err := excelize.OpenFile(path)
			require.NoError(t, err)
			_, err = f.NewSheet("Other")
			require.NoError(t, err)
			require.NoError(t, f.Save())
			require.NoError(t, f.Close())
			r.provider.answer = func(*llmpkg.ChatRequest) map[string]any { return tc.answer }
			cfg := quoteConfig(nil)
			if name == "outside range" {
				cfg["range"] = "A1:B5"
			}
			run := r.execute(cfg, testModel)
			require.EqualError(t, run.err, tc.want)
			assert.Equal(t, 1, run.exec.ExitCode())
			_, err = os.Stat(filepath.Join(r.dataDir, "xlsx"))
			assert.True(t, os.IsNotExist(err), "a failed step records nothing")
		})
	}
}

func TestExtractPinnedTypeFails(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	r.provider.answer = func(*llmpkg.ChatRequest) map[string]any {
		return map[string]any{"quote_no": "B3", "delivery": "B5", "total": "B3"}
	}
	run := r.execute(quoteConfig(nil), testModel)
	require.EqualError(t, run.err, `quote.xlsx Sheet1!B3: expected number, found "Q-2026-001"`)
}

func TestExtractCacheHit(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	first := r.execute(quoteConfig(nil), testModel)
	require.NoError(t, first.err)
	require.Equal(t, 1, r.provider.count())

	// Another quote in the same layout: the values changed, the cells did not.
	quoteBook(t, r.dir, map[string]any{"B3": "Q-2026-002", "B7": 99000})
	second := r.execute(quoteConfig(nil), testModel)
	require.NoError(t, second.err)
	assert.Equal(t, 1, r.provider.count(), "a known layout makes no model request")
	outputs := second.exec.GetOutputs()
	assert.Equal(t, "Q-2026-002", outputs["quote_no"])
	assert.Equal(t, int64(99000), outputs["total"])
	assert.Equal(t, sourceCache, outputs["source"])
	assert.Equal(t, "Extracted 3 fields from quote.xlsx Sheet1 (cache)\n", second.stdout.String())

	// cache: false asks every time.
	third := r.execute(quoteConfig(map[string]any{"cache": false}), testModel)
	require.NoError(t, third.err)
	assert.Equal(t, 2, r.provider.count())
	assert.Equal(t, sourceModel, third.exec.GetOutputs()["source"])
}

func TestExtractCacheHealsOnNewField(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, map[string]any{"A9": "担当者", "B9": "山田"})
	require.NoError(t, r.execute(quoteConfig(nil), testModel).err)
	require.Equal(t, 1, r.provider.count())

	schema := map[string]any{"type": "object", "properties": map[string]any{
		"quote_no": map[string]any{"type": "string", "description": "見積番号"},
		"delivery": map[string]any{"type": "string", "format": "date", "description": "納期"},
		"total":    map[string]any{"type": "number", "description": "合計金額"},
		"person":   map[string]any{"type": "string", "description": "担当者"},
	}}
	r.provider.answer = labelRight(map[string]string{"quote_no": "見積番号", "delivery": "納期", "total": "合計金額", "person": "担当者"})
	run := r.execute(quoteConfig(map[string]any{"schema": schema}), testModel)
	require.NoError(t, run.err)
	assert.Equal(t, 2, r.provider.count(), "a field the recording lacks asks the model again")
	assert.Equal(t, "山田", run.exec.GetOutputs()["person"])
	assert.Equal(t, sourceModel, run.exec.GetOutputs()["source"])

	again := r.execute(quoteConfig(map[string]any{"schema": schema}), testModel)
	require.NoError(t, again.err)
	assert.Equal(t, 2, r.provider.count(), "the healed recording covers the new field")
	assert.Equal(t, sourceCache, again.exec.GetOutputs()["source"])
}

func TestExtractCacheHealsOnRenamedLabel(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	require.NoError(t, r.execute(quoteConfig(nil), testModel).err)
	require.Equal(t, 1, r.provider.count())

	// The same shape, but the label beside the total now says something
	// else: the recorded cell may no longer be the total.
	quoteBook(t, r.dir, map[string]any{"A7": "税抜金額", "B7": 100000})
	run := r.execute(quoteConfig(nil), testModel)
	require.NoError(t, run.err)
	assert.Equal(t, 2, r.provider.count(), "a changed label asks the model again")
	assert.Equal(t, sourceModel, run.exec.GetOutputs()["source"])
	assert.Nil(t, run.exec.GetOutputs()["total"], "the scripted model finds no 合計金額 label now")
}

func TestExtractCacheHealsOnInstructionChange(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	require.NoError(t, r.execute(quoteConfig(nil), testModel).err)
	run := r.execute(quoteConfig(map[string]any{"instruction": "A quote; find the fields"}), testModel)
	require.NoError(t, run.err)
	assert.Equal(t, 2, r.provider.count(), "another instruction asks the model again")
}

func TestExtractNotCommittedOnFailure(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	r.provider.answer = func(*llmpkg.ChatRequest) map[string]any {
		return map[string]any{"quote_no": "B3", "delivery": "B5", "total": "B3"}
	}
	require.Error(t, r.execute(quoteConfig(nil), testModel).err)
	r.provider.answer = quoteAnswer
	run := r.execute(quoteConfig(nil), testModel)
	require.NoError(t, run.err)
	assert.Equal(t, 2, r.provider.count(), "nothing was recorded by the failed run")
	assert.Equal(t, sourceModel, run.exec.GetOutputs()["source"])
}

func TestExtractModelFailure(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	r.provider.fail = errors.New("boom")
	run := r.execute(quoteConfig(nil), testModel)
	require.ErrorContains(t, run.err, "model request failed: openai/test-model: ")
}

func TestExtractSendValuesFalse(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	quoteBook(t, r.dir, nil)
	run := r.execute(quoteConfig(map[string]any{"send_values": false}), testModel)
	require.NoError(t, run.err)
	user := r.provider.lastUserMessage()
	assert.True(t, strings.HasSuffix(user, "A7 [text]: 合計金額\nB7 [number]"), user)
	assert.NotContains(t, user, "123000")
	assert.NotContains(t, user, "2026-10-15")
	assert.Equal(t, int64(123000), run.exec.GetOutputs()["total"], "the value is still read from the cell")
}

func TestExtractCellCap(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	f := excelize.NewFile()
	for row := 1; row <= 1001; row++ {
		require.NoError(t, f.SetSheetRow("Sheet1", "A"+strconv.Itoa(row), &[]any{"label", row}))
	}
	require.NoError(t, f.SaveAs(filepath.Join(r.dir, "quote.xlsx")))
	require.NoError(t, f.Close())
	run := r.execute(quoteConfig(nil), testModel)
	require.EqualError(t, run.err, "quote.xlsx Sheet1: 2002 cells in Sheet1!A1:B1001 is more than 2000; set range to the part of the sheet that holds the fields")
	assert.Equal(t, 0, r.provider.count())
}

func TestExtractSecretInInstruction(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	r.secrets = map[string]string{"SHOP_TOKEN": "tok-12345"}
	quoteBook(t, r.dir, nil)
	run := r.execute(quoteConfig(map[string]any{"instruction": "Use tok-12345 to find the fields"}), testModel)
	require.EqualError(t, run.err, "xlsx: with.instruction contains the value of secret SHOP_TOKEN, which would be sent to the model")
	assert.Equal(t, 0, r.provider.count())
}

func TestExtractMasksSecretInListing(t *testing.T) {
	t.Parallel()
	r := newExtractRun(t)
	r.secrets = map[string]string{"SHOP_TOKEN": "tok-12345"}
	quoteBook(t, r.dir, map[string]any{"A9": "トークン", "B9": "tok-12345"})
	run := r.execute(quoteConfig(nil), testModel)
	require.NoError(t, run.err)
	user := r.provider.lastUserMessage()
	assert.Contains(t, user, "B9 [text]: ")
	assert.NotContains(t, user, "tok-12345", "a secret in a cell is masked before it reaches the model")
}

func TestExtractDryRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	quoteBook(t, dir, nil)
	require.NoError(t, dryRun(t, dir, opExtract, quoteConfig(nil)))
	require.ErrorContains(t, dryRun(t, dir, opExtract, quoteConfig(map[string]any{"sheet": "Nope"})), `field 'with.sheet': quote.xlsx: sheet "Nope" not found`)
	require.ErrorContains(t, dryRun(t, dir, opExtract, quoteConfig(map[string]any{"path": "none.xlsx"})), "field 'with.path': none.xlsx: workbook not found")
	require.NoError(t, dryRun(t, dir, opExtract, quoteConfig(map[string]any{"sheet": "${params.SHEET}"})), "a sheet still holding a reference is skipped")
}
