// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package harness

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/runenv"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeArgsFollowTheOrderOfTheSpec(t *testing.T) {
	t.Parallel()

	turns := 7
	invocation := claudeInvocation{
		prompt:         "Review it.",
		permissionMode: "dontAsk",
		model:          "sonnet",
		appendSystem:   "Be brief.",
		tools:          []string{"Read", "Grep", "Glob", "Bash"},
		allow:          []string{"Read", "Grep", "Glob", "Bash(git diff *)", "mcp__docs__search"},
		deny:           []string{"Bash(git push *)"},
		mcpConfig:      "/tmp/mcp.json",
		maxTurns:       &turns,
		jsonSchema:     `{"type":"object"}`,
	}
	assert.Equal(t, []string{
		"-p", "Review it.",
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", "dontAsk",
		"--model", "sonnet",
		"--append-system-prompt", "Be brief.",
		"--tools", "Read,Grep,Glob,Bash",
		"--allowedTools", "Read", "Grep", "Glob", "Bash(git diff *)", "mcp__docs__search",
		"--disallowedTools", "Bash(git push *)",
		"--strict-mcp-config", "--mcp-config", "/tmp/mcp.json",
		"--max-turns", "7",
		"--json-schema", `{"type":"object"}`,
	}, invocation.args())
}

// The flag --effort comes right after --model.
func TestClaudeArgsPutEffortAfterModel(t *testing.T) {
	t.Parallel()

	args := claudeInvocation{prompt: "hi", permissionMode: "dontAsk", model: "sonnet", effort: "xhigh", appendSystem: "Be brief.", mcpConfig: "/tmp/m.json"}.args()
	i := slicesIndex(args, "--model")
	assert.Equal(t, []string{"--model", "sonnet", "--effort", "xhigh", "--append-system-prompt"}, args[i:i+5])

	args = claudeInvocation{prompt: "hi", permissionMode: "dontAsk", effort: "low", mcpConfig: "/tmp/m.json"}.args()
	assert.Equal(t, "low", args[slicesIndex(args, "--effort")+1], "an effort with no model still passes the flag")
	assert.NotContains(t, args, "--model")
}

// A flag is absent when its key is absent. The flags --tools, --strict-mcp-config
// and --mcp-config are always there, and --bare never is.
func TestClaudeArgsMinimal(t *testing.T) {
	t.Parallel()

	args := claudeInvocation{prompt: "hi", permissionMode: "plan", mcpConfig: "/tmp/m.json"}.args()
	assert.Equal(t, []string{
		"-p", "hi",
		"--output-format", "stream-json", "--verbose",
		"--no-session-persistence",
		"--permission-mode", "plan",
		"--tools", "",
		"--strict-mcp-config", "--mcp-config", "/tmp/m.json",
	}, args)
	assert.NotContains(t, args, "--bare")
}

func TestDeriveClaudeTools(t *testing.T) {
	t.Parallel()

	entries := []ir.ToolEntry{
		{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{
			Read: true, Search: true,
			Shell: &ir.BuiltinShell{Allow: []string{"git diff *", "go test"}, Deny: []string{"git push *"}},
		}},
		{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Write: true, Edit: true}},
	}
	servers := []claudeMCPServer{{name: "docs", allow: []string{"search", "fetch"}}}

	tools := deriveClaudeTools(entries, servers, false)
	assert.Equal(t, []string{"Read", "Grep", "Glob", "Bash", "Write", "Edit"}, tools.names)
	assert.Equal(t, []string{
		"Read", "Grep", "Glob", "Bash(git diff *)", "Bash(go test)", "Write", "Edit",
		"mcp__docs__search", "mcp__docs__fetch",
	}, tools.allow)
	assert.Equal(t, []string{"Bash(git push *)"}, tools.deny)

	none := deriveClaudeTools(nil, nil, false)
	assert.Empty(t, none.names)
	assert.Empty(t, none.allow)
	assert.Empty(t, none.deny)
}

// The tools of the bridge are mcp__dagu__<name>, and they come before the tools
// of an mcp entry.
func TestDeriveClaudeToolsWithBridge(t *testing.T) {
	t.Parallel()

	entries := []ir.ToolEntry{{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}}}
	servers := []claudeMCPServer{
		{name: "dagu", allow: []string{"refund", "lookup"}},
		{name: "docs", allow: []string{"search"}},
	}

	tools := deriveClaudeTools(entries, servers, false)
	assert.Equal(t, []string{"Read"}, tools.names, "the bridge tools are not built-in tools")
	assert.Equal(t, []string{
		"Read", "mcp__dagu__refund", "mcp__dagu__lookup", "mcp__docs__search",
	}, tools.allow)
}

func TestClaudeMCPConfig(t *testing.T) {
	t.Parallel()

	data, err := claudeMCPConfig(nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"mcpServers":{}}`, string(data))

	data, err = claudeMCPConfig([]claudeMCPServer{{name: "docs", url: "https://x/mcp", headers: map[string]string{"Authorization": "Bearer t"}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"mcpServers":{"docs":{"type":"http","url":"https://x/mcp","headers":{"Authorization":"Bearer t"}}}}`, string(data))
}

// A mounted skill adds the tool Skill to --tools and to the allow rules.
func TestDeriveClaudeToolsWithSkill(t *testing.T) {
	t.Parallel()

	tools := deriveClaudeTools([]ir.ToolEntry{{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}}}, nil, true)
	assert.Equal(t, []string{"Read", "Skill"}, tools.names)
	assert.Equal(t, []string{"Read", "Skill"}, tools.allow)

	skillOnly := deriveClaudeTools(nil, nil, true)
	assert.Equal(t, []string{"Skill"}, skillOnly.names)
	assert.Equal(t, []string{"Skill"}, skillOnly.allow)
}

func TestAppendSystemText(t *testing.T) {
	t.Parallel()

	assert.Empty(t, appendSystemText(""))
	assert.Empty(t, appendSystemText(" \n"))
	assert.Equal(t, "Be brief.", appendSystemText("Be brief.\n"))
	assert.Equal(t, "Be brief.\n\nBe kind.", appendSystemText("Be brief.\n\nBe kind."))
}

const streamSuccess = `{"type":"system","subtype":"init","model":"sonnet","mcp_servers":[]}
not json at all
{"type":"assistant","message":{"content":[{"type":"text","text":"x"},{"type":"tool_use","name":"Grep","id":"t1","input":{}},{"type":"tool_use","name":"mcp__docs__search","id":"t2","input":{}},{"type":"tool_use","name":"StructuredOutput","id":"t3","input":{}}]}}
{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"done","structured_output":{"verdict":"accept"},"total_cost_usd":1.5,"modelUsage":{"sonnet":{"inputTokens":120,"outputTokens":30,"cacheReadInputTokens":5,"cacheCreationInputTokens":2,"costUSD":1.5}}}
`

func readStream(text string) *claudeStream {
	stream := &claudeStream{}
	_, _ = stream.Write([]byte(text))
	stream.Flush()
	return stream
}

func TestClaudeStreamReadsTheExampleOfTheSpec(t *testing.T) {
	t.Parallel()

	stream := readStream(streamSuccess)
	assert.Equal(t, []string{"search", "docs_search"}, stream.toolCalls, "StructuredOutput is not a call")
	require.NotNil(t, stream.result)
	assert.JSONEq(t, `{"verdict":"accept"}`, string(stream.result.StructuredOutput))

	outcome := classifyClaude(0, stream)
	assert.Equal(t, ir.AgentKindOK, outcome.kind)
}

func TestClaudeStreamReadsLinesAcrossWrites(t *testing.T) {
	t.Parallel()

	stream := &claudeStream{}
	for _, chunk := range []string{`{"type":"result","sub`, `type":"success","is_error":false}`, "\n"} {
		_, _ = stream.Write([]byte(chunk))
	}
	require.NotNil(t, stream.result)
	assert.Equal(t, "success", stream.result.Subtype)

	// A last line with no line end is read when the stream ends.
	stream = readStream(`{"type":"result","subtype":"success","is_error":false}`)
	require.NotNil(t, stream.result)
}

// A CLI that writes no line end cannot fill the memory of the worker: a line
// over the limit is dropped, and the next line is read.
func TestClaudeStreamDropsALineOverTheLimit(t *testing.T) {
	t.Parallel()

	stream := &claudeStream{}
	_, _ = stream.Write(bytes.Repeat([]byte("a"), maxStreamLine/2))
	_, _ = stream.Write(bytes.Repeat([]byte("a"), maxStreamLine/2+1))
	assert.Empty(t, stream.partial, "the stream keeps no byte of the long line")
	_, _ = stream.Write([]byte("end of the long line\n" + `{"type":"result","subtype":"success","is_error":false}` + "\n"))
	require.NotNil(t, stream.result)
	assert.Equal(t, "success", stream.result.Subtype)
}

func TestClaudeFailureKinds(t *testing.T) {
	t.Parallel()

	result := func(fields string) string {
		return `{"type":"result","is_error":true,` + fields + `}` + "\n"
	}
	assistant := func(errorName string) string {
		return `{"type":"assistant","error":"` + errorName + `","message":{"content":[]}}` + "\n"
	}
	for name, tc := range map[string]struct {
		stream string
		exit   int
		kind   ir.AgentFailureKind
		reason string
	}{
		"ok":                      {`{"type":"result","subtype":"success","is_error":false,"result":"x"}`, 0, ir.AgentKindOK, ""},
		"auth by assistant error": {assistant("authentication_failed") + result(`"subtype":"success","result":"x"`), 1, ir.AgentKindAuthRequired, ""},
		"auth by status":          {result(`"subtype":"error_during_execution","api_error_status":401`), 1, ir.AgentKindAuthRequired, ""},
		"rate limit by assistant": {assistant("rate_limit") + result(`"subtype":"success","result":"x"`), 1, ir.AgentKindRateLimited, ""},
		"rate limit by status":    {result(`"subtype":"error_during_execution","api_error_status":429`), 1, ir.AgentKindRateLimited, ""},
		"auth by text":            {result(`"subtype":"success","result":"Please run /login"`), 1, ir.AgentKindAuthRequired, ""},
		"rate limit by text":      {result(`"subtype":"success","result":"You've hit your session limit"`), 1, ir.AgentKindRateLimited, ""},
		"text with a structured field": {
			// The text test is a fallback: a structured error that is not auth or
			// rate limit wins over the words of the result.
			assistant("server_error") + result(`"subtype":"error_during_execution","result":"Please run /login"`), 1, ir.AgentKindError, "error_during_execution",
		},
		"max turns":         {result(`"subtype":"error_max_turns"`), 1, ir.AgentKindError, "max_tool_iterations"},
		"max budget":        {result(`"subtype":"error_max_budget_usd"`), 1, ir.AgentKindError, "error_max_budget_usd"},
		"structured retry":  {result(`"subtype":"error_max_structured_output_retries"`), 1, ir.AgentKindError, "schema_mismatch"},
		"during execution":  {result(`"subtype":"error_during_execution"`), 1, ir.AgentKindError, "error_during_execution"},
		"no result":         {`{"type":"system","subtype":"init"}`, 0, ir.AgentKindError, "no_result"},
		"no result, exit":   {"", 3, ir.AgentKindError, "exit"},
		"success but exit":  {`{"type":"result","subtype":"success","is_error":false}`, 2, ir.AgentKindError, "exit"},
		"retry events only": {`{"type":"system","subtype":"api_retry","error":"rate_limit"}` + "\n" + `{"type":"result","subtype":"success","is_error":false}`, 0, ir.AgentKindOK, ""},
	} {
		outcome := classifyClaude(tc.exit, readStream(tc.stream))
		assert.Equal(t, tc.kind, outcome.kind, name)
		if tc.reason != "" {
			assert.Equal(t, tc.reason, outcome.reason, name)
		}
	}
}

// fakeClaude is a script that stands for the claude CLI. It records its argv,
// and prints the file stream.<n> of its directory, where n is the number of the
// call, or the last file when there are fewer. It exits with the code in the
// file exit.<n>, or 0. It keeps a copy of the MCP config (mcp.<n>.json, with its
// mode in mcp.<n>.mode) and a copy of the directory of --add-dir (adddir.<n>),
// because Dagu deletes them at the end of the attempt. It waits for the file
// go.<n> when the file wait.<n> exists, and it writes the file stderr.<n> to its
// stderr.
const fakeClaude = `#!/bin/sh
dir="$FAKE_CLAUDE_DIR"
n=$(($(cat "$dir/count" 2>/dev/null || echo 0) + 1))
echo "$n" > "$dir/count"
: > "$dir/argv.$n"
env > "$dir/env.$n"
for a in "$@"; do printf '%s\n' "$a" >> "$dir/argv.$n"; done
previous=""
for a in "$@"; do
  if [ "$previous" = "--mcp-config" ] && [ -f "$a" ]; then
    cp "$a" "$dir/mcp.$n.json"
    ls -ln "$a" | cut -c1-10 > "$dir/mcp.$n.mode"
  fi
  if [ "$previous" = "--add-dir" ]; then
    cp -R "$a" "$dir/adddir.$n"
  fi
  previous="$a"
done
if [ -f "$dir/wait.$n" ]; then
  i=0
  while [ ! -f "$dir/go.$n" ] && [ "$i" -lt 1200 ]; do sleep 0.05; i=$((i+1)); done
fi
[ -f "$dir/stderr.$n" ] && cat "$dir/stderr.$n" >&2
f="$dir/stream.$n"
[ -f "$f" ] || f=$(ls "$dir"/stream.* | sort | tail -n 1)
cat "$f"
code=0
[ -f "$dir/exit.$n" ] && code=$(cat "$dir/exit.$n")
exit "$code"
`

type fakeClaudeRun struct {
	dir    string
	stdout bytes.Buffer
	stderr bytes.Buffer
	exec   executor.Executor
	agent  *agentHarness
	run    func() error
	// wrap changes the context of the run. A test of the gateway uses it.
	wrap func(context.Context) context.Context
}

// sharedFakeClaude is the one copy of the script of the fake claude that the
// parallel tests of the package link to. A test that wrote the script itself
// could start it while the file was still open for writing in a process that a
// parallel test had forked, and the start failed with "text file busy".
var sharedFakeClaude struct {
	sync.Mutex
	users int
	dir   string
}

// acquireFakeClaude returns the path of the shared script. The last test that
// uses it deletes it.
func acquireFakeClaude(t *testing.T) string {
	t.Helper()
	shared := &sharedFakeClaude
	shared.Lock()
	defer shared.Unlock()
	if shared.users == 0 {
		dir, err := os.MkdirTemp("", "dagu-fake-claude-*")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "claude"), []byte(fakeClaude), 0o755))
		shared.dir = dir
	}
	shared.users++
	t.Cleanup(func() {
		shared.Lock()
		defer shared.Unlock()
		shared.users--
		if shared.users == 0 {
			_ = os.RemoveAll(shared.dir)
		}
	})
	return filepath.Join(shared.dir, "claude")
}

func newFakeClaudeRun(t *testing.T, run ir.AgentRun, schema map[string]any, streams ...string) *fakeClaudeRun {
	t.Helper()
	return newFakeClaudeRunWith(t, nil, run, schema, streams...)
}

// newFakeClaudeRunWith adds context options to the context of the run.
func newFakeClaudeRunWith(t *testing.T, opts []runtime.ContextOption, run ir.AgentRun, schema map[string]any, streams ...string) *fakeClaudeRun {
	t.Helper()
	return newFakeClaudeRunConfig(t, opts, nil, run, schema, streams...)
}

// newFakeClaudeRunConfig adds keys to the harness config of the step.
func newFakeClaudeRunConfig(t *testing.T, opts []runtime.ContextOption, extra map[string]any, run ir.AgentRun, schema map[string]any, streams ...string) *fakeClaudeRun {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	require.NoError(t, os.Symlink(acquireFakeClaude(t), filepath.Join(bin, "claude")))
	for i, stream := range streams {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("stream.%d", i+1)), []byte(stream), 0o600))
	}

	if run.CLI == nil {
		run.CLI = &ir.AgentCLIConfig{}
	}
	run.Provider = "claude"
	logPath := filepath.Join(dir, "step.out")
	config := map[string]any{"provider": "claude", "model": "sonnet"}
	maps.Copy(config, extra)
	step := ir.Step{
		Name:         "review",
		Agent:        &run,
		OutputSchema: schema,
		ExecutorConfig: ir.ExecutorConfig{
			Type:   "harness",
			Config: config,
		},
	}
	dag := &ir.DAG{Name: "agent-run", WorkingDir: dir, LocalDAGs: bridgeLocalDAGs()}
	ctx := newHarnessTestContextWith(t, dag, step, opts,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_CLAUDE_DIR="+dir,
		runenv.EnvKeyDAGRunStepStdoutFile+"="+logPath,
		"PATCH=p-1",
	)
	exec, err := newHarness(ctx, step)
	require.NoError(t, err)
	agent, ok := exec.(*agentHarness)
	require.True(t, ok)
	result := &fakeClaudeRun{dir: dir, exec: exec, agent: agent}
	exec.SetStdout(&result.stdout)
	exec.SetStderr(&result.stderr)
	t.Cleanup(func() { _ = os.Remove(logPath) })
	result.run = func() error {
		runCtx := ctx
		if result.wrap != nil {
			runCtx = result.wrap(ctx)
		}
		return exec.Run(runCtx)
	}
	return result
}

func (f *fakeClaudeRun) argv(t *testing.T, n int) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, fmt.Sprintf("argv.%d", n)))
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func closedSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"verdict": map[string]any{"type": "string"}},
		"required":             []any{"verdict"},
	}
}

// The step passes --effort after --model. A value that a reference gave and that
// is not one of the five levels fails the attempt before the CLI starts.
func TestAgentRunCLIEffort(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRunConfig(t, nil, map[string]any{"effort": "xhigh"}, ir.AgentRun{Prompt: "x"}, closedSchema(), streamSuccess)
	require.NoError(t, run.run())
	args := run.argv(t, 1)
	i := slicesIndex(args, "--model")
	assert.Equal(t, []string{"--model", "sonnet", "--effort", "xhigh"}, args[i:i+4])

	for _, value := range []string{"extreme", "", "High"} {
		bad := newFakeClaudeRunConfig(t, nil, map[string]any{"effort": value}, ir.AgentRun{Prompt: "x"}, closedSchema(), streamSuccess)
		err := bad.run()
		agentErr, ok := executor.AsAgentError(err)
		require.True(t, ok, "%q: %v", value, err)
		assert.Equal(t, ir.AgentKindError, agentErr.Kind)
		assert.Equal(t, "effort_invalid", agentErr.Reason)
		assert.Contains(t, err.Error(), "low, medium, high, xhigh, max")
		_, statErr := os.Stat(filepath.Join(bad.dir, "argv.1"))
		assert.True(t, os.IsNotExist(statErr), "the CLI does not start")
	}
}

func TestAgentRunCLISuccess(t *testing.T) {
	t.Parallel()

	turns := 5
	run := newFakeClaudeRun(t, ir.AgentRun{
		Prompt:            "Review ${PATCH}.",
		System:            "Be brief.",
		MaxToolIterations: &turns,
		CLI: &ir.AgentCLIConfig{Tools: []ir.ToolEntry{
			{Kind: ir.ToolEntryBuiltin, Builtin: &ir.BuiltinTools{Read: true}},
		}},
	}, closedSchema(), streamSuccess)

	require.NoError(t, run.run())
	assert.Equal(t, `{"verdict":"accept"}`+"\n", run.stdout.String())
	assert.Equal(t, map[string]any{"tool_calls": []string{"search", "docs_search"}}, run.agent.GetOutputs())
	assert.Equal(t, 0, run.agent.ExitCode())

	args := run.argv(t, 1)
	assert.Equal(t, "-p", args[0])
	assert.Equal(t, "Review p-1.", args[1], "the prompt resolves value references")
	assert.Contains(t, args, "--permission-mode")
	assert.Equal(t, "dontAsk", args[slicesIndex(args, "--permission-mode")+1], "the permission mode is fixed")
	assert.Equal(t, "sonnet", args[slicesIndex(args, "--model")+1])
	assert.Equal(t, "Be brief.", args[slicesIndex(args, "--append-system-prompt")+1])
	assert.Equal(t, "Read", args[slicesIndex(args, "--tools")+1])
	assert.Equal(t, "5", args[slicesIndex(args, "--max-turns")+1])
	assert.JSONEq(t, `{"additionalProperties":false,"properties":{"verdict":{"type":"string"}},"required":["verdict"],"type":"object"}`,
		args[slicesIndex(args, "--json-schema")+1])
	assert.Contains(t, args, "--strict-mcp-config")
	assert.NotContains(t, args, "--bare")

	// The MCP config file does not outlive the attempt.
	config := args[slicesIndex(args, "--mcp-config")+1]
	_, err := os.Stat(config)
	assert.True(t, os.IsNotExist(err), "the MCP config is deleted at the end of the attempt")
	assert.JSONEq(t, `{"mcpServers":{}}`, run.file(t, "mcp.1.json"), "a step with no MCP server gives the CLI no server")

	// The raw stream is next to the stdout log.
	data, err := os.ReadFile(filepath.Join(run.dir, "step.out"+ir.AgentStreamSuffix))
	require.NoError(t, err)
	assert.Equal(t, streamSuccess, string(data))
}

func slicesIndex(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}

// A text step writes the field result to stdout and reads no JSON from it.
func TestAgentRunCLITextResult(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{Prompt: "Say hi."}, nil,
		`{"type":"result","subtype":"success","is_error":false,"num_turns":1,"result":"{\"verdict\":\"accept\"}"}`+"\n")
	require.NoError(t, run.run())
	assert.Equal(t, `{"verdict":"accept"}`+"\n", run.stdout.String())
	assert.Equal(t, map[string]any{"tool_calls": []string{}}, run.agent.GetOutputs())
}

// The typed result comes from structured_output only. A missing value, a
// duplicate field and an unknown field each fail with their reason.
func TestAgentRunCLITypedResultFailures(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		result string
		reason string
	}{
		"missing":   {`{"type":"result","subtype":"success","is_error":false,"result":"{\"verdict\":\"accept\"}"}`, "no_result"},
		"duplicate": {`{"type":"result","subtype":"success","is_error":false,"structured_output":{"verdict":"a","verdict":"b"}}`, "invalid_json"},
		"unknown":   {`{"type":"result","subtype":"success","is_error":false,"structured_output":{"verdict":"a","extra":1}}`, "schema_mismatch"},
		"retries":   {`{"type":"result","subtype":"error_max_structured_output_retries","is_error":true}`, "schema_mismatch"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exit := 0
			if name == "retries" {
				exit = 1
			}
			run := newFakeClaudeRun(t, ir.AgentRun{Prompt: "x"}, closedSchema(), tc.result+"\n")
			require.NoError(t, os.WriteFile(filepath.Join(run.dir, "exit.1"), []byte(fmt.Sprint(exit)), 0o600))

			err := run.run()
			agentErr, ok := executor.AsAgentError(err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, ir.AgentKindError, agentErr.Kind)
			assert.Equal(t, tc.reason, agentErr.Reason)
			assert.Empty(t, run.stdout.String())
			assert.Nil(t, run.agent.GetOutputs())
		})
	}
}

func TestAgentRunCLIFailureKinds(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		stream string
		exit   int
		kind   ir.AgentFailureKind
		code   int
	}{
		"auth":  {`{"type":"assistant","error":"authentication_failed","message":{"content":[]}}` + "\n" + `{"type":"result","subtype":"success","is_error":true,"result":"Please run /login"}` + "\n", 1, ir.AgentKindAuthRequired, 77},
		"rate":  {`{"type":"assistant","error":"rate_limit","message":{"content":[]}}` + "\n" + `{"type":"result","subtype":"success","is_error":true,"result":"limit"}` + "\n", 1, ir.AgentKindRateLimited, 75},
		"error": {`{"type":"result","subtype":"error_during_execution","is_error":true}` + "\n", 4, ir.AgentKindError, 4},
		"crash": {"", 9, ir.AgentKindError, 9},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			run := newFakeClaudeRun(t, ir.AgentRun{Prompt: "x"}, nil, tc.stream)
			require.NoError(t, os.WriteFile(filepath.Join(run.dir, "exit.1"), []byte(fmt.Sprint(tc.exit)), 0o600))

			err := run.run()
			agentErr, ok := executor.AsAgentError(err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, tc.kind, agentErr.Kind)
			assert.Equal(t, tc.code, agentErr.ExitCode())
			assert.Equal(t, tc.code, run.agent.ExitCode())
			assert.Equal(t, "agent: attempt ended "+string(tc.kind)+"\n", run.stderr.String())
			assert.Nil(t, run.agent.GetOutputs())
		})
	}
}

// A rate limit fails the step at once with the code 75. A retry_policy retries it.
func TestAgentRunCLIRateLimitFailsWithoutWait(t *testing.T) {
	t.Parallel()

	run := newFakeClaudeRun(t, ir.AgentRun{Prompt: "x"}, nil,
		`{"type":"assistant","error":"rate_limit","message":{"content":[]}}`+"\n"+`{"type":"result","subtype":"success","is_error":true,"result":"limit"}`+"\n")
	require.NoError(t, os.WriteFile(filepath.Join(run.dir, "exit.1"), []byte("1"), 0o600))
	require.Error(t, run.run())
	_, err := os.Stat(filepath.Join(run.dir, "argv.2"))
	assert.True(t, os.IsNotExist(err), "claude ran once")
}
