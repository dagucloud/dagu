// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec096_skills_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// good is the SKILL.md of a valid skill of the folder name.
func good(name string) string {
	return "---\nname: " + name + "\ndescription: Explain the rules of " + name + ". Use when the user asks about " + name + ".\n---\n# " + name + "\n\nFollow the rules.\n"
}

// project is an isolated project with the fake claude CLI in bin/.
type project struct {
	t    *testing.T
	dagu *harness.Runner
}

func newProject(t *testing.T) *project {
	t.Helper()
	dagu := harness.NewRunner(t)
	require.NoError(t, os.Chmod(dagu.ProjectPath("bin/claude"), 0o755))
	return &project{t: t, dagu: dagu}
}

// skill writes the files of a skill folder. A path maps to its content.
func (p *project) skill(name string, files map[string]string) string {
	p.t.Helper()
	dir := p.dagu.ProjectPath(filepath.Join("skills", name))
	require.NoError(p.t, os.MkdirAll(dir, 0o755))
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(p.t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(p.t, os.WriteFile(full, []byte(content), 0o644))
	}
	return dir
}

// chatDAG writes a DAG with a chat.completion step that names the skills.
func (p *project) chatDAG(file string, skills ...string) string {
	p.t.Helper()
	text := "working_dir: .\nenv:\n  - LLM_BASE_URL: ${LLM_BASE_URL}\nsteps:\n  - id: ask\n    action: chat.completion\n    stdout: result.out\n    with:\n" +
		"      provider: local\n      model: test-model\n      base_url: $LLM_BASE_URL\n      stream: false\n      prompt: Help me.\n      skills: [" + strings.Join(skills, ", ") + "]\n"
	require.NoError(p.t, os.WriteFile(p.dagu.ProjectPath(file), []byte(text), 0o644))
	return file
}

// seedEndpoint adds the AI endpoint that the DAG of agentDAG names to the data
// directory home. An agent.run step of the provider claude MUST name an endpoint.
func (p *project) seedEndpoint(home string) { //nolint:unused // used by use_test.go, which has the build tag !windows
	p.t.Helper()
	harness.SeedTestEndpoint(p.t, p.dagu, home)
}

// agentDAG writes a DAG with an agent.run step of the provider claude.
func (p *project) agentDAG(file string, skills ...string) string { //nolint:unused // used by use_test.go, which has the build tag !windows
	p.t.Helper()
	text := "working_dir: .\nenv:\n  - FAKE_CLAUDE_DIR: ${FAKE_CLAUDE_DIR}\nsteps:\n  - id: code\n    action: agent.run\n    stdout: code.out\n    with:\n" +
		"      provider: claude\n      endpoint:\n        name: " + harness.EndpointName + "\n        model: claude-test\n      prompt: Help me.\n      skills: [" + strings.Join(skills, ", ") + "]\n"
	require.NoError(p.t, os.WriteFile(p.dagu.ProjectPath(file), []byte(text), 0o644))
	return file
}

// canonicalHash computes the hash of Spec 096, "The hash", from the files of a
// skill. It does not use the code of Dagu: it is the SHA-256 of the lines that
// sha256sum prints, sorted by path.
func canonicalHash(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var manifest strings.Builder
	for _, p := range paths {
		sum := sha256.Sum256(files[p])
		manifest.WriteString(hex.EncodeToString(sum[:]) + "  " + p + "\n")
	}
	sum := sha256.Sum256([]byte(manifest.String()))
	return hex.EncodeToString(sum[:])
}

// folderHash reads a skill folder from disk and computes its hash.
func folderHash(t *testing.T, dir string) string {
	t.Helper()
	files := map[string][]byte{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)] = data
		return nil
	}))
	return canonicalHash(files)
}

// ---- scripted OpenAI-compatible endpoint ----

type chatRequest struct {
	Messages []struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name       string `json:"name"`
			Parameters struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
}

func (r chatRequest) toolNames() []string { //nolint:unused // used by use_test.go, which has the build tag !windows
	var names []string
	for _, tool := range r.Tools {
		names = append(names, tool.Function.Name)
	}
	return names
}

// lastToolResult returns the content of the last tool message.
func (r chatRequest) lastToolResult() string { //nolint:unused // used by use_test.go, which has the build tag !windows
	for _, v := range slices.Backward(r.Messages) {
		if v.Role == "tool" {
			return v.Content
		}
	}
	return ""
}

func (r chatRequest) system() string { //nolint:unused // used by use_test.go, which has the build tag !windows
	for _, m := range r.Messages {
		if m.Role == "system" {
			return m.Content
		}
	}
	return ""
}

// llm answers each request with the next scripted tool call, then with a final
// answer. It records every request.
type llm struct {
	srv      *httptest.Server
	mu       sync.Mutex
	script   []string
	requests []chatRequest
}

func newLLM(t *testing.T, calls ...[2]string) *llm {
	t.Helper()
	l := &llm{}
	for i, c := range calls {
		quoted, _ := json.Marshal(c[1])
		l.script = append(l.script, `{"id":"call_`+string(rune('a'+i))+`","type":"function","function":{"name":"`+c[0]+`","arguments":`+string(quoted)+`}}`)
	}
	l.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		l.mu.Lock()
		index := len(l.requests)
		l.requests = append(l.requests, req)
		l.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if index < len(l.script) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[` + l.script[index] + `]},"finish_reason":"tool_calls"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`))
	}))
	t.Cleanup(l.srv.Close)
	return l
}

func (l *llm) captured() []chatRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.requests)
}

// ---- persisted state ----

// savedSkillHashes reads the skill definitions of the node of a step from the
// last status of the only run under home.
func savedSkillHashes(t *testing.T, home, step string) map[string]string { //nolint:unused // used by use_test.go, which has the build tag !windows
	t.Helper()
	var files []string
	require.NoError(t, filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "status.jsonl" && !strings.Contains(filepath.ToSlash(path), "/sub/") {
			files = append(files, path)
		}
		return nil
	}))
	require.Len(t, files, 1)
	file, err := os.Open(files[0]) // #nosec G304 -- the path is under the temp home of the test.
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	var last []byte
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for scanner.Scan() {
		if line := bytes.TrimSpace(scanner.Bytes()); len(line) > 0 {
			last = append([]byte(nil), line...)
		}
	}
	var status struct {
		Nodes []struct {
			Step struct {
				ID string `json:"id"`
			} `json:"step"`
			SkillDefinitions []struct {
				Name string `json:"name"`
				Hash string `json:"hash"`
			} `json:"skillDefinitions"`
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(last, &status))
	out := map[string]string{}
	for _, node := range status.Nodes {
		if node.Step.ID != step {
			continue
		}
		for _, d := range node.SkillDefinitions {
			out[d.Name] = d.Hash
		}
	}
	return out
}

func sha256Hex(s string) string { //nolint:unused // used by use_test.go, which has the build tag !windows
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
