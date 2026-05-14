// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/spec"
	_ "github.com/dagucloud/dagu/internal/runtime/builtin/harness"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("WithName", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), testDAG, spec.WithName("testDAG"))
		require.NoError(t, err)
		require.Equal(t, "testDAG", dag.Name)
	})

	// Error cases
	errorTests := []struct {
		name        string
		content     string
		useFile     bool
		errContains string
	}{
		{
			name:    "InvalidPath",
			useFile: false,
		},
		{
			name:        "UnknownField",
			content:     "invalidKey: test\n",
			useFile:     true,
			errContains: "has invalid keys: invalidKey",
		},
		{
			name:        "InvalidYAML",
			content:     "invalidyaml",
			useFile:     true,
			errContains: "invalidyaml",
		},
	}

	for _, tt := range errorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var testDAG string
			if tt.useFile {
				testDAG = createTempYAMLFile(t, tt.content)
			} else {
				testDAG = "/tmp/non_existing_file_" + t.Name() + ".yaml"
			}
			_, err := spec.Load(context.Background(), testDAG)
			require.Error(t, err)
			if tt.errContains != "" {
				require.Contains(t, err.Error(), tt.errContains)
			}
		})
	}

	t.Run("MetadataOnly", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
log_dir: /var/log/dagu
hist_retention_days: 90
max_clean_up_time_sec: 60
mail_on:
  failure: true
steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), testDAG, spec.OnlyMetadata())
		require.NoError(t, err)
		// Steps should not be loaded in metadata-only mode
		require.Empty(t, dag.Steps)
		// Non-metadata fields from YAML should NOT be populated in metadata-only mode
		assert.Empty(t, dag.LogDir, "LogDir should be empty in metadata-only mode")
		assert.Nil(t, dag.MailOn, "MailOn should be nil in metadata-only mode")
		// Defaults are still applied by InitializeDefaults (not from YAML)
		assert.Equal(t, 30, dag.HistRetentionDays, "HistRetentionDays should be default (30), not YAML value (90)")
		assert.Equal(t, 5*time.Second, dag.MaxCleanUpTime, "MaxCleanUpTime should be default (5s), not YAML value (60s)")
	})
	t.Run("DefaultConfig", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), testDAG)

		require.NoError(t, err)

		// DAG level
		assert.Equal(t, "", dag.LogDir)
		assert.Equal(t, testDAG, dag.Location)
		assert.Equal(t, time.Second*5, dag.MaxCleanUpTime)
		assert.Equal(t, 30, dag.HistRetentionDays)
		assert.Equal(t, 0, dag.HistRetentionRuns)

		// Step level
		require.Len(t, dag.Steps, 1)
		assert.Equal(t, "1", dag.Steps[0].Name, "1")
		require.Len(t, dag.Steps[0].Commands, 1)
		assert.Equal(t, "true", dag.Steps[0].Commands[0].Command)
	})
	t.Run("HistoryRetentionRuns", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `hist_retention_runs: 3
steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)

		assert.Equal(t, 0, dag.HistRetentionDays)
		assert.Equal(t, 3, dag.HistRetentionRuns)
	})
	t.Run("RejectBothHistoryRetentionModesInSameDocument", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `hist_retention_days: 7
hist_retention_runs: 3
steps:
  - name: "1"
    run: "true"
`)
		_, err := spec.Load(context.Background(), testDAG)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hist_retention_days and hist_retention_runs cannot both be specified")
	})
	t.Run("OverrideConfig", func(t *testing.T) {
		t.Parallel()

		// Base config has the following values:
		// MailOn: {Failure: true, Success: false}
		base := createTempYAMLFile(t, `env:
  LOG_DIR: "${HOME}/logs"
log_dir: "${LOG_DIR}"
smtp:
  host: "smtp.host"
  port: "25"
error_mail:
  from: "system@mail.com"
  to: "error@mail.com"
  prefix: "[ERROR]"
info_mail:
  from: "system@mail.com"
  to: "info@mail.com"
  prefix: "[INFO]"
mail_on:
  failure: true
`)
		// Overwrite the base config with the following values:
		// MailOn: {Failure: false, Success: true}
		testDAG := createTempYAMLFile(t, `mail_on:
  failure: false
  success: true

hist_retention_days: 7

steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), testDAG, spec.WithBaseConfig(base))
		require.NoError(t, err)

		// Check if the MailOn values are overwritten
		assert.False(t, dag.MailOn.Failure)
		assert.True(t, dag.MailOn.Success)
	})
	t.Run("OverrideErrorMailPrefixOnly", func(t *testing.T) {
		t.Parallel()

		// Base config has error_mail with all fields set
		base := createTempYAMLFile(t, `error_mail:
  from: "base@example.com"
  to: "error@example.com"
  prefix: "[BASE-ERROR]"
  attach_logs: true
info_mail:
  from: "base@example.com"
  to: "info@example.com"
  prefix: "[BASE-INFO]"
wait_mail:
  from: "base@example.com"
  to: "wait@example.com"
  prefix: "[BASE-WAIT]"
`)
		// Child DAG only overrides prefix - this should work without
		// requiring other fields to be specified (GitHub issue #1512)
		testDAG := createTempYAMLFile(t, `error_mail:
  prefix: "[OVERRIDE-ERROR]"
info_mail:
  prefix: "[OVERRIDE-INFO]"
wait_mail:
  prefix: "[OVERRIDE-WAIT]"

steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), testDAG, spec.WithBaseConfig(base))
		require.NoError(t, err)

		// Check if error_mail prefix is overridden
		require.NotNil(t, dag.ErrorMail)
		assert.Equal(t, "[OVERRIDE-ERROR]", dag.ErrorMail.Prefix, "error_mail prefix should be overridden")
		// Other fields should be inherited from base
		assert.Equal(t, "base@example.com", dag.ErrorMail.From, "error_mail from should be inherited from base")
		assert.Equal(t, []string{"error@example.com"}, dag.ErrorMail.To, "error_mail to should be inherited from base")
		assert.True(t, dag.ErrorMail.AttachLogs, "error_mail attach_logs should be inherited from base")

		// Check if info_mail prefix is overridden
		require.NotNil(t, dag.InfoMail)
		assert.Equal(t, "[OVERRIDE-INFO]", dag.InfoMail.Prefix, "info_mail prefix should be overridden")
		// Other fields should be inherited from base
		assert.Equal(t, "base@example.com", dag.InfoMail.From, "info_mail from should be inherited from base")
		assert.Equal(t, []string{"info@example.com"}, dag.InfoMail.To, "info_mail to should be inherited from base")

		// Check if wait_mail prefix is overridden
		require.NotNil(t, dag.WaitMail)
		assert.Equal(t, "[OVERRIDE-WAIT]", dag.WaitMail.Prefix, "wait_mail prefix should be overridden")
		// Other fields should be inherited from base
		assert.Equal(t, "base@example.com", dag.WaitMail.From, "wait_mail from should be inherited from base")
		assert.Equal(t, []string{"wait@example.com"}, dag.WaitMail.To, "wait_mail to should be inherited from base")
	})
	t.Run("OverrideSMTPCredentialsOnly", func(t *testing.T) {
		t.Parallel()

		// Base config has smtp with host and port set
		base := createTempYAMLFile(t, `smtp:
  host: "smtp.base.com"
  port: "587"
`)
		// Child DAG only overrides username and password
		// This should work and inherit host/port from base
		testDAG := createTempYAMLFile(t, `smtp:
  username: "override-user"
  password: "override-pass"

steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), testDAG, spec.WithBaseConfig(base))
		require.NoError(t, err)

		// Check if SMTP username and password are set (from child)
		require.NotNil(t, dag.SMTP)
		assert.Equal(t, "override-user", dag.SMTP.Username, "smtp username should be set")
		assert.Equal(t, "override-pass", dag.SMTP.Password, "smtp password should be set")
		// Host and port should be inherited from base
		assert.Equal(t, "smtp.base.com", dag.SMTP.Host, "smtp host should be inherited from base")
		assert.Equal(t, "587", dag.SMTP.Port, "smtp port should be inherited from base")
	})
	t.Run("WaitMailConfig", func(t *testing.T) {
		t.Parallel()

		// Test wait_mail loading independently with all fields
		dagFile := createTempYAMLFile(t, `
wait_mail:
  from: "wait@example.com"
  to:
    - "approvers@example.com"
    - "managers@example.com"
  prefix: "[APPROVAL REQUIRED]"
  attach_logs: false

mail_on:
  wait: true

steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), dagFile)
		require.NoError(t, err)

		require.NotNil(t, dag.WaitMail)
		assert.Equal(t, "wait@example.com", dag.WaitMail.From)
		assert.Equal(t, []string{"approvers@example.com", "managers@example.com"}, dag.WaitMail.To)
		assert.Equal(t, "[APPROVAL REQUIRED]", dag.WaitMail.Prefix)
		assert.False(t, dag.WaitMail.AttachLogs)

		require.NotNil(t, dag.MailOn)
		assert.True(t, dag.MailOn.Wait)
	})
	t.Run("WaitMailSingleRecipient", func(t *testing.T) {
		t.Parallel()

		// Test wait_mail with single recipient (string format)
		dagFile := createTempYAMLFile(t, `
wait_mail:
  from: "wait@example.com"
  to: "single@example.com"
  prefix: "[WAIT]"

steps:
  - name: "1"
    run: "true"
`)
		dag, err := spec.Load(context.Background(), dagFile)
		require.NoError(t, err)

		require.NotNil(t, dag.WaitMail)
		assert.Equal(t, "wait@example.com", dag.WaitMail.From)
		assert.Equal(t, []string{"single@example.com"}, dag.WaitMail.To)
		assert.Equal(t, "[WAIT]", dag.WaitMail.Prefix)
		assert.False(t, dag.WaitMail.AttachLogs)
	})
}

func TestLoad_HarnessDefinitionsBaseConfigMerge(t *testing.T) {
	t.Parallel()

	t.Run("SameNameOverrideReplacesWholeDefinition", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
harnesses:
  gemini:
    binary: gemini
    prefix_args: ["run"]
    prompt_mode: flag
    prompt_flag: --prompt
    option_flags:
      model: --model
steps:
  - run: echo base
`)
		child := createTempYAMLFile(t, `
harnesses:
  gemini:
    binary: aider
    prompt_mode: stdin
steps:
  - action: harness.run
    with:
      prompt: Review this repository
      provider: gemini
`)

		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		require.Contains(t, dag.Harnesses, "gemini")

		def := dag.Harnesses["gemini"]
		require.NotNil(t, def)
		assert.Equal(t, "aider", def.Binary)
		assert.Nil(t, def.PrefixArgs)
		assert.Equal(t, core.HarnessPromptModeStdin, def.PromptMode)
		assert.Empty(t, def.PromptFlag)
		assert.Nil(t, def.OptionFlags)
	})

	t.Run("NullEntryDeletesInheritedDefinition", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
harnesses:
  gemini:
    binary: gemini
    prompt_mode: flag
    prompt_flag: --prompt
steps:
  - run: echo base
`)
		child := createTempYAMLFile(t, `
harnesses:
  gemini: null
steps:
  - run: echo child
`)

		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		assert.Nil(t, dag.Harnesses)
	})

	t.Run("NullDeletionMakesReferenceInvalid", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
harnesses:
  gemini:
    binary: gemini
    prompt_mode: flag
    prompt_flag: --prompt
steps:
  - run: echo base
`)
		child := createTempYAMLFile(t, `
harnesses:
  gemini: null
steps:
  - action: harness.run
    with:
      prompt: Review this repository
      provider: gemini
`)

		_, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.Error(t, err)
		require.Contains(t, err.Error(), `unknown provider "gemini"`)
	})
}

func TestLoad_BaseHarnessConfigBuildsChildSteps(t *testing.T) {
	t.Parallel()

	t.Run("InheritsBaseHarnessDefaults", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
harnesses:
  passthrough:
    binary: cat
    prompt_mode: stdin
harness:
  provider: passthrough
`)
		child := createTempYAMLFile(t, `
steps:
  - action: harness.run
    with:
      prompt: Review the repository
      stdin: |
        summarize the current branch
`)

		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		step := dag.Steps[0]
		assert.Equal(t, "harness", step.ExecutorConfig.Type)
		assert.Equal(t, "passthrough", step.ExecutorConfig.Config["provider"])
		assert.Equal(t, "summarize the current branch", step.Script)
	})

	t.Run("ChildStepCanReferenceBaseHarnessDefinition", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
harnesses:
  passthrough:
    binary: cat
    prompt_mode: stdin
`)
		child := createTempYAMLFile(t, `
steps:
  - action: harness.run
    with:
      prompt: Review the repository
      provider: passthrough
`)

		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		step := dag.Steps[0]
		assert.Equal(t, "harness", step.ExecutorConfig.Type)
		assert.Equal(t, "passthrough", step.ExecutorConfig.Config["provider"])
	})
}

func TestLoadBaseConfig(t *testing.T) {
	t.Parallel()

	t.Run("LoadBaseConfigFile", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `env:
  LOG_DIR: "${HOME}/logs"
log_dir: "${LOG_DIR}"
smtp:
  host: "smtp.host"
  port: "25"
error_mail:
  from: "system@mail.com"
  to: "error@mail.com"
  prefix: "[ERROR]"
info_mail:
  from: "system@mail.com"
  to: "info@mail.com"
  prefix: "[INFO]"
mail_on:
  failure: true
`)
		dag, err := spec.LoadBaseConfig(spec.BuildContext{}, testDAG)
		require.NotNil(t, dag)
		require.NoError(t, err)
	})
	t.Run("InheritBaseConfig", func(t *testing.T) {
		t.Parallel()

		// Base config with multiple inheritable fields
		baseDAG := createTempYAMLFile(t, `
env:
  BASE_ENV: "base_value"
  OVERWRITE_ENV: "base_overwrite_value"

log_dir: "/base/logs"
log_output: merged
hist_retention_days: 90
max_clean_up_time_sec: 120

llm:
  provider: openai
  model: gpt-4o
  system: "Base system prompt"
`)

		// Child DAG inherits all base config fields
		childDAG := createTempYAMLFile(t, `
env:
  SUB_ENV: "sub_value"
  OVERWRITE_ENV: "sub_overwrite_value"

steps:
  - name: "step1"
    run: echo "step1"
`)
		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)

		// Env inheritance: base + child merged, child overrides base
		assert.Contains(t, dag.Env, "BASE_ENV=base_value")
		assert.Contains(t, dag.Env, "SUB_ENV=sub_value")
		assert.Contains(t, dag.Env, "OVERWRITE_ENV=sub_overwrite_value")

		// LogDir inherited from base
		assert.Equal(t, "/base/logs", dag.LogDir)

		// LogOutput inherited from base
		assert.Equal(t, core.LogOutputMerged, dag.LogOutput)

		// HistRetentionDays inherited from base
		assert.Equal(t, 90, dag.HistRetentionDays)

		// MaxCleanUpTime inherited from base
		assert.Equal(t, 120*time.Second, dag.MaxCleanUpTime)

		// LLM inherited from base
		require.NotNil(t, dag.LLM)
		assert.Equal(t, "openai", dag.LLM.Provider)
		assert.Equal(t, "gpt-4o", dag.LLM.Model)
		assert.Equal(t, "Base system prompt", dag.LLM.System)
	})

	t.Run("HistoryRetentionRunsOverrideBaseDays", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `hist_retention_days: 90
`)
		childDAG := createTempYAMLFile(t, `hist_retention_runs: 3
steps:
  - name: "step1"
    run: echo "step1"
`)
		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)

		assert.Equal(t, 0, dag.HistRetentionDays)
		assert.Equal(t, 3, dag.HistRetentionRuns)
	})

	t.Run("HistoryRetentionDaysOverrideBaseRuns", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `hist_retention_runs: 5
`)
		childDAG := createTempYAMLFile(t, `hist_retention_days: 7
steps:
  - name: "step1"
    run: echo "step1"
`)
		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)

		assert.Equal(t, 7, dag.HistRetentionDays)
		assert.Equal(t, 0, dag.HistRetentionRuns)
	})

	t.Run("HistoryRetentionDaysZeroOverrideBaseRuns", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `hist_retention_runs: 5
`)
		childDAG := createTempYAMLFile(t, `hist_retention_days: 0
steps:
  - name: "step1"
    run: echo "step1"
`)
		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)

		assert.Equal(t, 0, dag.HistRetentionRuns)
	})

	t.Run("WithBaseConfigContent_MergesEnvVars", func(t *testing.T) {
		t.Parallel()

		// Simulate embedded base config content (as used in distributed mode)
		baseContent := []byte(`
env:
  BASE_ENV: "base_value"
  OVERWRITE_ENV: "base_overwrite_value"

log_dir: "/base/logs"
hist_retention_days: 90
`)

		childDAG := createTempYAMLFile(t, `
env:
  SUB_ENV: "sub_value"
  OVERWRITE_ENV: "sub_overwrite_value"

steps:
  - name: "step1"
    run: echo "step1"
`)
		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfigContent(baseContent))
		require.NoError(t, err)
		require.NotNil(t, dag)

		// Env vars from embedded base config should be merged
		assert.Contains(t, dag.Env, "BASE_ENV=base_value")
		assert.Contains(t, dag.Env, "SUB_ENV=sub_value")
		assert.Contains(t, dag.Env, "OVERWRITE_ENV=sub_overwrite_value")

		// Other fields should be inherited
		assert.Equal(t, "/base/logs", dag.LogDir)
		assert.Equal(t, 90, dag.HistRetentionDays)

		// BaseConfigData should be populated for propagation
		assert.NotEmpty(t, dag.BaseConfigData)
	})

	t.Run("WithBaseConfigContent_MatchesWithBaseConfig", func(t *testing.T) {
		t.Parallel()

		// Both WithBaseConfig (file) and WithBaseConfigContent (bytes) should
		// produce identical DAGs when given the same base config content.
		baseYAML := `
env:
  MY_VAR: "hello"

log_dir: "/shared/logs"
hist_retention_days: 30
`
		baseFile := createTempYAMLFile(t, baseYAML)
		childDAG := createTempYAMLFile(t, `
steps:
  - name: "step1"
    run: echo "${MY_VAR}"
`)

		dagFromFile, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseFile))
		require.NoError(t, err)

		dagFromContent, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfigContent([]byte(baseYAML)))
		require.NoError(t, err)

		// Env, LogDir, HistRetentionDays should match
		assert.Equal(t, dagFromFile.Env, dagFromContent.Env)
		assert.Equal(t, dagFromFile.LogDir, dagFromContent.LogDir)
		assert.Equal(t, dagFromFile.HistRetentionDays, dagFromContent.HistRetentionDays)

		// Both should have BaseConfigData populated
		assert.NotEmpty(t, dagFromFile.BaseConfigData)
		assert.NotEmpty(t, dagFromContent.BaseConfigData)
	})

	t.Run("WithBaseConfigContent_PrecedenceOverFile", func(t *testing.T) {
		t.Parallel()

		// When both WithBaseConfig and WithBaseConfigContent are set,
		// embedded content should take precedence.
		fileBase := createTempYAMLFile(t, `
env:
  SOURCE: "file"
`)
		embeddedContent := []byte(`
env:
  SOURCE: "embedded"
`)

		childDAG := createTempYAMLFile(t, `
steps:
  - name: "step1"
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), childDAG,
			spec.WithBaseConfig(fileBase),
			spec.WithBaseConfigContent(embeddedContent),
		)
		require.NoError(t, err)
		assert.Contains(t, dag.Env, "SOURCE=embedded")
	})

	t.Run("WithWorkspaceBaseConfigDir_MergesNamedWorkspaceConfig", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		globalBase := filepath.Join(root, "base.yaml")
		require.NoError(t, os.WriteFile(globalBase, []byte(`
env:
  GLOBAL_ONLY: "global"
  SHARED: "global"
log_dir: "/global/logs"
hist_retention_days: 30
`), 0600))

		workspaceConfigDir := filepath.Join(root, "workspaces")
		require.NoError(t, os.MkdirAll(filepath.Join(workspaceConfigDir, "ops"), 0750))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceConfigDir, "ops", "base.yaml"), []byte(`
env:
  WORKSPACE_ONLY: "ops"
  SHARED: "workspace"
log_dir: "/workspace/logs"
max_active_steps: 7
`), 0600))

		childDAG := createTempYAMLFile(t, `
labels:
  - workspace=ops
env:
  DAG_ONLY: "dag"
steps:
  - name: "step1"
    run: echo "step1"
`)

		dag, err := spec.Load(context.Background(), childDAG,
			spec.WithBaseConfig(globalBase),
			spec.WithWorkspaceBaseConfigDir(workspaceConfigDir),
		)
		require.NoError(t, err)

		assert.Contains(t, dag.Env, "GLOBAL_ONLY=global")
		assert.Contains(t, dag.Env, "WORKSPACE_ONLY=ops")
		assert.Contains(t, dag.Env, "DAG_ONLY=dag")
		assert.Contains(t, dag.Env, "SHARED=workspace")
		assert.Equal(t, "/workspace/logs", dag.LogDir)
		assert.Equal(t, 30, dag.HistRetentionDays)
		assert.Equal(t, 7, dag.MaxActiveSteps)
		assert.Contains(t, string(dag.BaseConfigData), "WORKSPACE_ONLY")
	})

	t.Run("WithWorkspaceBaseConfigDir_MergesListSyntaxEnv", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		globalBase := filepath.Join(root, "base.yaml")
		require.NoError(t, os.WriteFile(globalBase, []byte(`
env:
  - GLOBAL_ONLY=global
  - SHARED=global
`), 0600))

		workspaceConfigDir := filepath.Join(root, "workspaces")
		require.NoError(t, os.MkdirAll(filepath.Join(workspaceConfigDir, "ops"), 0750))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceConfigDir, "ops", "base.yaml"), []byte(`
env:
  - WORKSPACE_ONLY=ops
  - SHARED=workspace
`), 0600))

		childDAG := createTempYAMLFile(t, `
labels:
  - workspace=ops
steps:
  - name: "step1"
    run: echo "step1"
`)

		dag, err := spec.Load(context.Background(), childDAG,
			spec.WithBaseConfig(globalBase),
			spec.WithWorkspaceBaseConfigDir(workspaceConfigDir),
		)
		require.NoError(t, err)

		assert.Contains(t, dag.Env, "GLOBAL_ONLY=global")
		assert.Contains(t, dag.Env, "WORKSPACE_ONLY=ops")
		assert.Contains(t, dag.Env, "SHARED=workspace")
	})

	t.Run("WithWorkspaceBaseConfigDir_IgnoresDefaultWorkspace", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		globalBase := filepath.Join(root, "base.yaml")
		require.NoError(t, os.WriteFile(globalBase, []byte(`
env:
  GLOBAL_ONLY: "global"
log_dir: "/global/logs"
`), 0600))

		workspaceConfigDir := filepath.Join(root, "workspaces")
		require.NoError(t, os.MkdirAll(filepath.Join(workspaceConfigDir, "default"), 0750))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceConfigDir, "default", "base.yaml"), []byte(`
env:
  SHOULD_NOT_APPLY: "default"
log_dir: "/default/logs"
`), 0600))

		childDAG := createTempYAMLFile(t, `
steps:
  - name: "step1"
    run: echo "step1"
`)

		dag, err := spec.Load(context.Background(), childDAG,
			spec.WithBaseConfig(globalBase),
			spec.WithWorkspaceBaseConfigDir(workspaceConfigDir),
		)
		require.NoError(t, err)

		assert.Contains(t, dag.Env, "GLOBAL_ONLY=global")
		assert.NotContains(t, dag.Env, "SHOULD_NOT_APPLY=default")
		assert.Equal(t, "/global/logs", dag.LogDir)
	})

	t.Run("OverrideBaseConfig", func(t *testing.T) {
		t.Parallel()

		// Base config with multiple inheritable fields
		baseDAG := createTempYAMLFile(t, `
env:
  BASE_ENV: "base_value"

log_dir: "/base/logs"
log_output: merged
hist_retention_days: 90
max_clean_up_time_sec: 120

llm:
  provider: openai
  model: gpt-4o
  system: "Base system prompt"
`)

		// Child DAG overrides specific fields
		overrideDAG := createTempYAMLFile(t, `
log_dir: "/override/logs"
log_output: separate
hist_retention_days: 7
max_clean_up_time_sec: 30

llm:
  provider: anthropic
  model: claude-sonnet-4-20250514
  system: "Override system prompt"

steps:
  - name: "step1"
    run: echo "step1"
`)
		dag, err := spec.Load(context.Background(), overrideDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)

		// LogDir overridden
		assert.Equal(t, "/override/logs", dag.LogDir)

		// LogOutput overridden
		assert.Equal(t, core.LogOutputSeparate, dag.LogOutput)

		// HistRetentionDays overridden
		assert.Equal(t, 7, dag.HistRetentionDays)

		// MaxCleanUpTime overridden
		assert.Equal(t, 30*time.Second, dag.MaxCleanUpTime)

		// LLM overridden
		require.NotNil(t, dag.LLM)
		assert.Equal(t, "anthropic", dag.LLM.Provider)
		assert.Equal(t, "claude-sonnet-4-20250514", dag.LLM.Model)
		assert.Equal(t, "Override system prompt", dag.LLM.System)

		// Env still inherited from base (since not specified in override DAG)
		assert.Contains(t, dag.Env, "BASE_ENV=base_value")
	})

	t.Run("InheritBaseArtifactsConfig", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
artifacts:
  enabled: true
  dir: "/base/artifacts"
`)

		childDAG := createTempYAMLFile(t, `
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)
		require.NotNil(t, dag.Artifacts)
		assert.True(t, dag.Artifacts.Enabled)
		assert.Equal(t, "/base/artifacts", dag.Artifacts.Dir)
	})

	t.Run("OverrideBaseArtifactsConfig", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
artifacts:
  enabled: true
  dir: "/base/artifacts"
`)

		childDAG := createTempYAMLFile(t, `
artifacts:
  enabled: true
  dir: "/override/artifacts"
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)
		require.NotNil(t, dag.Artifacts)
		assert.True(t, dag.Artifacts.Enabled)
		assert.Equal(t, "/override/artifacts", dag.Artifacts.Dir)
	})

	t.Run("InheritBaseWebhookForwardHeaders", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
webhook:
  forward_headers:
    - X-GitHub-Event
    - X-GitHub-Delivery
`)

		childDAG := createTempYAMLFile(t, `
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)
		require.NotNil(t, dag.Webhook)
		assert.Equal(t, []string{"x-github-event", "x-github-delivery"}, dag.Webhook.ForwardHeaders)
	})

	t.Run("OverrideBaseWebhookForwardHeaders", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
webhook:
  forward_headers:
    - X-GitHub-Event
    - X-GitHub-Delivery
`)

		childDAG := createTempYAMLFile(t, `
webhook:
  forward_headers:
    - Stripe-Idempotency-Key
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)
		require.NotNil(t, dag.Webhook)
		assert.Equal(t, []string{"stripe-idempotency-key"}, dag.Webhook.ForwardHeaders)
	})

	t.Run("ClearInheritedWebhookForwardHeaders", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
webhook:
  forward_headers:
    - X-GitHub-Event
`)

		childDAG := createTempYAMLFile(t, `
webhook:
  forward_headers: []
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)
		require.NotNil(t, dag.Webhook)
		assert.Empty(t, dag.Webhook.ForwardHeaders)
	})

	t.Run("EmptyWebhookObjectClearsInheritedWebhookConfig", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
webhook:
  forward_headers:
    - X-GitHub-Event
`)

		childDAG := createTempYAMLFile(t, `
webhook: {}
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		require.NotNil(t, dag)
		require.NotNil(t, dag.Webhook)
		assert.Empty(t, dag.Webhook.ForwardHeaders)
	})

	t.Run("RejectAuthorizationInWebhookForwardHeaders", func(t *testing.T) {
		t.Parallel()

		dagPath := createTempYAMLFile(t, `
webhook:
  forward_headers:
    - Authorization
steps:
  - name: "step1"
    run: echo "test"
`)

		_, err := spec.Load(context.Background(), dagPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "authorization")
	})

	t.Run("InheritBaseWorkingDir", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
working_dir: /shared/workspace
`)

		childDAG := createTempYAMLFile(t, `
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)

		// Child should inherit base's working_dir
		expected := "/shared/workspace"
		if runtime.GOOS == "windows" {
			expected = filepath.Join(filepath.Dir(baseDAG), filepath.FromSlash("/shared/workspace"))
		}
		assert.Equal(t, expected, dag.WorkingDir)
	})

	t.Run("OverrideBaseWorkingDir", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `
working_dir: /shared/workspace
`)

		childDAG := createTempYAMLFile(t, `
working_dir: /my/custom/dir
steps:
  - name: "step1"
    run: echo "test"
`)

		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)

		// Child's explicit working_dir should override base
		expected := "/my/custom/dir"
		if runtime.GOOS == "windows" {
			expected = filepath.Join(filepath.Dir(childDAG), filepath.FromSlash("/my/custom/dir"))
		}
		assert.Equal(t, expected, dag.WorkingDir)
	})
}

func TestWorkingDirExplicit(t *testing.T) {
	t.Parallel()

	t.Run("ExplicitlySet", func(t *testing.T) {
		t.Parallel()

		dagFile := createTempYAMLFile(t, `
working_dir: /custom
steps:
  - name: a
    run: echo hi
`)
		dag, err := spec.Load(context.Background(), dagFile)
		require.NoError(t, err)
		assert.True(t, dag.WorkingDirExplicit)
	})

	t.Run("DefaultedToFileDir", func(t *testing.T) {
		t.Parallel()

		dagFile := createTempYAMLFile(t, `steps:
  - name: a
    run: echo hi
`)
		dag, err := spec.Load(context.Background(), dagFile)
		require.NoError(t, err)
		assert.False(t, dag.WorkingDirExplicit)
	})

	t.Run("FromBaseConfig", func(t *testing.T) {
		t.Parallel()

		baseDAG := createTempYAMLFile(t, `working_dir: /shared/workspace`)
		childDAG := createTempYAMLFile(t, `steps:
  - name: a
    run: echo hi
`)
		dag, err := spec.Load(context.Background(), childDAG, spec.WithBaseConfig(baseDAG))
		require.NoError(t, err)
		assert.True(t, dag.WorkingDirExplicit)
	})

	t.Run("FromDefaultWorkingDir", func(t *testing.T) {
		t.Parallel()

		dagFile := createTempYAMLFile(t, `steps:
  - name: a
    run: echo hi
`)
		dag, err := spec.Load(context.Background(), dagFile, spec.WithDefaultWorkingDir("/default"))
		require.NoError(t, err)
		assert.True(t, dag.WorkingDirExplicit)
	})
}

func TestLoadYAML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		wantErr     bool
		wantName    string
		wantCommand string
	}{
		{
			name: "ValidYAMLData",
			input: `steps:
  - name: "1"
    run: "true"
`,
			wantName:    "1",
			wantCommand: "true",
		},
		{
			name:    "InvalidYAMLData",
			input:   "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ret, err := spec.LoadYAMLWithOpts(context.Background(), []byte(tt.input), spec.BuildOpts{})
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Len(t, ret.Steps, 1)
			assert.Equal(t, tt.wantName, ret.Steps[0].Name)
			require.Len(t, ret.Steps[0].Commands, 1)
			assert.Equal(t, tt.wantCommand, ret.Steps[0].Commands[0].Command)
			assert.Empty(t, ret.SourceFile)
		})
	}
}

func TestLoadPreservesSourceFileForFileBasedDAG(t *testing.T) {
	t.Parallel()

	dagFile := createTempYAMLFile(t, `steps:
  - name: a
    run: echo hi
`)

	dag, err := spec.Load(context.Background(), dagFile)
	require.NoError(t, err)
	assert.Equal(t, dagFile, dag.Location)
	assert.Equal(t, dagFile, dag.SourceFile)
}

func TestLoadYAMLWithOpts_MarksConfiguredWorkingDirExplicit(t *testing.T) {
	t.Parallel()

	t.Run("FromYAML", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		dag, err := spec.LoadYAMLWithOpts(context.Background(), fmt.Appendf(nil, `
working_dir: %q
steps:
  - name: step1
    run: echo hello
`, workDir), spec.BuildOpts{})
		require.NoError(t, err)
		assert.Equal(t, workDir, dag.WorkingDir)
		assert.True(t, dag.WorkingDirExplicit)
	})

	t.Run("FromBaseConfigContent", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
steps:
  - name: step1
    run: echo hello
`), spec.BuildOpts{
			BaseConfigContent: fmt.Appendf(nil, "working_dir: %q", workDir),
		})
		require.NoError(t, err)
		assert.Equal(t, workDir, dag.WorkingDir)
		assert.True(t, dag.WorkingDirExplicit)
	})

	t.Run("FromDefaultWorkingDir", func(t *testing.T) {
		t.Parallel()

		workDir := t.TempDir()
		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
steps:
  - name: step1
    run: echo hello
`), spec.BuildOpts{
			DefaultWorkingDir: workDir,
		})
		require.NoError(t, err)
		assert.Equal(t, workDir, dag.WorkingDir)
		assert.True(t, dag.WorkingDirExplicit)
	})
}

func TestLoadYAMLWithOpts_PreservesLegacyContract(t *testing.T) {
	t.Parallel()

	t.Run("DoesNotInitializeDefaults", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
name: test-dag
steps:
  - name: step1
    run: echo hello
`), spec.BuildOpts{})
		require.NoError(t, err)
		assert.Equal(t, core.LogOutputMode(""), dag.LogOutput)
	})

	t.Run("DoesNotSynthesizeWorkingDirWithoutContext", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
steps:
  - name: step1
    run: echo hello
`), spec.BuildOpts{})
		require.NoError(t, err)
		assert.Empty(t, dag.WorkingDir)
		assert.False(t, dag.WorkingDirExplicit)
	})

	t.Run("WithoutBaseConfigStillDefaultsTypeToChain", func(t *testing.T) {
		t.Parallel()

		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
steps:
  - name: step1
    run: echo one
  - name: step2
    run: echo two
`), spec.BuildOpts{})
		require.NoError(t, err)
		assert.Equal(t, core.TypeChain, dag.Type)
		require.Len(t, dag.Steps, 2)
		assert.Equal(t, []string{"step1"}, dag.Steps[1].Depends)
	})
}

func TestLoad_TypeInheritanceFromBaseConfig(t *testing.T) {
	t.Parallel()

	t.Run("InheritsGraphTypeWhenChildOmitsType", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, "type: graph\n")
		child := createTempYAMLFile(t, `
steps:
  - name: first
    run: echo first
  - name: second
    run: echo second
`)

		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		assert.Equal(t, core.TypeGraph, dag.Type)
		require.Len(t, dag.Steps, 2)
		assert.Empty(t, dag.Steps[1].Depends)
	})

	t.Run("ExplicitChildTypeOverridesBaseType", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, "type: graph\n")
		child := createTempYAMLFile(t, `
type: chain
steps:
  - name: first
    run: echo first
  - name: second
    run: echo second
`)

		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		assert.Equal(t, core.TypeChain, dag.Type)
		require.Len(t, dag.Steps, 2)
		assert.Equal(t, []string{"first"}, dag.Steps[1].Depends)
	})

	t.Run("InheritsChainTypeBeforeBuildToInjectDependencies", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, "type: chain\n")
		child := createTempYAMLFile(t, `
steps:
  - name: first
    run: echo first
  - name: second
    run: echo second
`)

		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		assert.Equal(t, core.TypeChain, dag.Type)
		require.Len(t, dag.Steps, 2)
		assert.Equal(t, []string{"first"}, dag.Steps[1].Depends)
	})
}

func TestLoadYAMLWithNameOption(t *testing.T) {
	t.Parallel()
	const testDAG = `
steps:
  - name: "1"
    run: "true"
`

	ret, err := spec.LoadYAMLWithOpts(context.Background(), []byte(testDAG), spec.BuildOpts{
		Name: "testDAG",
	})
	require.NoError(t, err)
	require.Equal(t, "testDAG", ret.Name)

	step := ret.Steps[0]
	require.Equal(t, "1", step.Name)
	require.Len(t, step.Commands, 1)
	require.Equal(t, "true", step.Commands[0].Command)
}

func TestLoadYAMLWithOpts_PreservesLocalDAGsFromMultiDocumentYAML(t *testing.T) {
	t.Parallel()

	dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-task

---
name: child-task
steps:
  - name: work
    run: echo "child"
`), spec.BuildOpts{Name: "parent-task"})
	require.NoError(t, err)

	require.NotNil(t, dag.LocalDAGs)
	require.Len(t, dag.LocalDAGs, 1)
	assert.Equal(t, "parent-task", dag.Name)

	childDAG, ok := dag.LocalDAGs["child-task"]
	require.True(t, ok)
	assert.Equal(t, "child-task", childDAG.Name)
	require.Len(t, childDAG.Steps, 1)
	assert.Equal(t, "work", childDAG.Steps[0].Name)
	assert.Equal(t, core.TypeChain, childDAG.Type)
}

func TestLoad_MultiDocumentFilePreservesDocumentProvenance(t *testing.T) {
	t.Parallel()

	dagFile := createTempYAMLFile(t, `
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-task

---
name: child-task
steps:
  - name: work
    run: echo "child"
`)

	dag, err := spec.Load(context.Background(), dagFile)
	require.NoError(t, err)
	require.NotNil(t, dag.LocalDAGs)

	childDAG, ok := dag.LocalDAGs["child-task"]
	require.True(t, ok)

	assert.Equal(t, dagFile, dag.Location)
	assert.Equal(t, dagFile, dag.SourceFile)
	assert.Contains(t, string(dag.YamlData), "action: dag.run")
	assert.Contains(t, string(dag.YamlData), "name: child-task")

	assert.Equal(t, dagFile, childDAG.Location)
	assert.Equal(t, dagFile, childDAG.SourceFile)
	assert.Contains(t, string(childDAG.YamlData), "name: child-task")
	assert.NotContains(t, string(childDAG.YamlData), "action: dag.run")
}

func TestLoad_MultiDocumentFileWithLeadingSeparatorPreservesMainDocumentProvenance(t *testing.T) {
	t.Parallel()

	dagFile := createTempYAMLFile(t, `---
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-task

---
name: child-task
steps:
  - name: work
    run: echo "child"
`)

	dag, err := spec.Load(context.Background(), dagFile)
	require.NoError(t, err)
	require.NotNil(t, dag.LocalDAGs)

	childDAG, ok := dag.LocalDAGs["child-task"]
	require.True(t, ok)

	assert.Equal(t, dagFile, dag.Location)
	assert.Equal(t, dagFile, dag.SourceFile)
	assert.Contains(t, string(dag.YamlData), "action: dag.run")
	assert.Contains(t, string(dag.YamlData), "name: child-task")

	assert.Equal(t, dagFile, childDAG.Location)
	assert.Equal(t, dagFile, childDAG.SourceFile)
	assert.Contains(t, string(childDAG.YamlData), "name: child-task")
	assert.NotContains(t, string(childDAG.YamlData), "action: dag.run")
}

func TestLoad_MultiDocumentFilePropagatesWorkspaceBaseConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	globalBase := filepath.Join(root, "base.yaml")
	require.NoError(t, os.WriteFile(globalBase, []byte(`
env:
  GLOBAL_ONLY: "global"
  SHARED: "global"
`), 0600))

	workspaceConfigDir := workspace.BaseConfigDir(root)
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceConfigDir, "ops"), 0750))
	require.NoError(t, os.WriteFile(workspace.BaseConfigPath(root, "ops"), []byte(`
env:
  WORKSPACE_ONLY: "ops"
  SHARED: "workspace"
`), 0600))

	dagFile := filepath.Join(root, "parent.yaml")
	require.NoError(t, os.WriteFile(dagFile, []byte(`
labels:
  - workspace=ops
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-task

---
name: child-task
steps:
  - name: work
    run: echo "child"
`), 0600))

	dag, err := spec.Load(context.Background(), dagFile,
		spec.WithBaseConfig(globalBase),
		spec.WithWorkspaceBaseConfigDir(workspaceConfigDir),
	)
	require.NoError(t, err)

	childDAG, ok := dag.LocalDAGs["child-task"]
	require.True(t, ok)

	assert.Contains(t, dag.Env, "WORKSPACE_ONLY=ops")
	assert.Contains(t, dag.Env, "SHARED=workspace")
	assert.Contains(t, childDAG.Env, "GLOBAL_ONLY=global")
	assert.Contains(t, childDAG.Env, "WORKSPACE_ONLY=ops")
	assert.Contains(t, childDAG.Env, "SHARED=workspace")
	assert.Contains(t, string(childDAG.BaseConfigData), "WORKSPACE_ONLY")
}

func TestLoadYAMLWithOpts_TypeInheritanceInMultiDocumentYAML(t *testing.T) {
	t.Parallel()

	t.Run("InheritsBaseTypeForParentAndSubDAGWhenOmitted", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, "type: graph\n")
		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-task
  - name: after-child
    run: echo parent

---
name: child-task
steps:
  - name: work
    run: echo child
  - name: finish
    run: echo done
`), spec.BuildOpts{Name: "parent-task", Base: base})
		require.NoError(t, err)

		assert.Equal(t, core.TypeGraph, dag.Type)
		require.Len(t, dag.Steps, 2)
		assert.Empty(t, dag.Steps[1].Depends)

		childDAG, ok := dag.LocalDAGs["child-task"]
		require.True(t, ok)
		assert.Equal(t, core.TypeGraph, childDAG.Type)
		require.Len(t, childDAG.Steps, 2)
		assert.Empty(t, childDAG.Steps[1].Depends)
	})

	t.Run("ExplicitSubDAGTypeOverridesInheritedBaseType", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, "type: graph\n")
		dag, err := spec.LoadYAMLWithOpts(context.Background(), []byte(`
steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-task

---
name: child-task
type: chain
steps:
  - name: work
    run: echo child
  - name: finish
    run: echo done
`), spec.BuildOpts{Name: "parent-task", Base: base})
		require.NoError(t, err)

		assert.Equal(t, core.TypeGraph, dag.Type)

		childDAG, ok := dag.LocalDAGs["child-task"]
		require.True(t, ok)
		assert.Equal(t, core.TypeChain, childDAG.Type)
		require.Len(t, childDAG.Steps, 2)
		assert.Equal(t, []string{"work"}, childDAG.Steps[1].Depends)
	})
}

// createTempYAMLFile creates a temporary YAML file with the given content
func createTempYAMLFile(t *testing.T, content string) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "*.yaml")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(tmpFile.Name()) })

	_, err = tmpFile.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	return tmpFile.Name()
}

func TestMultiDAGFile(t *testing.T) {
	t.Parallel()

	t.Run("LoadMultipleDAGs", func(t *testing.T) {
		t.Parallel()

		// Create a temporary multi-DAG YAML file
		multiDAGContent := `steps:
  - name: process
    action: dag.run
    with:
      dag: transform-data
  - name: archive
    action: dag.run
    with:
      dag: archive-results

---
name: transform-data
steps:
  - name: transform
    run: transform.py

---
name: archive-results
steps:
  - name: archive
    run: archive.sh
`
		// Create temporary file
		tmpFile := createTempYAMLFile(t, multiDAGContent)

		// Load the multi-DAG file
		dag, err := spec.Load(context.Background(), tmpFile)
		require.NoError(t, err)

		// Verify main DAG
		assert.Len(t, dag.Steps, 2)
		assert.Equal(t, "process", dag.Steps[0].Name)
		assert.Equal(t, "transform-data", dag.Steps[0].SubDAG.Name)
		assert.Equal(t, "archive", dag.Steps[1].Name)
		assert.Equal(t, "archive-results", dag.Steps[1].SubDAG.Name)

		// Verify sub DAGs
		require.NotNil(t, dag.LocalDAGs)
		assert.Len(t, dag.LocalDAGs, 2)

		// Check transform-data sub DAG
		_, exists := dag.LocalDAGs["transform-data"]
		require.True(t, exists)
		transformDAG := dag.LocalDAGs["transform-data"]
		assert.Equal(t, "transform-data", transformDAG.Name)
		assert.Len(t, transformDAG.Steps, 1)
		assert.Equal(t, "transform", transformDAG.Steps[0].Name)
		require.Len(t, transformDAG.Steps[0].Commands, 1)
		assert.Equal(t, "transform.py", transformDAG.Steps[0].Commands[0].Command)

		// Check archive-results sub DAG
		_, exists = dag.LocalDAGs["archive-results"]
		require.True(t, exists)
		archiveDAG := dag.LocalDAGs["archive-results"]
		assert.Equal(t, "archive-results", archiveDAG.Name)
		assert.Len(t, archiveDAG.Steps, 1)
		assert.Equal(t, "archive", archiveDAG.Steps[0].Name)
		require.Len(t, archiveDAG.Steps[0].Commands, 1)
		assert.Equal(t, "archive.sh", archiveDAG.Steps[0].Commands[0].Command)
	})

	t.Run("WithNamePreservesSubDAGNames", func(t *testing.T) {
		t.Parallel()

		// Multi-document YAML with parent + inline sub-DAG
		multiDAGContent := `steps:
  - name: call-child
    action: dag.run
    with:
      dag: child-task

---
name: child-task
steps:
  - name: do-work
    run: echo "child executed"
`
		tmpFile := createTempYAMLFile(t, multiDAGContent)

		// Load with WithName — simulates what the worker does when
		// receiving a task from the coordinator
		dag, err := spec.Load(context.Background(), tmpFile,
			spec.WithName("custom-parent-name"))
		require.NoError(t, err)

		// Main DAG should use the overridden name
		assert.Equal(t, "custom-parent-name", dag.Name)

		// Sub-DAG must keep its own name, not the overridden name
		require.NotNil(t, dag.LocalDAGs)
		assert.Len(t, dag.LocalDAGs, 1)

		_, wrongKey := dag.LocalDAGs["custom-parent-name"]
		assert.False(t, wrongKey, "sub-DAG should NOT be stored under the parent's overridden name")

		childDAG, exists := dag.LocalDAGs["child-task"]
		require.True(t, exists, "sub-DAG should be stored under its own name 'child-task'")
		assert.Equal(t, "child-task", childDAG.Name)
	})

	t.Run("MultiDAGWithBaseConfig", func(t *testing.T) {
		t.Parallel()

		// Create base config
		baseConfig := `env:
  - ENV: production
  - API_KEY: secret123
log_dir: /base/logs
smtp:
  host: smtp.example.com
  port: "587"
`
		baseFile := createTempYAMLFile(t, baseConfig)

		// Create multi-DAG file
		multiDAGContent := `env:
  - APP: myapp
steps:
  - name: process
    run: echo "main"

---
name: sub-dag
env:
  - SERVICE: worker
steps:
  - name: work
    run: echo "child"
`
		tmpFile := createTempYAMLFile(t, multiDAGContent)

		// Load with base config
		dag, err := spec.Load(context.Background(), tmpFile,
			spec.WithBaseConfig(baseFile))
		require.NoError(t, err)

		// Verify main DAG inherits base config
		assert.Equal(t, "/base/logs", dag.LogDir)
		assert.NotNil(t, dag.SMTP)
		assert.Equal(t, "smtp.example.com", dag.SMTP.Host)

		// Verify main DAG has merged env vars
		assert.Contains(t, dag.Env, "ENV=production")
		assert.Contains(t, dag.Env, "API_KEY=secret123")
		assert.Contains(t, dag.Env, "APP=myapp")

		// Verify sub DAG also inherits base config
		subDAG := dag.LocalDAGs["sub-dag"]
		require.NotNil(t, subDAG)
		assert.Equal(t, "/base/logs", subDAG.LogDir)
		assert.NotNil(t, subDAG.SMTP)
		assert.Equal(t, "smtp.example.com", subDAG.SMTP.Host)

		// Verify sub DAG has merged env vars
		assert.Contains(t, subDAG.Env, "ENV=production")
		assert.Contains(t, subDAG.Env, "API_KEY=secret123")
		assert.Contains(t, subDAG.Env, "SERVICE=worker")
	})

	t.Run("SingleDAGFileCompatibility", func(t *testing.T) {
		t.Parallel()

		// Single DAG file (no document separator)
		singleDAGContent := `steps:
  - name: step1
    run: echo "hello"
`
		tmpFile := createTempYAMLFile(t, singleDAGContent)

		// Load single DAG file
		dag, err := spec.Load(context.Background(), tmpFile)
		require.NoError(t, err)

		// Verify it loads correctly without sub DAGs
		assert.Len(t, dag.Steps, 1)
		assert.Nil(t, dag.LocalDAGs) // No sub DAGs for single DAG file
	})

	// MultiDAG error cases
	multiDAGErrorTests := []struct {
		name        string
		content     string
		errContains string
	}{
		{
			name: "DuplicateSubDAGNames",
			content: `steps:
  - name: step1
    run: echo "main"

---
name: duplicate-name
steps:
  - name: step1
    run: echo "first"

---
name: duplicate-name
steps:
  - name: step1
    run: echo "second"
`,
			errContains: "duplicate DAG name",
		},
		{
			name: "DuplicateMainAndSubDAGNames",
			content: `name: duplicate-name
steps:
  - name: step1
    run: echo "main"

---
name: duplicate-name
steps:
  - name: step1
    run: echo "child"
`,
			errContains: "duplicate DAG name",
		},
		{
			name: "SubDAGWithoutName",
			content: `steps:
  - name: step1
    run: echo "main"

---
steps:
  - name: step1
    run: echo "unnamed"
`,
			errContains: "must have a name",
		},
	}

	for _, tt := range multiDAGErrorTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tmpFile := createTempYAMLFile(t, tt.content)
			_, err := spec.Load(context.Background(), tmpFile)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errContains)
		})
	}

	t.Run("EmptyDocumentSeparator", func(t *testing.T) {
		t.Parallel()

		// TODO: The YAML parser has limitations with empty documents (---)
		// The behavior is inconsistent - sometimes it skips them, sometimes it errors.
		// For now, we test that it loads something, but the sub DAG after
		// the empty document may or may not be loaded.
		multiDAGContent := `steps:
  - name: step1
    run: echo "main"

---

---
name: child
steps:
  - name: step1
    run: echo "child"
`
		tmpFile := createTempYAMLFile(t, multiDAGContent)

		// The behavior with empty documents is unpredictable
		_, err := spec.Load(context.Background(), tmpFile)
		if err != nil {
			// If it errors, it should be a decode error
			assert.Contains(t, err.Error(), "failed to decode document")
		}
	})

	t.Run("ComplexMultiDAGWithParameters", func(t *testing.T) {
		t.Parallel()

		// Complex multi-DAG with parameters
		// Using type: graph for sub-DAG that needs explicit dependencies
		multiDAGContent := `params:
  - ENVIRONMENT: dev
schedule: "0 2 * * *"
steps:
  - name: extract
    action: dag.run
    with:
      dag: extract-module
      params: "SOURCE=customers TABLE=users"
  - name: transform
    action: dag.run
    with:
      dag: transform-module

---
name: extract-module
type: graph
params:
  - SOURCE: default_source
  - TABLE: default_table
steps:
  - name: validate
    run: test -f data/${SOURCE}/${TABLE}
  - name: extract
    run: extract.py --source=${SOURCE} --table=${TABLE}
    depends: validate

---
name: transform-module
steps:
  - name: transform
    run: transform.py
`
		tmpFile := createTempYAMLFile(t, multiDAGContent)

		dag, err := spec.Load(context.Background(), tmpFile)
		require.NoError(t, err)

		// Verify main DAG
		assert.Len(t, dag.Schedule, 1)
		assert.Equal(t, "0 2 * * *", dag.Schedule[0].Expression)
		assert.Contains(t, dag.Params, "ENVIRONMENT=dev")

		// Verify sub DAG references and parameters
		assert.Equal(t, "extract-module", dag.Steps[0].SubDAG.Name)
		assert.Equal(t, `SOURCE="customers" TABLE="users"`, dag.Steps[0].SubDAG.Params)
		assert.Equal(t, "transform-module", dag.Steps[1].SubDAG.Name)

		// Verify extract-module sub DAG
		extractDAG := dag.LocalDAGs["extract-module"]
		require.NotNil(t, extractDAG)
		assert.Contains(t, extractDAG.Params, "SOURCE=default_source")
		assert.Contains(t, extractDAG.Params, "TABLE=default_table")
		assert.Len(t, extractDAG.Steps, 2)

		// Verify dependencies in sub DAG
		assert.Contains(t, extractDAG.Steps[1].Depends, "validate")
	})

	t.Run("WorkerSelector", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `description: Test DAG with worker selector
worker_selector:
  gpu: "true"
  memory: "64G"
steps:
  - name: gpu-task
    run: echo "Running on GPU worker"
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)

		// Verify DAG loaded successfully
		assert.Equal(t, "Test DAG with worker selector", dag.Description)
		assert.Len(t, dag.Steps, 1)

		// Verify the step with GPU selector
		assert.NotNil(t, dag.WorkerSelector)
		assert.Equal(t, "true", dag.WorkerSelector["gpu"])
		assert.Equal(t, "64G", dag.WorkerSelector["memory"])
	})
}

func TestWithDefaultWorkingDir(t *testing.T) {
	t.Parallel()

	t.Run("DefaultUsedWhenNoFileContext", func(t *testing.T) {
		t.Parallel()

		// Create a temporary directory to use as default working dir
		tmpDir := t.TempDir()

		// Load from YAML data (no file context) with WithDefaultWorkingDir option
		dag, err := spec.LoadYAML(context.Background(), []byte(`steps:
  - name: test
    run: echo hello
`), spec.WithDefaultWorkingDir(tmpDir))
		require.NoError(t, err)

		// The WorkingDir should be set to the provided default value
		assert.Equal(t, tmpDir, dag.WorkingDir)
	})

	t.Run("DefaultTakesPrecedenceOverFileContext", func(t *testing.T) {
		t.Parallel()

		// Create a temporary directory for default
		defaultDir := t.TempDir()

		// Create a DAG file without explicit working_dir
		testDAG := createTempYAMLFile(t, `steps:
  - name: test
    run: echo hello
`)
		fileDir := filepath.Dir(testDAG)

		// First, verify that without the option, file's directory is used
		dagWithoutOption, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		assert.Equal(t, fileDir, dagWithoutOption.WorkingDir, "Without option, should use file's directory")

		// Now load with WithDefaultWorkingDir option
		// The default should take precedence over the file's directory
		dag, err := spec.Load(context.Background(), testDAG, spec.WithDefaultWorkingDir(defaultDir))
		require.NoError(t, err)

		// The WorkingDir should be the default, not the DAG file's directory
		assert.Equal(t, defaultDir, dag.WorkingDir)
		assert.NotEqual(t, fileDir, dag.WorkingDir, "Default should take precedence over file's directory")
	})

	t.Run("ExplicitWorkingDirTakesPrecedence", func(t *testing.T) {
		t.Parallel()

		// Create temporary directories
		explicitDir := t.TempDir()
		defaultDir := t.TempDir()

		// Create a DAG file with explicit working_dir
		testDAG := createTempYAMLFile(t, `working_dir: `+explicitDir+`
steps:
  - name: test
    run: echo hello
`)
		// Load with WithDefaultWorkingDir option (should be ignored since DAG has explicit working_dir)
		dag, err := spec.Load(context.Background(), testDAG, spec.WithDefaultWorkingDir(defaultDir))
		require.NoError(t, err)

		// The explicit working_dir from the DAG should take precedence
		assert.Equal(t, explicitDir, dag.WorkingDir)
	})
}

func TestLoadWithLoaderOptions(t *testing.T) {
	t.Parallel()

	t.Run("WithDAGsDir", func(t *testing.T) {
		t.Parallel()

		// Create a DAGs directory
		dagsDir := t.TempDir()

		// Create a sub-DAG file
		subDAGPath := filepath.Join(dagsDir, "subdag.yaml")
		require.NoError(t, os.WriteFile(subDAGPath, []byte(`
steps:
  - name: sub-step
    run: echo sub
`), 0644))

		// Create main DAG that calls the sub-DAG
		mainDAG := createTempYAMLFile(t, `
steps:
  - name: main-step
    run: echo main
`)
		// Load with WithDAGsDir
		dag, err := spec.Load(context.Background(), mainDAG, spec.WithDAGsDir(dagsDir))
		require.NoError(t, err)
		require.NotNil(t, dag)
	})

	t.Run("WithAllowBuildErrors", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
steps:
  - name: test
    run: echo test
    depends:
      - nonexistent-step
`)
		// Without AllowBuildErrors, this would fail
		_, err := spec.Load(context.Background(), testDAG)
		require.Error(t, err)

		// With AllowBuildErrors, it should succeed but capture errors
		dag, err := spec.Load(context.Background(), testDAG, spec.WithAllowBuildErrors())
		require.NoError(t, err)
		require.NotNil(t, dag)
		assert.NotEmpty(t, dag.BuildErrors)
	})

	t.Run("WithAllowBuildErrors_YAMLSyntaxError", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
this is not valid yaml: [unterminated
  - broken: {nope
`)
		// Without AllowBuildErrors, this should fail
		_, err := spec.Load(context.Background(), testDAG)
		require.Error(t, err)

		// With AllowBuildErrors, it should return a DAG with errors captured
		dag, err := spec.Load(context.Background(), testDAG, spec.WithAllowBuildErrors())
		require.NoError(t, err)
		require.NotNil(t, dag)
		assert.NotEmpty(t, dag.BuildErrors)
		assert.Equal(t, testDAG, dag.Location)
	})

	t.Run("WithAllowBuildErrors_EmptyFile", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, ``)

		// Without AllowBuildErrors, this should fail
		_, err := spec.Load(context.Background(), testDAG)
		require.Error(t, err)

		// With AllowBuildErrors, it should return a DAG with errors captured
		dag, err := spec.Load(context.Background(), testDAG, spec.WithAllowBuildErrors())
		require.NoError(t, err)
		require.NotNil(t, dag)
		assert.NotEmpty(t, dag.BuildErrors)
		assert.Equal(t, testDAG, dag.Location)
	})

	t.Run("SkipSchemaValidation", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
params:
  schema: "nonexistent-schema.json"
  values:
    foo: bar
steps:
  - name: test
    run: echo test
`)
		// Without SkipSchemaValidation, this would fail due to missing schema
		_, err := spec.Load(context.Background(), testDAG)
		require.Error(t, err)

		// With SkipSchemaValidation, it should succeed
		dag, err := spec.Load(context.Background(), testDAG, spec.SkipSchemaValidation())
		require.NoError(t, err)
		require.NotNil(t, dag)
	})

	t.Run("WithSkipBaseHandlers", func(t *testing.T) {
		t.Parallel()

		// Create base config with handlers
		baseDir := t.TempDir()
		baseConfig := filepath.Join(baseDir, "base.yaml")
		require.NoError(t, os.WriteFile(baseConfig, []byte(`
handler_on:
  success:
    run: echo base-success
`), 0644))

		testDAG := createTempYAMLFile(t, `
steps:
  - name: test
    run: echo test
`)
		// Load with base config but skip base handlers
		dag, err := spec.Load(context.Background(), testDAG,
			spec.WithBaseConfig(baseConfig),
			spec.WithSkipBaseHandlers())
		require.NoError(t, err)
		require.NotNil(t, dag)
		// The base success handler should not be present
		assert.Nil(t, dag.HandlerOn.Success)
	})

	t.Run("WithParamsAsList", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
params: KEY1 KEY2
steps:
  - name: test
    run: echo $KEY1 $KEY2
`)
		// Load with params as list
		dag, err := spec.Load(context.Background(), testDAG,
			spec.WithParams([]string{"KEY1=value1", "KEY2=value2"}))
		require.NoError(t, err)
		require.NotNil(t, dag)

		// Check that params were applied
		found := 0
		for _, p := range dag.Params {
			if p == "KEY1=value1" || p == "KEY2=value2" {
				found++
			}
		}
		assert.Equal(t, 2, found, "Both params should be applied")
	})
}

// TestLoadWithoutEval tests the WithoutEval loader option
// This test cannot be parallel because it uses t.Setenv
func TestLoadWithoutEval(t *testing.T) {
	t.Setenv("TEST_VAR", "should-not-expand")

	testDAG := createTempYAMLFile(t, `
env:
  - MY_VAR: "${TEST_VAR}"
steps:
  - name: test
    run: echo test
`)
	dag, err := spec.Load(context.Background(), testDAG, spec.WithoutEval())
	require.NoError(t, err)

	// With NoEval, environment variables should not be expanded
	assert.Contains(t, dag.Env, "MY_VAR=${TEST_VAR}")
}

func TestDefaults_LoadDAG(t *testing.T) {
	t.Parallel()

	t.Run("StepsInheritDefaults", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
defaults:
  retry_policy:
    limit: 3
    interval_sec: 5
  timeout_sec: 600
  mail_on_error: true

steps:
  - name: step1
    run: echo "hello"
  - name: step2
    run: echo "world"
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)

		for _, step := range dag.Steps {
			require.Equal(t, 3, step.RetryPolicy.Limit, "step %s should inherit retry limit", step.Name)
			require.Equal(t, 5*time.Second, step.RetryPolicy.Interval, "step %s should inherit retry interval", step.Name)
			require.Equal(t, 600*time.Second, step.Timeout, "step %s should inherit timeout", step.Name)
			require.True(t, step.MailOnError, "step %s should inherit mail_on_error", step.Name)
		}
	})

	t.Run("StepOverridesDefault", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
defaults:
  retry_policy:
    limit: 3
    interval_sec: 5
  timeout_sec: 600

steps:
  - name: step1
    run: echo "inherits"
  - name: step2
    run: echo "overrides"
    retry_policy:
      limit: 10
      interval_sec: 30
    timeout_sec: 300
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)

		// step1 inherits defaults
		require.Equal(t, 3, dag.Steps[0].RetryPolicy.Limit)
		require.Equal(t, 600*time.Second, dag.Steps[0].Timeout)

		// step2 overrides defaults
		require.Equal(t, 10, dag.Steps[1].RetryPolicy.Limit)
		require.Equal(t, 30*time.Second, dag.Steps[1].RetryPolicy.Interval)
		require.Equal(t, 300*time.Second, dag.Steps[1].Timeout)
	})

	t.Run("AdditiveEnvAndPreconditions", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
defaults:
  env:
    - DEFAULT_VAR: default_value
  preconditions:
    - condition: "true"

steps:
  - name: step1
    run: echo "only defaults"
  - name: step2
    run: echo "both"
    env:
      - STEP_VAR: step_value
    preconditions:
      - condition: "test -d /tmp"
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 2)

		// step1 gets only defaults
		require.Contains(t, dag.Steps[0].Env, "DEFAULT_VAR=default_value")
		require.Len(t, dag.Steps[0].Preconditions, 1)

		// step2 gets both defaults + step-level (additive)
		require.Contains(t, dag.Steps[1].Env, "DEFAULT_VAR=default_value")
		require.Contains(t, dag.Steps[1].Env, "STEP_VAR=step_value")
		require.Len(t, dag.Steps[1].Preconditions, 2)
	})

	t.Run("HandlersInheritDefaults", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
defaults:
  timeout_sec: 300

handler_on:
  failure:
    run: echo "failure handler"
  exit:
    run: echo "exit handler"
    timeout_sec: 60

steps:
  - name: step1
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)

		// failure handler inherits default timeout
		require.NotNil(t, dag.HandlerOn.Failure)
		require.Equal(t, 300*time.Second, dag.HandlerOn.Failure.Timeout)

		// exit handler overrides default timeout
		require.NotNil(t, dag.HandlerOn.Exit)
		require.Equal(t, 60*time.Second, dag.HandlerOn.Exit.Timeout)
	})

	t.Run("BaseConfigDefaults", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
defaults:
  timeout_sec: 300
`)
		child := createTempYAMLFile(t, `
defaults:
  timeout_sec: 600

steps:
  - name: step1
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)

		// DAG-level defaults should override base config defaults
		require.Equal(t, 600*time.Second, dag.Steps[0].Timeout)
	})

	t.Run("BaseConfigDefaultsAllowExplicitClears", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
defaults:
  timeout_sec: 300
  env:
    - BASE_ONLY: base-only
  preconditions:
    - condition: "test -f /base"
`)
		child := createTempYAMLFile(t, `
defaults:
  timeout_sec: 0
  env: []
  preconditions: []

steps:
  - name: step1
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		require.Zero(t, dag.Steps[0].Timeout)
		require.Empty(t, dag.Steps[0].Env)
		require.Empty(t, dag.Steps[0].Preconditions)
	})

	t.Run("BaseConfigDAGRetryPolicy", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
retry_policy:
  limit: 1
  interval_sec: 60
`)
		child := createTempYAMLFile(t, `
steps:
  - name: step1
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		require.NotNil(t, dag.RetryPolicy)
		require.Equal(t, 1, dag.RetryPolicy.Limit)
		require.Equal(t, 60*time.Second, dag.RetryPolicy.Interval)
	})

	t.Run("BaseConfigDAGRetryPolicyCanBeDisabledByChild", func(t *testing.T) {
		t.Parallel()

		base := createTempYAMLFile(t, `
retry_policy:
  limit: 3
  interval_sec: 30
  max_interval_sec: 300
`)
		child := createTempYAMLFile(t, `
retry_policy:
  limit: 0
steps:
  - name: step1
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), child, spec.WithBaseConfig(base))
		require.NoError(t, err)
		require.NotNil(t, dag.RetryPolicy)
		require.Equal(t, 0, dag.RetryPolicy.Limit)
		require.Equal(t, 60*time.Second, dag.RetryPolicy.Interval)
		require.Equal(t, time.Hour, dag.RetryPolicy.MaxInterval)
	})

	t.Run("DAGRetryPolicyNormalization", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name        string
			spec        string
			wantPolicy  *core.DAGRetryPolicy
			errContains string
		}{
			{
				name: "NumericStringsAndBackoffTrue",
				spec: `
name: retryable
retry_policy:
  limit: "3"
  interval_sec: "60"
  backoff: true
  max_interval_sec: "300"
steps:
  - run: echo hi
`,
				wantPolicy: &core.DAGRetryPolicy{
					Limit:          3,
					Interval:       60 * time.Second,
					IntervalSecStr: "60",
					Backoff:        2.0,
					MaxInterval:    300 * time.Second,
				},
			},
			{
				name: "LimitZeroDefaultsRetryIntervals",
				spec: `
name: retryable
retry_policy:
  limit: 0
steps:
  - run: echo hi
`,
				wantPolicy: &core.DAGRetryPolicy{
					Limit:       0,
					Interval:    60 * time.Second,
					Backoff:     0,
					MaxInterval: time.Hour,
				},
			},
			{
				name: "StringLimitZeroDefaultsRetryIntervals",
				spec: `
name: retryable
retry_policy:
  limit: "0"
steps:
  - run: echo hi
`,
				wantPolicy: &core.DAGRetryPolicy{
					Limit:       0,
					Interval:    60 * time.Second,
					Backoff:     0,
					MaxInterval: time.Hour,
				},
			},
			{
				name: "BackoffFalseUsesFixedInterval",
				spec: `
name: retryable
retry_policy:
  limit: 2
  interval_sec: "5"
  backoff: false
steps:
  - run: echo hi
`,
				wantPolicy: &core.DAGRetryPolicy{
					Limit:          2,
					Interval:       5 * time.Second,
					IntervalSecStr: "5",
					Backoff:        0,
					MaxInterval:    time.Hour,
				},
			},
			{
				name: "RejectsNonNumericStringLimit",
				spec: `
name: retryable
retry_policy:
  limit: three
steps:
  - run: echo hi
`,
				errContains: "retry_policy.limit",
			},
			{
				name: "RejectsNegativeLimit",
				spec: `
name: retryable
retry_policy:
  limit: -1
steps:
  - run: echo hi
`,
				errContains: "retry_policy.limit",
			},
			{
				name: "RejectsNegativeStringLimit",
				spec: `
name: retryable
retry_policy:
  limit: "-1"
steps:
  - run: echo hi
`,
				errContains: "retry_policy.limit",
			},
			{
				name: "RejectsNonNumericStringInterval",
				spec: `
name: retryable
retry_policy:
  limit: 3
  interval_sec: later
steps:
  - run: echo hi
`,
				errContains: "retry_policy.interval_sec",
			},
			{
				name: "RejectsZeroInterval",
				spec: `
name: retryable
retry_policy:
  limit: 1
  interval_sec: 0
steps:
  - run: echo hi
`,
				errContains: "retry_policy.interval_sec",
			},
			{
				name: "RejectsNegativeInterval",
				spec: `
name: retryable
retry_policy:
  limit: 1
  interval_sec: -1
steps:
  - run: echo hi
`,
				errContains: "retry_policy.interval_sec",
			},
			{
				name: "RejectsBackoffOnePointZero",
				spec: `
name: retryable
retry_policy:
  limit: 3
  interval_sec: 10
  backoff: 1.0
steps:
  - run: echo hi
`,
				errContains: "retry_policy.backoff",
			},
			{
				name: "RejectsZeroMaxInterval",
				spec: `
name: retryable
retry_policy:
  limit: 1
  max_interval_sec: 0
steps:
  - run: echo hi
`,
				errContains: "retry_policy.max_interval_sec",
			},
			{
				name: "RejectsNegativeMaxInterval",
				spec: `
name: retryable
retry_policy:
  limit: 1
  max_interval_sec: -1
steps:
  - run: echo hi
`,
				errContains: "retry_policy.max_interval_sec",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				dag, err := spec.LoadYAML(context.Background(), []byte(tt.spec))
				if tt.errContains != "" {
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.errContains)
					return
				}

				require.NoError(t, err)
				require.Equal(t, tt.wantPolicy, dag.RetryPolicy)
			})
		}
	})

	t.Run("UnknownKeyInDefaults", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
defaults:
  timeout_sec: 600
  unknown_field: value

steps:
  - name: step1
    run: echo "test"
`)
		_, err := spec.Load(context.Background(), testDAG)
		require.Error(t, err)
		require.Contains(t, err.Error(), "defaults")
	})

	t.Run("ContinueOnDefault", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
defaults:
  continue_on: failed

steps:
  - name: step1
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		require.True(t, dag.Steps[0].ContinueOn.Failure)
	})

	t.Run("SignalOnStopDefault", func(t *testing.T) {
		t.Parallel()

		testDAG := createTempYAMLFile(t, `
defaults:
  signal_on_stop: SIGTERM

steps:
  - name: step1
    run: echo "test"
`)
		dag, err := spec.Load(context.Background(), testDAG)
		require.NoError(t, err)
		require.Len(t, dag.Steps, 1)
		require.Equal(t, "SIGTERM", dag.Steps[0].SignalOnStop)
	})
}
