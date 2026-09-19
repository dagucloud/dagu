// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"fmt"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
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

		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "hello\nworld\ttab",
		})
	})
}

func TestJQExecutorArgs(t *testing.T) {
	t.Parallel()

	t.Run("ParamsAndLiteralsAsVariables", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `params:
  - WHO: "World"
steps:
  - name: greet
    action: jq.filter
    with:
      filter: '$greeting + ", " + $who + "!"'
      data: '{}'
      raw: true
      args:
        greeting: "Hello"
        who: ${WHO}
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "Hello, World!",
		})
	})

	t.Run("StepOutputAsVariable", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, fmt.Sprintf(`steps:
  - id: producer
    run: %q
    output: PRODUCED

  - name: consume
    depends: [producer]
    action: jq.filter
    with:
      filter: '.items[] | select(. > ($min | tonumber))'
      data: '{"items": [1, 5, 40, 50]}'
      raw: true
      args:
        min: ${producer.output}
    output: RESULT
`, test.Output("42")))
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "50",
		})
	})

	t.Run("TypedArgValues", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: typed
    action: jq.filter
    with:
      filter: 'if $enabled then .items[] | select(. > $threshold) else empty end'
      data: '{"items": [1, 5, 10]}'
      raw: true
      args:
        enabled: true
        threshold: 4
    output: RESULT
`)
		agent := dag.Agent()

		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "5\n10",
		})
	})
}

func TestJQArgResolution(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		config string
		want   string
	}{
		{
			name: "LiteralDollar",
			config: `filter: '$text'
      args:
        text: '\$who'`,
			want: "$who",
		},
		{
			name: "NameCollision",
			config: `filter: '$who'
      args:
        who: Alice`,
			want: "Alice",
		},
		{
			name: "EmptyArgs",
			config: `filter: '"${env.who}"'
      args: {}`,
			want: "${env.who}",
		},
		{
			name:   "NoArgs",
			config: `filter: '"${env.who}"'`,
			want:   "World",
		},
		{
			name: "Multiline",
			config: `filter: |
        $who |
        ascii_upcase
      args:
        who: Alice`,
			want: "ALICE",
		},
		{
			name: "NestedValues",
			config: `filter: '[$cfg.name, $cfg.literal, ($cfg.count | type), ($cfg.enabled | type), $items[0]] | join("|")'
      args:
        cfg:
          name: ${env.who}
          literal: '\$who'
          count: 4
          enabled: true
        items: ['${env.who}']`,
			want: "World|$who|number|boolean|World",
		},
		{
			name: "LargeNegativeInteger",
			config: `filter: '$n'
      args:
        n: -2147483649`,
			want: "-2147483649",
		},
		{
			name: "NumericReference",
			config: `filter: '.items[] | select(. > ($minimum | tonumber))'
      args:
        minimum: ${env.MINIMUM}`,
			want: "5\n10",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			th := test.Setup(t)
			dag := th.DAG(t, `env:
  who: World
  MINIMUM: '4'
steps:
  - id: filter
    action: jq.filter
    with:
      data: '{"items": [1, 5, 10]}'
      raw: true
      `+tc.config+`
    output: RESULT
`)
			dag.Agent().RunSuccess(t)
			dag.AssertOutputs(t, map[string]any{"RESULT": tc.want})
		})
	}
}

func TestJQLegacyArgs(t *testing.T) {
	t.Parallel()

	th := test.Setup(t)
	dag := th.DAG(t, `env:
  who: World
steps:
  - id: filter
    type: jq
    config:
      raw: true
      args:
        who: Alice
    command: '$who'
    script: '{}'
    output: RESULT
`)
	dag.Agent().RunSuccess(t)
	dag.AssertOutputs(t, map[string]any{"RESULT": "Alice"})
}
