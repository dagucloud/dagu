// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentrun

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeStrictRejectsADuplicateField(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"top level": `{"a":1,"a":2}`,
		"nested":    `{"a":{"b":1,"b":2}}`,
		"in a list": `{"a":[{"b":1,"b":2}]}`,
	} {
		_, err := DecodeStrict([]byte(raw))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "duplicate field")
	}

	value, err := DecodeStrict([]byte(`{"a":12345678901234567890,"b":[1,{"c":null}]}`))
	require.NoError(t, err)
	assert.Equal(t, "12345678901234567890", value.(map[string]any)["a"].(interface{ String() string }).String())

	_, err = DecodeStrict([]byte(`{"a":1} {"b":2}`))
	require.Error(t, err, "data after the value")
	_, err = DecodeStrict([]byte(`{"a":`))
	require.Error(t, err)
}

func TestDecodeStrictInvalidUTF8(t *testing.T) {
	t.Parallel()

	_, err := DecodeStrict([]byte("{\"a\":\"\xff\"}"))
	require.Error(t, err)

	value, err := DecodeStrict([]byte(`{"a":"é"}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"a": "é"}, value)
}

const closedSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "verdict": {"type": "string", "enum": ["accept", "reject"]},
    "score": {"type": "integer"}
  },
  "required": ["verdict"]
}`

func newSchema(t *testing.T) *ResultSchema {
	t.Helper()
	var schema map[string]any
	data, err := DecodeStrict([]byte(closedSchema))
	require.NoError(t, err)
	schema = data.(map[string]any)
	// The schema decoder keeps json.Number; the loader hands plain numbers.
	schema["properties"].(map[string]any)["score"] = map[string]any{"type": "integer"}
	result, err := NewResultSchema(schema)
	require.NoError(t, err)
	return result
}

func TestResultSchemaCheck(t *testing.T) {
	t.Parallel()

	schema := newSchema(t)
	line, err := schema.Check([]byte(`{"score": 3, "verdict": "accept"}`))
	require.Nil(t, err)
	assert.Equal(t, `{"score":3,"verdict":"accept"}`, string(line), "one line, keys in sorted order")

	for name, tc := range map[string]struct {
		raw    string
		reason ResultReason
		text   string
	}{
		"empty":            {"", ReasonNoResult, ""},
		"not json":         {`{"verdict":`, ReasonInvalidJSON, ""},
		"duplicate field":  {`{"verdict":"accept","verdict":"reject"}`, ReasonInvalidJSON, `duplicate field "verdict"`},
		"unknown field":    {`{"verdict":"accept","extra":1}`, ReasonSchemaMismatch, "extra"},
		"wrong type":       {`{"verdict":"accept","score":"high"}`, ReasonSchemaMismatch, "score"},
		"enum":             {`{"verdict":"maybe"}`, ReasonSchemaMismatch, "maybe"},
		"required missing": {`{"score":1}`, ReasonSchemaMismatch, "verdict"},
		"not an object":    {`[1]`, ReasonSchemaMismatch, "object"},
	} {
		_, got := schema.Check([]byte(tc.raw))
		require.NotNil(t, got, name)
		assert.Equal(t, tc.reason, got.Reason, name)
		assert.Contains(t, got.Error(), tc.text, name)
	}
}

// format is a note and is not enforced.
func TestResultSchemaDoesNotEnforceFormat(t *testing.T) {
	t.Parallel()

	schema, err := NewResultSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"when": map[string]any{"type": "string", "format": "date-time"}},
	})
	require.NoError(t, err)
	line, resultErr := schema.Check([]byte(`{"when":"yesterday"}`))
	require.Nil(t, resultErr)
	assert.Equal(t, `{"when":"yesterday"}`, string(line))
}

func TestResultErrorCutsTheValueAt80Bytes(t *testing.T) {
	t.Parallel()

	schema := newSchema(t)
	_, err := schema.Check([]byte(`{"verdict":"` + strings.Repeat("x", 300) + `"}`))
	require.NotNil(t, err)
	assert.Less(t, len(err.Error()), 400)
	assert.NotContains(t, err.Error(), strings.Repeat("x", 250))
	assert.Contains(t, err.Error(), "(value: ")
}

func integerSchema(t *testing.T, field map[string]any) *ResultSchema {
	t.Helper()
	schema, err := NewResultSchema(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"n": field},
	})
	require.NoError(t, err)
	return schema
}

// The check must see the integer that the step emits, not a float64 copy
// rounded to 53 bits.
func TestCheckExactIntegers(t *testing.T) {
	t.Parallel()

	bounded := integerSchema(t, map[string]any{"type": "integer", "maximum": 1 << 53})
	line, err := bounded.Check([]byte(`{"n":9007199254740992}`))
	require.Nil(t, err)
	assert.Equal(t, `{"n":9007199254740992}`, string(line))

	_, err = bounded.Check([]byte(`{"n":9007199254740993}`))
	require.NotNil(t, err, "above the maximum by one")
	assert.Equal(t, ReasonSchemaMismatch, err.Reason)

	minimum := integerSchema(t, map[string]any{"type": "integer", "minimum": 18446744073709551615.0})
	_, err = minimum.Check([]byte(`{"n":18446744073709551614}`))
	require.NotNil(t, err, "a uint64 below the minimum")
	assert.Equal(t, ReasonSchemaMismatch, err.Reason)

	open := integerSchema(t, map[string]any{"type": "integer"})
	line, err = open.Check([]byte(`{"n":12345678901234567891}`))
	require.Nil(t, err)
	assert.Equal(t, `{"n":12345678901234567891}`, string(line), "every digit is kept")

	_, err = open.Check([]byte(`{"n":123456789012345678901234567890}`))
	require.NotNil(t, err, "no exact check above 64 bits")
	assert.Equal(t, ReasonSchemaMismatch, err.Reason)

	_, err = open.Check([]byte(`{"n":1.5}`))
	require.NotNil(t, err)
	assert.Equal(t, ReasonSchemaMismatch, err.Reason)

	_, err = open.Check([]byte(`{"n":1e400}`))
	require.NotNil(t, err, "a number that no float64 holds")
	assert.Equal(t, ReasonInvalidJSON, err.Reason)

	number := integerSchema(t, map[string]any{"type": "number"})
	line, err = number.Check([]byte(`{"n":2.5e3}`))
	require.Nil(t, err)
	assert.Equal(t, `{"n":2.5e3}`, string(line))
}

func TestNormalizeToolName(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"mcp__dagu__refund":          "refund",
		"mcp__docs__search":          "docs_search",
		"mcp__github__create__issue": "github_create__issue",
		"mcp__broken":                "mcp__broken",
		"Read":                       "read",
		"Write":                      "write",
		"Edit":                       "edit",
		"Grep":                       "search",
		"Glob":                       "search",
		"Bash":                       "shell",
		"WebFetch":                   "WebFetch",
		"Skill":                      "Skill",
	} {
		assert.Equal(t, want, NormalizeToolName(in), in)
	}
}
