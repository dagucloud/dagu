// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSystemPrompt(t *testing.T) {
	t.Parallel()

	t.Run("includes environment info", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{
			DAGsDir:        "/dags",
			DocsDir:        "/dags/docs",
			LogDir:         "/logs",
			SessionsDir:    "/data/agent/sessions",
			WorkingDir:     "/work",
			BaseConfigFile: "/config/base.yaml",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleDeveloper})

		assert.NotEmpty(t, result)
		assert.Contains(t, result, "/dags")
		assert.Contains(t, result, "/dags/docs")
		assert.Contains(t, result, "Session Store Directory: /data/agent/sessions")
		assert.Contains(t, result, "/config/base.yaml")
		assert.Contains(t, result, "Authenticated role: developer")
	})

	t.Run("includes current DAG context", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		dag := &CurrentDAG{
			Name:     "test-dag",
			FilePath: "/dags/test-dag.yaml",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, CurrentDAG: dag, Role: auth.RoleAdmin})

		assert.NotEmpty(t, result)
		assert.Contains(t, result, "test-dag")
		assert.Contains(t, result, "Authenticated role: admin")
	})

	t.Run("includes available action guidance", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{
			BaseConfigFile: "/config/base.yaml",
			ReferencesDir:  "/data/agent/references",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleDeveloper})
		legacyReference := "executor" + "s.md"
		legacySchemaPath := "dagu schema dag steps." + "type"

		assert.Contains(t, result, "<actions>")
		assert.Contains(t, result, "Available actions are generated from the built-in action registry and base config files")
		assert.Contains(t, result, "Builtin actions:")
		assert.Contains(t, result, "`dag.run`")
		assert.Contains(t, result, "`http.request`")
		assert.Contains(t, result, "`kubernetes.run`")
		assert.Contains(t, result, "`k8s.run`")
		assert.Contains(t, result, "`chat.completion`")
		assert.Contains(t, result, "`agent.run`")
		assert.Contains(t, result, "`harness.run`")
		assert.Contains(t, result, "`redis.<operation>`")
		assert.Contains(t, result, "`file.write`")
		assert.Contains(t, result, "`artifact.write`")
		assert.Contains(t, result, "`artifact.read`")
		assert.Contains(t, result, "`artifact.list`")
		assert.Contains(t, result, "Use top-level `run:` for plain shell commands and scripts")
		assert.Contains(t, result, "prefer built-in `file.*` actions over `run:` commands")
		assert.Contains(t, result, "prefer built-in `artifact.write`, `artifact.read`, and `artifact.list`")
		assert.Contains(t, result, "Base config custom actions")
		assert.Contains(t, result, "Current DAG-local custom actions: inspect `actions:`")
		assert.Contains(t, result, "Legacy DAG-local `step_types:` definitions")
		assert.Contains(t, result, "`steptypes.md` — Built-in and custom actions")
		assert.Contains(t, result, "dagu schema dag steps.action")
		assert.NotContains(t, result, legacyReference)
		assert.NotContains(t, result, legacySchemaPath)
		assert.NotContains(t, result, "global Base Config (`/config/base.yaml`): unable to inspect")
	})

	t.Run("works with empty environment", func(t *testing.T) {
		t.Parallel()

		result := GenerateSystemPrompt(SystemPromptParams{Role: auth.RoleViewer})

		assert.NotEmpty(t, result)
		assert.Contains(t, result, "Authenticated role: viewer")
	})

	t.Run("no memory omits memory section", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleViewer})

		assert.NotContains(t, result, "<global_memory>")
		assert.NotContains(t, result, "<dag_memory")
		assert.NotContains(t, result, "<memory_paths>")
		assert.NotContains(t, result, "<memory_management>")
	})

	t.Run("includes global memory only", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		mem := MemoryContent{
			GlobalMemory: "User prefers concise output.",
			MemoryDir:    "/dags/memory",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Memory: mem, Role: auth.RoleViewer})

		assert.Contains(t, result, "<global_memory>")
		assert.Contains(t, result, "User prefers concise output.")
		assert.NotContains(t, result, "<dag_memory")
	})

	t.Run("includes both global and DAG memory", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		mem := MemoryContent{
			GlobalMemory: "Global info.",
			DAGMemory:    "DAG-specific info.",
			DAGName:      "my-etl",
			MemoryDir:    "/dags/memory",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Memory: mem, Role: auth.RoleViewer})

		assert.Contains(t, result, "<global_memory>")
		assert.Contains(t, result, "Global info.")
		assert.Contains(t, result, `<dag_memory dag="my-etl">`)
		assert.Contains(t, result, "DAG-specific info.")
	})

	t.Run("memory paths appear in output", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		mem := MemoryContent{
			MemoryDir: "/dags/memory",
			DAGName:   "test-dag",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Memory: mem, Role: auth.RoleViewer})

		assert.Contains(t, result, "/dags/memory/MEMORY.md")
		assert.Contains(t, result, "/dags/memory/dags/test-dag/MEMORY.md")
	})

	t.Run("memory management enforces DAG-first policy", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		mem := MemoryContent{
			MemoryDir: "/dags/memory",
			DAGName:   "new-etl",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Memory: mem, Role: auth.RoleViewer})

		assert.Contains(t, result, "If DAG context is available, save memory to Per-DAG by default (not Global)")
		assert.Contains(t, result, "After creating or updating a DAG, if anything should be remembered, create/update that DAG's memory file")
		assert.Contains(t, result, "Global memory is only for cross-DAG or user-wide stable preferences/policies")
	})

	t.Run("memory management requires confirmation before global write without DAG context", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		mem := MemoryContent{
			MemoryDir: "/dags/memory",
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Memory: mem, Role: auth.RoleViewer})

		assert.Contains(t, result, "If no DAG context is available, ask the user before writing to Global memory")
	})

	t.Run("read-only memory omits writable guidance and paths", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		mem := MemoryContent{
			GlobalMemory: "Remembered context.",
			DAGMemory:    "DAG context.",
			DAGName:      "my-etl",
			MemoryDir:    "/dags/memory",
			ReadOnly:     true,
		}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Memory: mem, Role: auth.RoleViewer})

		assert.Contains(t, result, "<memory_mode>")
		assert.Contains(t, result, "Memory is read-only execution context in this run.")
		assert.Contains(t, result, "Do not attempt to persist memory in this run.")
		assert.NotContains(t, result, "<memory_paths>")
		assert.NotContains(t, result, "<memory_management>")
	})

	t.Run("includes soul content when provided", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		soul := &Soul{Content: "test soul identity"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleViewer, Soul: soul})

		assert.NotEmpty(t, result)
		assert.Contains(t, result, "test soul identity")
	})

	t.Run("template-like syntax in soul content is not evaluated", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}
		soul := &Soul{Content: "You are {{.Name}} and use {{template}} things"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleViewer, Soul: soul})

		assert.NotEmpty(t, result)
		// The literal template syntax must appear in output, not be evaluated.
		assert.Contains(t, result, "You are {{.Name}} and use {{template}} things")
		// The identity tag must be present (soul content is rendered).
		assert.Contains(t, result, "<identity>")
		// Fallback prompt must NOT be used.
		assert.NotContains(t, result, "You are Dagu Assistant, an AI assistant")
	})

	t.Run("execution guidance prefers enqueue without preflight checks", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleDeveloper})

		assert.Contains(t, result, "Default to queue-based execution: `dagu enqueue <dag-name>`")
		assert.Contains(t, result, "Do not check running jobs, queued jobs")
		assert.Contains(t, result, "pass user parameters with `-p`")
		assert.Contains(t, result, `dagu enqueue my-dag -p 'topic="OpenAI new model released March 2026"'`)
		assert.Contains(t, result, "Avoid passing spaced values after `--`")
		assert.NotContains(t, result, "2. Start: `dagu start <dag-name>`")
	})

	t.Run("includes active progress reporting guidance", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleDeveloper})

		assert.Contains(t, result, "<communication>")
		assert.Contains(t, result, "Actively report your progress during multi-step work")
		assert.Contains(t, result, "Before using tools or starting a long-running action")
		assert.Contains(t, result, "Do not stay silent until the final answer")
		assert.Contains(t, result, "what you did, what you found, and what you will do next")
	})

	t.Run("documents session search tool", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleDeveloper})

		assert.Contains(t, result, "`session_search`: Search past persisted session transcripts")
	})

	t.Run("documents web tools using exact tool names", func(t *testing.T) {
		t.Parallel()

		result := GenerateSystemPrompt(SystemPromptParams{Role: auth.RoleDeveloper})

		assert.Contains(t, result, "`web_search`: Search the public web")
		assert.Contains(t, result, "`web_extract`: Extract readable text")
		assert.NotContains(t, result, "search_files")
		assert.NotContains(t, result, "read_file")
	})

	t.Run("documents runbook tool for docs store moves and deletes", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DocsDir: "/dags/docs"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleDeveloper})

		assert.Contains(t, result, "`runbook_manage`: List/search/get/create/update/patch/move/delete")
		assert.Contains(t, result, "Do not use `patch` to move, rename, delete, or maintain documents under /dags/docs")
		assert.Contains(t, result, "`runbook_manage` action `move` with `id` and `new_id`")
		assert.Contains(t, result, "`runbook_manage` action `delete`")
	})

	t.Run("authoring guidance prefers explicit actions for file and artifact operations", func(t *testing.T) {
		t.Parallel()
		env := EnvironmentInfo{DAGsDir: "/dags"}

		result := GenerateSystemPrompt(SystemPromptParams{Env: env, Role: auth.RoleDeveloper})

		assert.Contains(t, result, "Use the appropriate action (`http.request`, `s3.*`, `postgres.query`, `artifact.*`, `file.*`, etc.) instead of shelling out.")
		assert.Contains(t, result, "use `file.stat`, `file.read`, `file.write`, `file.copy`, `file.move`, `file.delete`, `file.mkdir`, or `file.list`")
		assert.Contains(t, result, "instead of shell commands such as `cat`, `cp`, `mv`, `rm`, or `mkdir`")
		assert.Contains(t, result, "For DAG-run outputs such as reports, JSON snapshots, Markdown summaries, and handoff files, use `artifact.write`, `artifact.read`, or `artifact.list`")
		assert.Contains(t, result, "Use `artifact.write`, `artifact.read`, and `artifact.list` for DAG-run artifacts")
		assert.Contains(t, result, "Use `run:` only when the step is actually shell logic or an installed CLI invocation.")
	})
}

func TestGenerateSystemPromptDynamicActions(t *testing.T) {
	t.Run("includes custom actions from base config file", func(t *testing.T) {
		dir := t.TempDir()
		baseConfigPath := filepath.Join(dir, "base.yaml")
		require.NoError(t, os.WriteFile(baseConfigPath, []byte(`
actions:
  report.write:
    description: Write a report
    input_schema:
      type: object
      additionalProperties: false
    template:
      run: echo report
`), 0o600))

		result := GenerateSystemPrompt(SystemPromptParams{
			Env:  EnvironmentInfo{BaseConfigFile: baseConfigPath},
			Role: auth.RoleDeveloper,
		})

		assert.Contains(t, result, "global Base Config")
		assert.Contains(t, result, "`report.write`")
	})

	t.Run("includes legacy step_types from base config as migration-only definitions", func(t *testing.T) {
		dir := t.TempDir()
		baseConfigPath := filepath.Join(dir, "base.yaml")
		require.NoError(t, os.WriteFile(baseConfigPath, []byte(`
step_types:
  report_writer:
    type: command
    description: Write a report
    input_schema:
      type: object
      additionalProperties: false
    template:
      command: echo report
`), 0o600))

		result := GenerateSystemPrompt(SystemPromptParams{
			Env:  EnvironmentInfo{BaseConfigFile: baseConfigPath},
			Role: auth.RoleDeveloper,
		})

		assert.Contains(t, result, "legacy `step_types:` definitions")
		assert.Contains(t, result, "`report_writer` -> `command`")
		assert.Contains(t, result, "Prefer `actions:` for new work")
	})

	t.Run("includes custom actions from workspace base config files", func(t *testing.T) {
		dir := t.TempDir()
		workspaceBaseDir := filepath.Join(dir, "workspaces", "ops")
		require.NoError(t, os.MkdirAll(workspaceBaseDir, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceBaseDir, "base.yaml"), []byte(`
actions:
  deploy.service:
    description: Deploy service
    input_schema:
      type: object
      additionalProperties: false
    template:
      run: echo deploy
`), 0o600))

		result := GenerateSystemPrompt(SystemPromptParams{
			Env:  EnvironmentInfo{DAGsDir: dir},
			Role: auth.RoleDeveloper,
		})

		assert.Contains(t, result, "workspace `ops`")
		assert.Contains(t, result, "`deploy.service`")
	})

	t.Run("filters workspace base config files by workspace access", func(t *testing.T) {
		dir := t.TempDir()
		for workspaceName, actionName := range map[string]string{
			"ops":  "deploy.service",
			"prod": "prod.release",
		} {
			workspaceBaseDir := filepath.Join(dir, "workspaces", workspaceName)
			require.NoError(t, os.MkdirAll(workspaceBaseDir, 0o750))
			require.NoError(t, os.WriteFile(filepath.Join(workspaceBaseDir, "base.yaml"), fmt.Appendf(nil, `
actions:
  %s:
    description: Workspace action
    input_schema:
      type: object
      additionalProperties: false
    template:
      run: echo workspace
`, actionName), 0o600))
		}

		result := GenerateSystemPrompt(SystemPromptParams{
			Env:  EnvironmentInfo{DAGsDir: dir},
			Role: auth.RoleViewer,
			WorkspaceAccess: &auth.WorkspaceAccess{
				Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleViewer}},
			},
		})

		assert.Contains(t, result, "workspace `ops`")
		assert.Contains(t, result, "`deploy.service`")
		assert.NotContains(t, result, "workspace `prod`")
		assert.NotContains(t, result, "prod.release")
	})

	t.Run("surfaces workspace base config directory inspection errors", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "workspaces"), []byte("not a directory"), 0o600))

		result := buildActionsPrompt(EnvironmentInfo{DAGsDir: dir}, nil)

		assert.Contains(t, result, "workspace base config directory")
		assert.Contains(t, result, "unable to inspect")
		assert.NotContains(t, result, "Base config custom actions: none found in configured base config files")
	})
}

func TestFallbackPrompt(t *testing.T) {
	t.Parallel()

	t.Run("includes DAGs directory", func(t *testing.T) {
		t.Parallel()

		result := fallbackPrompt(EnvironmentInfo{DAGsDir: "/my/dags"})

		assert.Contains(t, result, "/my/dags")
		assert.Contains(t, result, "Dagu Assistant")
	})

	t.Run("works with empty environment", func(t *testing.T) {
		t.Parallel()

		result := fallbackPrompt(EnvironmentInfo{})

		assert.NotEmpty(t, result)
		assert.Contains(t, result, "Dagu Assistant")
	})
}
