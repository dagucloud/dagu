// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package browser

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	llmpkg "github.com/dagucloud/dagu/v2/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pagedOrders = `{
	"url": "https://shop.example.com/orders",
	"do": [{"extract": {
		"instruction": "The orders on this page",
		"schema": {"type": "object", "properties": {"title": {"type": "string"}, "orders": {"type": "array", "items": {"type": "string"}}}},
		"pages": {"next": "Click Next", "within": "100ms"}
	}}]
}`

var pageRows = regexp.MustCompile(`rows: ([^\n]*)`)

// listModel clicks the element an act asks for and extracts the rows the
// page it is sent shows, with the list's title.
func listModel(req *llmpkg.ChatRequest) (string, error) {
	text := lastUserText(req)
	properties, _ := req.Tools[0].Function.Parameters["properties"].(map[string]any)
	if _, ok := properties["elementId"]; ok {
		return `{"elementId":"/html/body/button"}`, nil
	}
	rows := []string{}
	if match := pageRows.FindStringSubmatch(text); match != nil && match[1] != "" {
		rows = strings.Split(match[1], ",")
	}
	data, err := json.Marshal(map[string]any{"title": "Orders", "orders": rows})
	return string(data), err
}

// An extract with pages reads every page of a list, turning each with its
// next act, and ends where the next button is gone. A later run replays
// the click without a model call.
func TestExtractReadsEveryPage(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, listModel)
	run.engine.showList("rows: A,B", "rows: C,D", "rows: E")
	first := run.execute(pagedOrders, nil)
	require.NoError(t, first.err)
	assert.Equal(t, map[string]any{"title": "Orders", "orders": []any{"A", "B", "C", "D", "E"}}, extracted(first.exec))
	assert.Equal(t, []string{"Click Next"}, run.engine.actInstructions(), "the click the model made turns the later pages")

	run.engine.showList("rows: A,B", "rows: C,D", "rows: E")
	second := run.execute(pagedOrders, nil)
	require.NoError(t, second.err)
	assert.Equal(t, map[string]any{"title": "Orders", "orders": []any{"A", "B", "C", "D", "E"}}, extracted(second.exec))
	assert.Len(t, run.engine.actInstructions(), 1, "the later run asks no model to turn a page")
}

// A next button left on the last page that changes nothing ends the list,
// and so does a page that gives the items the page before gave.
func TestExtractPagesEndWhereNothingChanges(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, listModel)
	run.engine.lastPageKeepsNext = true
	run.engine.showList("rows: A,B", "rows: C")
	still := run.execute(pagedOrders, nil)
	require.NoError(t, still.err)
	assert.Equal(t, []any{"A", "B", "C"}, extracted(still.exec)["orders"])

	run.engine.showList("rows: A,B", "rows: A,B\nupdated 10:01")
	repeated := run.execute(pagedOrders, nil)
	require.NoError(t, repeated.err)
	assert.Equal(t, []any{"A", "B"}, extracted(repeated.exec)["orders"])
}

// A list longer than pages.max is read up to it, with a warning.
func TestExtractPagesStopAtMax(t *testing.T) {
	t.Parallel()

	run := newTestRun(t, listModel)
	run.engine.showList("rows: A", "rows: B", "rows: C")
	limited := run.execute(strings.Replace(pagedOrders, `"within": "100ms"`, `"within": "100ms", "max": 2`, 1), nil)
	require.NoError(t, limited.err)
	assert.Equal(t, []any{"A", "B"}, extracted(limited.exec)["orders"])
	assert.Contains(t, limited.stderr.String(), "extract read pages.max, 2 pages, and read no page after it")
}

func TestExtractPagesValidation(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		pages, properties, message string
	}{
		"no array":  {`{"next": "Click Next"}`, `{"title": {"type": "string"}}`, "extract pages collects the items of the schema's top-level array properties, and the schema lists none"},
		"one page":  {`{"next": "Click Next", "max": 1}`, `{"rows": {"type": "array"}}`, "max"},
		"blank":     {`{"next": " "}`, `{"rows": {"type": "array"}}`, "extract pages.next must not be empty"},
		"variable":  {`{"next": "Click %page%"}`, `{"rows": {"type": "array"}}`, "extract.pages.next references %page%, which is not in with.variables"},
		"no next":   {`{"max": 3}`, `{"rows": {"type": "array"}}`, "next"},
		"bad delay": {`{"next": "Click Next", "within": "soon"}`, `{"rows": {"type": "array"}}`, "pages.within"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var with map[string]any
			require.NoError(t, json.Unmarshal([]byte(`{"do": [{"extract": {"instruction": "Rows", "schema": {"type": "object", "properties": `+test.properties+`}, "pages": `+test.pages+`}}]}`), &with))
			cfg, err := parseConfig(with)
			if err == nil {
				err = cfg.validate()
			}
			require.ErrorContains(t, err, test.message)
		})
	}
}
