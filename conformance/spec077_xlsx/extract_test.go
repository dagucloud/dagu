// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec077_xlsx_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// listingLine is one cell of the listing the step sends: the address, the
// kind and hints, and the text when values are sent.
var listingLine = regexp.MustCompile(`^([A-Z]+)(\d+)(?::[A-Z]+\d+)? \[[^\]]*\](?:: (.*))?$`)

// formModel answers like a model reading the listing: for each requested
// field it finds the cell whose text is the field's description, or its
// name, and names the cell to its right; a field with no such label is
// null. It counts requests and keeps the last user message.
type formModel struct {
	mu       sync.Mutex
	requests int
	lastUser string
}

func startFormModel(t *testing.T) (*formModel, string) {
	t.Helper()
	model := &formModel{}
	server := httptest.NewServer(http.HandlerFunc(model.serve))
	t.Cleanup(server.Close)
	return model, server.URL
}

func (m *formModel) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func (m *formModel) userMessage() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastUser
}

func (m *formModel) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name       string `json:"name"`
				Parameters struct {
					Properties map[string]struct {
						Description string `json:"description"`
					} `json:"properties"`
				} `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Tools) == 0 {
		http.Error(w, "expected a tool request", http.StatusBadRequest)
		return
	}
	user := ""
	for _, message := range req.Messages {
		if message.Role == "user" {
			user = message.Content
		}
	}
	// The cell to the right of each label.
	valueCells := map[string]string{}
	for line := range strings.SplitSeq(user, "\n") {
		match := listingLine.FindStringSubmatch(line)
		if match == nil || match[3] == "" {
			continue
		}
		valueCells[strings.TrimSpace(match[3])] = nextColumn(match[1]) + match[2]
	}
	// The step wraps each property's description in a sentence; the label
	// is the listing text that sentence, or the property name, contains.
	answer := map[string]any{}
	for name, property := range req.Tools[0].Function.Parameters.Properties {
		answer[name] = nil
		for label, cell := range valueCells {
			if strings.Contains(property.Description, label) || label == name {
				answer[name] = cell
				break
			}
		}
	}
	m.mu.Lock()
	m.requests++
	m.lastUser = user
	m.mu.Unlock()

	arguments, _ := json.Marshal(answer)
	quoted, _ := json.Marshal(string(arguments))
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"`+req.Tools[0].Function.Name+`","arguments":`+string(quoted)+`}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`)
}

// nextColumn returns the column letter after the given one, A to B; the
// listings here never reach Z.
func nextColumn(column string) string {
	last := column[len(column)-1]
	return column[:len(column)-1] + string(last+1)
}

type extracted struct {
	QuoteNo  string            `json:"quote_no"`
	Delivery string            `json:"delivery"`
	Total    float64           `json:"total"`
	Contact  string            `json:"contact"`
	Cells    map[string]string `json:"cells"`
	Sheet    string            `json:"sheet"`
	Source   string            `json:"source"`
	Warnings []string          `json:"warnings"`
}

func extractEnv(modelURL, home string) []string {
	return []string{"LLM_BASE_URL=" + modelURL, "DAGU_HOME=" + home}
}

// The cells the model names are read with their types; a field the sheet
// lacks is absent.
func TestXlsxExtract(t *testing.T) {
	t.Parallel()
	model, modelURL := startFormModel(t)
	dagu := harness.NewRunner(t)
	dagu.RunWithEnv([]string{"LLM_BASE_URL=" + modelURL}, "start", "extract.yaml").ExpectExitCode(0)
	var out extracted
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, 1, model.count())
	require.Equal(t, "Q-2026-001", out.QuoteNo)
	require.Equal(t, "2026-10-15", out.Delivery, "a date column reads as ISO text")
	require.Equal(t, float64(123000), out.Total, "a number cell reads as a number")
	require.Equal(t, map[string]string{"quote_no": "Sheet1!B2", "delivery": "Sheet1!B3", "total": "Sheet1!B4", "terms": ""}, out.Cells)
	require.Equal(t, "Sheet1", out.Sheet)
	require.Equal(t, "model", out.Source)
	require.Empty(t, out.Warnings)
	require.Contains(t, model.userMessage(), "A2 [text]: 見積番号")
	require.Contains(t, model.userMessage(), "B4 [number]: 123000")
}

// A repeated layout is read from the cache; a schema with a new field, and
// a changed layout, ask the model again.
func TestXlsxExtractCache(t *testing.T) {
	t.Parallel()
	model, modelURL := startFormModel(t)
	home := filepath.Join(t.TempDir(), "dagu")
	dagu := harness.NewRunner(t)
	env := extractEnv(modelURL, home)

	dagu.RunWithEnv(env, "start", "extract.yaml").ExpectExitCode(0)
	require.Equal(t, 1, model.count())

	again := harness.NewRunner(t)
	again.RunWithEnv(env, "start", "extract.yaml").ExpectExitCode(0)
	var out extracted
	readJSON(t, again, "out.json", &out)
	require.Equal(t, 1, model.count(), "the same layout makes no model request")
	require.Equal(t, "cache", out.Source)
	require.Equal(t, "Q-2026-001", out.QuoteNo)

	more := harness.NewRunner(t)
	more.RunWithEnv(env, "start", "extract_more_fields.yaml").ExpectExitCode(0)
	readJSON(t, more, "out.json", &out)
	require.Equal(t, 2, model.count(), "a field the cached entry lacks asks again")
	require.Equal(t, "model", out.Source)
	require.Equal(t, "佐藤", out.Contact)
	require.Equal(t, "Q-2026-002", out.QuoteNo, "the same template with other values")

	relayout := harness.NewRunner(t)
	relayout.RunWithEnv(env, "start", "extract_relayout.yaml").ExpectExitCode(0)
	readJSON(t, relayout, "out.json", &out)
	require.Equal(t, 3, model.count(), "a changed layout asks again")
	require.Equal(t, "Sheet1!B3", out.Cells["quote_no"], "the cells follow the new layout")
	require.Equal(t, float64(45000), out.Total)
}

// dagu xlsx cache clear and dagu rm --history drop the cached cells.
func TestXlsxExtractCacheClear(t *testing.T) {
	t.Parallel()
	model, modelURL := startFormModel(t)
	home := filepath.Join(t.TempDir(), "dagu")
	env := extractEnv(modelURL, home)
	dagu := harness.NewRunner(t)
	dagu.RunWithEnv(env, "start", "extract.yaml").ExpectExitCode(0)
	require.Equal(t, 1, model.count())

	cleared := dagu.RunWithEnv(env, "xlsx", "cache", "clear", "quote-extract", "--step", "fields")
	cleared.ExpectExitCode(0)
	cleared.ExpectStdout(`Removed xlsx replay cache for step "fields" of DAG "quote-extract"` + "\n")
	dagu.RunWithEnv(env, "xlsx", "cache", "clear", "quote-extract").ExpectStdout(`No xlsx replay cache for DAG "quote-extract"` + "\n")

	second := harness.NewRunner(t)
	second.RunWithEnv(env, "start", "extract.yaml").ExpectExitCode(0)
	require.Equal(t, 2, model.count(), "a cleared cache asks the model again")

	second.RunWithEnv(env, "rm", "--history", "--force", "quote-extract").ExpectExitCode(0)
	third := harness.NewRunner(t)
	third.RunWithEnv(env, "start", "extract.yaml").ExpectExitCode(0)
	require.Equal(t, 3, model.count(), "removing the history removes the cache too")
}

// send_values: false keeps values out of the request; the cells are still
// read on the host.
func TestXlsxExtractSendValuesFalse(t *testing.T) {
	t.Parallel()
	model, modelURL := startFormModel(t)
	dagu := harness.NewRunner(t)
	dagu.RunWithEnv([]string{"LLM_BASE_URL=" + modelURL}, "start", "extract_no_values.yaml").ExpectExitCode(0)
	var out extracted
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, float64(123000), out.Total)
	require.Equal(t, "Q-2026-001", out.QuoteNo)
	user := model.userMessage()
	require.Contains(t, user, "A2 [text]: 合計金額", "labels are sent")
	require.Regexp(t, `(?m)^B2 \[number\]$`, user, "a value cell is listed as its kind only")
	require.NotContains(t, user, "123000")
}

// A secret in the instruction stops the step before any request.
func TestXlsxExtractSecretInInstruction(t *testing.T) {
	t.Parallel()
	model, modelURL := startFormModel(t)
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"LLM_BASE_URL=" + modelURL, "SUPPLIER_TOKEN=tok-12345"}, "start", "extract_secret.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("with.instruction contains the value of secret SUPPLIER_TOKEN")
	result.ExpectStderrNotContains("tok-12345")
	require.Zero(t, model.count())
	dagu.ExpectNoFile("after.txt")
}

func TestXlsxExtractValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ file, message string }{
		{"validation_extract_missing_llm.yaml", "xlsx.extract needs a model: set llm at the DAG level or with.llm on the step"},
		{"validation_extract_schema_not_object.yaml", "schema must have type: object"},
		{"validation_extract_no_instruction.yaml", "extract requires with.instruction"},
		{"validation_extract_llm_on_read.yaml", "with.llm is not valid for xlsx.read"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.message)
		})
	}
}

// with.llm on the step gives the model when the DAG has no llm block.
func TestXlsxExtractStepLLM(t *testing.T) {
	t.Parallel()
	model, modelURL := startFormModel(t)
	dagu := harness.NewRunner(t)
	dagu.RunWithEnv([]string{"LLM_BASE_URL=" + modelURL}, "start", "extract_step_llm.yaml").ExpectExitCode(0)
	var out extracted
	readJSON(t, dagu, "out.json", &out)
	require.Equal(t, 1, model.count())
	require.Equal(t, "Q-2026-009", out.QuoteNo)
	require.Equal(t, float64(500), out.Total)
}
