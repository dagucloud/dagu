// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec091_agent_node holds black-box conformance tests for the action
// agent.run. A CLI provider runs a fake claude executable that prints canned
// stream-json lines and records its argv. The tests of chat.completion run
// against a local OpenAI-compatible endpoint. No test needs an external service.
package spec091_agent_node_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// agentProject is an isolated project with the fake CLIs in bin/ and the files
// that the fake CLIs write.
type agentProject struct {
	t     *testing.T
	dagu  *harness.Runner
	home  string
	fake  string
	llm   *llm
	extra []string
}

// newProject copies the fixtures, makes the fake CLIs executable and adds the AI
// endpoint of the fixtures to the data directory. The mode of
// a file that git checks out depends on the platform, so the test sets it.
func newProject(t *testing.T, llm *llm) *agentProject {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLIs of the fixtures are POSIX scripts")
	}
	dagu := harness.NewRunner(t)
	for _, name := range []string{"claude", "codex"} {
		require.NoError(t, os.Chmod(dagu.ProjectPath(filepath.Join("bin", name)), 0o755))
	}
	// An agent.run step of the provider claude MUST name an endpoint (Spec 091,
	// "Endpoint"), so the data directory holds the endpoint that the fixtures name.
	home := filepath.Join(t.TempDir(), "dagu")
	harness.SeedTestEndpoint(t, dagu, home)
	// The process has no restricted user, so its untrusted steps need isolation: off.
	harness.WriteIsolationOff(t, home)
	return &agentProject{
		t:    t,
		dagu: dagu,
		home: home,
		fake: t.TempDir(),
		llm:  llm,
	}
}

// env returns the environment of a run: the fake CLIs first in PATH, the state
// directory of the fake CLIs, the scenario, and the address of the model. The
// process has no coordinator, so a CLI agent runs on the host (Spec 092,
// "Dispatch", development mode).
func (p *agentProject) env(scenario string, extra ...string) []string {
	env := []string{
		"PATH=" + p.dagu.ProjectPath("bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"DAGU_HOME=" + p.home,
		"DAGU_COORDINATOR_ENABLED=false",
		"FAKE_CLAUDE_DIR=" + p.fake,
		"FAKE_CLAUDE_SCENARIO=" + scenario,
		"LLM_BASE_URL=http://127.0.0.1:1",
	}
	if p.llm != nil {
		env[len(env)-1] = "LLM_BASE_URL=" + p.llm.srv.URL
	}
	return append(env, extra...)
}

// start runs `dagu start` on a fixture.
func (p *agentProject) start(scenario, fixture string, extra ...string) *harness.Result {
	p.t.Helper()
	return p.dagu.RunWithEnv(p.env(scenario, extra...), "start", fixture)
}

// calls returns how many times the fake claude ran.
func (p *agentProject) calls() int {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.fake, "count"))
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(p.t, err)
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(p.t, err)
	return n
}

// argv returns the arguments of the nth call of the fake claude.
func (p *agentProject) argv(n int) []string {
	p.t.Helper()
	return p.readArgv("argv." + strconv.Itoa(n))
}

func (p *agentProject) readArgv(name string) []string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.fake, name))
	require.NoError(p.t, err)
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

// fakeFile returns the content of a file that a fake CLI wrote.
func (p *agentProject) fakeFile(name string) string {
	p.t.Helper()
	data, err := os.ReadFile(filepath.Join(p.fake, name))
	require.NoError(p.t, err)
	return string(data)
}

// requireNoStdout checks that a failed attempt wrote nothing to the stdout file
// of the step. The file can exist, because the redirect creates it.
func (p *agentProject) requireNoStdout(name string) {
	p.t.Helper()
	data, err := os.ReadFile(p.dagu.ProjectPath(name))
	if os.IsNotExist(err) {
		return
	}
	require.NoError(p.t, err)
	require.Empty(p.t, string(data), "a failed attempt publishes no output")
}

// flagValue returns the argument after a flag.
func flagValue(t *testing.T, argv []string, flag string) string {
	t.Helper()
	i := slices.Index(argv, flag)
	require.GreaterOrEqual(t, i, 0, "flag %s in %q", flag, argv)
	require.Less(t, i+1, len(argv))
	return argv[i+1]
}

// summary flattens the run summary that dagu start prints: the tree wraps long
// lines inside box characters.
func summary(result *harness.Result) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(result.Stdout(), "│", " ")), " ")
}

// requirePartialSuccess checks a run in which the step failed, and continue_on let
// the next step run: the run ends partially succeeded and the command fails.
func requirePartialSuccess(t *testing.T, result *harness.Result) {
	t.Helper()
	result.ExpectNonZeroExitCode()
	require.Contains(t, result.Stdout(), "Partially Succeeded")
}

// ---- persisted state ----

type savedNode struct {
	Step struct {
		ID             string `json:"id"`
		ExecutorConfig struct {
			Type string `json:"type"`
		} `json:"executorConfig"`
		AgentRun *struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Prompt   string `json:"prompt"`
		} `json:"agentRun"`
	} `json:"step"`
	Stdout           string            `json:"stdout"`
	Status           int               `json:"status"`
	ToolDefinitions  []toolDefinition  `json:"toolDefinitions"`
	SkillDefinitions []skillDefinition `json:"skillDefinitions"`
	Children         []struct {
		DAGRunID string `json:"dagRunId"`
		DAGName  string `json:"dagName"`
	} `json:"children"`
}

// toolDefinition is the record of one tool of the effective catalog.
type toolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
	Source      string         `json:"source"`
}

// skillDefinition is the record of one skill of the step.
type skillDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Omitted     string `json:"omitted"`
}

// savedNodes reads the nodes of the last status of the only run under the
// Dagu home of the project. The status file is the persisted state of the run.
// The runs of child DAGs, such as the DAG of a tool, are in sub folders and do
// not count.
func (p *agentProject) savedNodes() []savedNode {
	p.t.Helper()

	var files []string
	err := filepath.WalkDir(p.home, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && d.Name() == "status.jsonl" && !strings.Contains(filepath.ToSlash(path), "/sub/") {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(p.t, err)
	require.Len(p.t, files, 1, "status files under %s", p.home)

	file, err := os.Open(files[0]) // #nosec G304 -- the path is under the temp home of the test.
	require.NoError(p.t, err)
	defer func() { _ = file.Close() }()
	var last []byte
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
			last = append([]byte(nil), line...)
		}
	}
	require.NoError(p.t, scanner.Err())

	var status struct {
		Nodes []savedNode `json:"nodes"`
	}
	require.NoError(p.t, json.Unmarshal(last, &status))
	return status.Nodes
}

// savedNode returns the node of a step.
func (p *agentProject) savedNode(id string) savedNode {
	p.t.Helper()
	for _, node := range p.savedNodes() {
		if node.Step.ID == id {
			return node
		}
	}
	p.t.Fatalf("no node %q in the saved status", id)
	return savedNode{}
}

// ---- local OpenAI-compatible endpoint ----

type chatRequest struct {
	Model      string `json:"model"`
	Stream     bool   `json:"stream"`
	ToolChoice string `json:"tool_choice"`
	Messages   []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

// messages returns the content of each message of a role, in order.
func (r chatRequest) messages(role string) []string {
	var contents []string
	for _, m := range r.Messages {
		if m.Role == role {
			contents = append(contents, m.Content)
		}
	}
	return contents
}

func (r chatRequest) message(role string) string {
	for _, m := range r.Messages {
		if m.Role == role {
			return m.Content
		}
	}
	return ""
}

// reply is one answer of the endpoint. times says how often it repeats; the
// last reply repeats without end.
type reply struct {
	status int
	body   string
	times  int
}

// llm is a local OpenAI-compatible endpoint. It records each request and
// answers with the replies in order.
type llm struct {
	srv      *httptest.Server
	mu       sync.Mutex
	replies  []reply
	requests []chatRequest
}

func newLLM(t *testing.T, replies ...reply) *llm {
	t.Helper()
	l := &llm{replies: replies}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		l.mu.Lock()
		l.requests = append(l.requests, req)
		var next reply
		switch {
		case len(l.replies) == 0:
			next = reply{status: http.StatusInternalServerError, body: "no scripted reply left"}
		default:
			next = l.replies[0]
			if len(l.replies) > 1 {
				if next.times > 1 {
					l.replies[0].times--
				} else {
					l.replies = l.replies[1:]
				}
			}
		}
		l.mu.Unlock()

		if next.status != 0 && next.status != http.StatusOK {
			http.Error(w, next.body, next.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(next.body))
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *llm) captured() []chatRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.requests)
}

// usageBlock is the usage that an answer reports.
func usageBlock(prompt, completion int) string {
	return `"usage":{"prompt_tokens":` + strconv.Itoa(prompt) +
		`,"completion_tokens":` + strconv.Itoa(completion) +
		`,"total_tokens":` + strconv.Itoa(prompt+completion) + `}`
}

// textReply is a final answer.
func textReply(text string, prompt, completion int) reply {
	body, _ := json.Marshal(map[string]any{"role": "assistant", "content": text})
	return reply{body: `{"choices":[{"message":` + string(body) + `,"finish_reason":"stop"}],` + usageBlock(prompt, completion) + `}`}
}

// toolCallReply is a turn that calls one tool.
func toolCallReply(name, arguments string, prompt, completion int) reply {
	message, _ := json.Marshal(map[string]any{
		"role":    "assistant",
		"content": "",
		"tool_calls": []any{map[string]any{
			"id":       "call_" + name,
			"type":     "function",
			"function": map[string]any{"name": name, "arguments": arguments},
		}},
	})
	return reply{body: `{"choices":[{"message":` + string(message) + `,"finish_reason":"tool_calls"}],` + usageBlock(prompt, completion) + `}`}
}

// respondReply is a turn that answers through the respond tool.
func respondReply(arguments string, prompt, completion int) reply {
	return toolCallReply("respond", arguments, prompt, completion)
}
