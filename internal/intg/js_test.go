// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/test"
)

func TestJSRun(t *testing.T) {
	t.Parallel()

	t.Run("CapturesJSONOutput", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - id: shape
    action: js.run
    with:
      input:
        html: '<a href="/a">x</a><a href="/b">y</a>'
      script: |
        const urls = [];
        for (const m of input.html.matchAll(/href="([^"]+)"/g)) {
          urls.push(new URL(m[1], "https://example.com").href);
        }
        return urls;
    output: LINKS
`)
		dag.Agent().RunSuccess(t)

		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"LINKS": "[\n  \"https://example.com/a\",\n  \"https://example.com/b\"\n]",
		})
	})

	t.Run("CapturesStringOutput", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - id: greet
    action: js.run
    with:
      script: return "hello"
    output: GREETING
`)
		dag.Agent().RunSuccess(t)

		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertOutputs(t, map[string]any{"GREETING": "hello"})
	})

	t.Run("ChainsThroughInput", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - id: produce
    action: js.run
    with:
      script: |
        return {count: 2, items: ["a", "b"]}
    output: DATA
  - id: consume
    depends: [produce]
    action: js.run
    with:
      input: ${produce.output}
      script: return input.items.map((item) => item + input.count).join(",")
    output: RESULT
`)
		dag.Agent().RunSuccess(t)

		dag.AssertLatestStatus(t, ir.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "a2,b2",
		})
	})

	t.Run("ScriptErrorFailsStep", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - id: boom
    action: js.run
    with:
      script: throw new TypeError("bad input")
`)
		dag.Agent().RunError(t)

		dag.AssertLatestStatus(t, ir.Failed)
	})
}
