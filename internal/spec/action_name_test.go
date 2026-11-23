// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each builtin action loads into a step that ActionName names it from. The
// fixtures must cover every registered action, so a new action fails here
// until it has one.
func TestActionNameRoundTrip(t *testing.T) {
	t.Parallel()

	// withFields maps each action to its with field as a YAML flow mapping.
	withFields := map[string]string{
		"a2a.get_task":        "{agent_url: https://agents.example.com/a, task_id: t}",
		"a2a.send":            "{agent_url: https://agents.example.com/a, message: m}",
		"artifact.list":       "",
		"artifact.read":       "{path: a.txt}",
		"artifact.write":      "{path: a.txt, content: x}",
		"archive.create":      "{source: dir, destination: a.tar.gz}",
		"archive.extract":     "{source: a.tar.gz, destination: dir}",
		"archive.list":        "{source: a.tar.gz}",
		"browser.extract":     "{url: https://example.com, instruction: The title, schema: {type: object}, llm: {provider: openai, model: gpt-5}}",
		"browser.run":         "{url: https://example.com, do: [{act: Sign in}], llm: {provider: openai, model: gpt-5}}",
		"chat.completion":     "{provider: openai, model: gpt-5, prompt: hi}",
		"clickhouse.query":    "{url: http://localhost:8123, query: SELECT 1}",
		"computer.extract":    "{instruction: The title, schema: {type: object}, llm: {provider: openai, model: gpt-5}}",
		"computer.run":        "{do: [{launch: notepad.exe}]}",
		"container.run":       "{image: alpine, command: echo}",
		"dag.enqueue":         "{dag: child}",
		"dag.run":             "{dag: child}",
		"data.convert":        "{from: csv, to: json, data: a}",
		"data.filter":         "{from: json, data: {a: 1}, where: {a: {max: 3}}}",
		"data.pick":           "{from: json, select: .a, data: {a: 1}}",
		"decision.evaluate":   "{provider: openrouter, model: m, state: s, questions: {q: {type: noul, instructions: Q}}}",
		"docker.run":          "{image: alpine}",
		"exec":                "{command: echo}",
		"file.copy":           "{source: a, destination: b}",
		"file.delete":         "{path: a}",
		"file.list":           "{path: .}",
		"file.mkdir":          "{path: d}",
		"file.move":           "{source: a, destination: b}",
		"file.read":           "{path: a}",
		"file.stat":           "{path: a}",
		"file.write":          "{path: a, content: x}",
		"git.checkout":        "{repository: https://github.com/a/b, path: b}",
		"git.worktree.add":    "{branch: feature}",
		"git.worktree.remove": "{branch: feature}",
		"harness.run":         "{provider: codex, prompt: review}",
		"http.request":        "{method: GET, url: https://example.com}",
		"human.task":          "{prompt: Approve?}",
		"jq.filter":           "{filter: ., data: {a: 1}}",
		"js.run":              `{script: "1"}`,
		"k8s.run":             "{image: alpine}",
		"kubernetes.run":      "{image: alpine}",
		"log.write":           "{message: hi}",
		"mail.organize":       `{mailbox: ops@example.com, emails: ["1"], mark: read}`,
		"mail.search":         "{mailbox: ops@example.com}",
		"mail.send":           "{to: a@example.com, subject: s, message: b}",
		"mcp.call":            "{url: https://mcp.example.com/mcp, tool: scrape}",
		"noop":                "",
		"outputs.write":       "{values: {a: 1}}",
		"postgres.import":     "{dsn: postgres://localhost/db, import: {input_file: a.csv, table: t}}",
		"postgres.query":      "{dsn: postgres://localhost/db, query: SELECT 1}",
		"router.route":        "{value: x, routes: {x: [step]}}",
		"s3.delete":           "{bucket: b, key: k}",
		"s3.download":         "{bucket: b, key: k, destination: f}",
		"s3.list":             "{bucket: b}",
		"s3.upload":           "{bucket: b, key: k, source: f}",
		"sftp.download":       "{host: app, source: a, destination: b}",
		"sftp.upload":         "{host: app, source: a, destination: b}",
		"sqlite.import":       "{dsn: file:a.db, import: {input_file: a.csv, table: t}}",
		"sqlite.query":        "{dsn: file:a.db, query: SELECT 1}",
		"ssh.run":             "{host: app, command: echo}",
		"state.delete":        "{key: k}",
		"state.diff":          "{key: k, value: v}",
		"state.get":           "{key: k}",
		"state.list":          "",
		"state.set":           "{key: k, value: v}",
		"template.render":     "{template: hi}",
		"wait.duration":       "{duration: 1s}",
		"wait.file":           "{path: a}",
		"wait.http":           "{url: https://example.com}",
		"wait.until":          `{until: "09:00"}`,
		"xlsx.append":         "{path: a.xlsx, input: b.csv}",
		"xlsx.convert":        "{path: a.xlsx, output: a.csv}",
		"xlsx.extract":        "{path: a.xlsx, instruction: Find the total, schema: {type: object}, llm: {provider: openai, model: gpt-5}}",
		"xlsx.info":           "{path: a.xlsx}",
		"xlsx.list_sheets":    "{path: a.xlsx}",
		"xlsx.read":           "{path: a.xlsx}",
		"xlsx.sheet":          "{path: a.xlsx, operation: add, sheet: s}",
		"xlsx.update_rows":    `{path: a.xlsx, key: k, rows: "[]", set: {s: s}}`,
		"xlsx.validate":       "{path: a.xlsx, required: [k]}",
		"xlsx.write":          `{path: a.xlsx, rows: "[]"}`,
		"xlsx.write_cells":    "{path: a.xlsx, cells: {A1: x}}",
	}
	// These actions load as another action's step, or as a run step, which
	// has no action name.
	loadsAs := map[string]string{
		"browser.extract":  "browser.run",
		"computer.extract": "computer.run",
		"exec":             "",
	}
	require.ElementsMatch(t, slices.Collect(maps.Keys(builtinActions)), slices.Collect(maps.Keys(withFields)))

	for action, with := range withFields {
		t.Run(action, func(t *testing.T) {
			t.Parallel()

			yaml := "steps:\n  - id: step\n    action: " + action + "\n"
			if with != "" {
				yaml += "    with: " + with + "\n"
			}
			want, ok := loadsAs[action]
			if !ok {
				want = action
			}
			assert.Equal(t, want, loadedActionName(t, yaml))
		})
	}
}

func TestActionNameOutsideBuiltinTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "Redis",
			yaml: "steps:\n  - id: step\n    action: redis.get\n    with: {key: k}\n",
			want: "redis.get",
		},
		{
			name: "RemoteAction",
			yaml: "steps:\n  - id: step\n    action: acme/dagu-actions-slack@v1\n",
			want: "acme/dagu-actions-slack@v1",
		},
		{
			name: "CustomAction",
			yaml: "actions:\n  greet:\n    input_schema: {type: object}\n    template: {action: log.write, with: {message: hi}}\n" +
				"steps:\n  - id: step\n    action: greet\n",
			want: "greet",
		},
		{
			name: "ParallelDAGRun",
			yaml: "steps:\n  - id: step\n    parallel: {items: [a]}\n    action: dag.run\n    with: {dag: child}\n",
			want: "dag.run",
		},
		{
			name: "Run",
			yaml: "steps:\n  - id: step\n    run: echo hi\n",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, loadedActionName(t, tt.yaml))
		})
	}
}

// loadedActionName loads a DAG of one step and returns the step's action name.
func loadedActionName(t *testing.T, yaml string) string {
	t.Helper()
	dag, err := LoadYAML(context.Background(), []byte(yaml))
	require.NoError(t, err)
	require.Len(t, dag.Steps, 1)
	return ActionName(&dag.Steps[0])
}
