// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/spec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Dagu does not escape values inserted into a shell command, and a model's
// reply can carry whatever text the model read. Values whose shape the output
// schema pins down cannot carry shell syntax, and neither can a log path.
func TestModelOutputInRunWarnings(t *testing.T) {
	t.Parallel()

	const harness = `
  - id: agent
    action: harness.run
    with:
      provider: claude
      prompt: reply
    output_schema:
      type: object
      properties:
        summary: { type: string }
        approved: { type: boolean }
        attempts: { type: integer }
        severity: { type: string, enum: [low, high] }
`
	const chat = `
  - id: agent
    action: chat.completion
    with:
      provider: openai
      model: gpt-4o
      messages:
        - role: user
          content: reply
`
	const command = `
  - id: agent
    run: echo ok
    output: REPLY
`

	tests := []struct {
		name     string
		producer string
		consumer string
		want     string
	}{
		{name: "SingleQuoted", producer: harness, consumer: `run: ./page.sh '${agent.output.summary}'`, want: "${agent.output.summary}"},
		{name: "DoubleQuoted", producer: harness, consumer: `run: ./page.sh "${agent.output.summary}"`, want: "${agent.output.summary}"},
		{name: "Unquoted", producer: harness, consumer: `run: ./page.sh ${agent.output.summary}`, want: "${agent.output.summary}"},
		{name: "WholeOutput", producer: harness, consumer: `run: ./page.sh '${agent.output}'`, want: "${agent.output}"},
		{name: "MultilineRun", producer: harness, consumer: "run: |\n      set -e\n      ./page.sh '${agent.output.summary}'", want: "${agent.output.summary}"},
		{name: "ChatStep", producer: chat, consumer: `run: ./page.sh '${agent.output}'`, want: "${agent.output}"},
		{name: "StringInEnvIsSafe", producer: harness, consumer: "env:\n      - SUMMARY: ${agent.output.summary}\n    run: ./page.sh \"$SUMMARY\""},
		{name: "BooleanIsSafe", producer: harness, consumer: `run: test "${agent.output.approved}" = "true"`},
		{name: "IntegerIsSafe", producer: harness, consumer: `run: test "${agent.output.attempts}" -gt 1`},
		{name: "EnumIsSafe", producer: harness, consumer: `run: ./page.sh '${agent.output.severity}'`},
		{name: "StdoutIsALogPath", producer: harness, consumer: `run: cat '${agent.stdout}'`},
		{name: "ExitCodeIsSafe", producer: harness, consumer: `run: test '${agent.exit_code}' = 0`},
		{name: "EscapedIsLiteral", producer: harness, consumer: `run: echo '\${agent.output.summary}'`},
		{name: "CommandStepIsNotAModel", producer: command, consumer: `run: ./page.sh '${agent.output}'`},
		{name: "AgentInputIsNotAShell", producer: harness, consumer: "action: harness.run\n    with:\n      provider: claude\n      prompt: review ${agent.output.summary}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			yaml := "name: reply\nworking_dir: .\nsteps:" + tt.producer +
				"  - id: consumer\n    depends: [agent]\n    " + tt.consumer + "\n"
			dag, err := spec.LoadYAML(context.Background(), []byte(yaml))
			require.NoError(t, err)

			var warnings []string
			for _, warning := range dag.BuildWarnings {
				if strings.Contains(warning, "model output") {
					warnings = append(warnings, warning)
				}
			}
			if tt.want == "" {
				assert.Empty(t, warnings)
				return
			}
			require.Len(t, warnings, 1)
			assert.Contains(t, warnings[0], `DAG "reply": step "consumer": `+tt.want)
			assert.Contains(t, warnings[0], "env")
		})
	}
}
