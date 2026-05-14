// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
)

func TestJQExecutor(t *testing.T) {
	t.Parallel()

	t.Run("MultipleOutputsWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-array-raw
    action: jq.filter
    with:
      filter: '.data[]'
      data: |
        { "data": [1, 2, 3] }
      raw: true
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "1\n2\n3",
		})
	})

	t.Run("MultipleOutputsWithRawFalse", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-array-json
    action: jq.filter
    with:
      filter: '.data[]'
      data: |
        { "data": [1, 2, 3] }
      raw: false
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "1\n2\n3",
		})
	})

	t.Run("StringOutputWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-strings-raw
    action: jq.filter
    with:
      filter: '.messages[]'
      data: |
        { "messages": ["hello", "world"] }
      raw: true
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "hello\nworld",
		})
	})

	t.Run("StringOutputWithRawFalse", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-strings-json
    action: jq.filter
    with:
      filter: '.messages[]'
      data: |
        { "messages": ["hello", "world"] }
      raw: false
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "\"hello\"\n\"world\"",
		})
	})

	t.Run("TSVOutputWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-tsv
    action: jq.filter
    with:
      filter: '.data[] | [., 100 * .] | @tsv'
      data: |
        { "data": [1, 2, 3] }
      raw: true
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "1\t100\n2\t200\n3\t300",
		})
	})

	t.Run("SingleStringWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-single-string-raw
    action: jq.filter
    with:
      filter: .foo
      data: |
        {"foo": "bar"}
      raw: true
    output: RESULT
`)

		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "bar",
		})
	})

	t.Run("SingleStringWithRawFalse", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-single-string-json
    action: jq.filter
    with:
      filter: .foo
      data: |
        {"foo": "bar"}
      raw: false
    output: RESULT
`)

		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": `"bar"`,
		})
	})

	t.Run("SingleNumberWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-single-number-raw
    action: jq.filter
    with:
      filter: .value
      data: |
        {"value": 42}
      raw: true
    output: RESULT
`)

		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "42",
		})
	})

	t.Run("SingleBooleanWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-single-boolean-raw
    action: jq.filter
    with:
      filter: .enabled
      data: |
        {"enabled": true, "disabled": false}
      raw: true
    output: ENABLED
`)

		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"ENABLED": "true",
		})
	})

	t.Run("NullValueWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-null-raw
    action: jq.filter
    with:
      filter: .value
      data: |
        {"value": null}
      raw: true
    output: RESULT
`)

		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		// Null values output empty string, but the output variable still exists
		// So we check that it contains an empty value
		dag.AssertOutputs(t, map[string]any{
			"RESULT": test.Contains("RESULT="),
		})
	})

	t.Run("ObjectWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-object-raw
    action: jq.filter
    with:
      filter: .user
      data: |
        {"user": {"name": "John", "age": 30}}
      raw: true
    output: RESULT
`)

		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		// In raw mode, object is output as compact JSON (key order not guaranteed)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": []test.Contains{
				test.Contains(`"name":"John"`),
				test.Contains(`"age":30`),
			},
		})
	})

	t.Run("InputFromFileWithStepRef", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
steps:
  - id: producer
    run: 'echo ''{"items": [{"name": "a"}, {"name": "b"}]}'''
    output: PRODUCER_OUT

  - id: filter
    depends:
      - producer
    action: jq.filter
    with:
      filter: '.items[] | .name'
      data: "file://${producer.stdout}"
      raw: true
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "a\nb",
		})
	})

	t.Run("ConfigInputWithStepRef", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
steps:
  - id: producer
    run: 'echo ''{"items": [{"name": "a"}, {"name": "b"}]}'''
    output: PRODUCER_OUT

  - id: filter
    depends:
      - producer
    action: jq.filter
    with:
      filter: '.items[] | .name'
      raw: true
      input: "${producer.stdout}"
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "a\nb",
		})
	})

	t.Run("ConfigInputWithRawFalse", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
steps:
  - id: producer
    run: 'echo ''{"items": [{"name": "a"}, {"name": "b"}]}'''
    output: PRODUCER_OUT

  - id: filter
    depends:
      - producer
    action: jq.filter
    with:
      filter: '.items[] | .name'
      raw: false
      input: "${producer.stdout}"
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "\"a\"\n\"b\"",
		})
	})

	t.Run("ConfigInputLargePayload", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		// Generate a JSON array with 100 items via a shell command
		dag := th.DAG(t, `
type: graph
steps:
  - id: producer
    run: |
      python3 -c "import json; print(json.dumps({'items': [{'id': i, 'name': f'item-{i}'} for i in range(100)]}))"
    output: PRODUCER_OUT

  - id: filter
    depends:
      - producer
    action: jq.filter
    with:
      filter: '[.items | length] | .[0]'
      raw: true
      input: "${producer.stdout}"
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "100",
		})
	})

	t.Run("ConfigInputNestedQuery", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
steps:
  - id: producer
    run: 'echo ''{"data": {"users": [{"name": "Alice", "email": "alice@example.com"}, {"name": "Bob", "email": "bob@example.com"}]}}'''
    output: PRODUCER_OUT

  - id: filter
    depends:
      - producer
    action: jq.filter
    with:
      filter: '.data.users[] | .name'
      raw: true
      input: "${producer.stdout}"
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "Alice\nBob",
		})
	})

	t.Run("StringWithSpecialCharsWithRawTrue", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: extract-special-chars-raw
    action: jq.filter
    with:
      filter: .message
      data: |
        {"message": "hello\nworld\ttab"}
      raw: true
    output: RESULT
`)

		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "hello\nworld\ttab",
		})
	})
}
