// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package intg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmd"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTemplateExecutor covers the end-to-end behavior of the template executor,
// including literal rendering, file output, and data interpolation edge cases.
func TestTemplateExecutor(t *testing.T) {
	t.Parallel()

	t.Run("StdoutOnly", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        {{ .greeting }}, world!
      data:
        greeting: hello
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": test.Contains("hello, world!"),
		})
	})

	t.Run("FileOnly", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		outFile := filepath.Join(tmpDir, "report.md")
		outFileForYAML := filepath.ToSlash(outFile)

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        # {{ .title }}
      output: "`+outFileForYAML+`"
      data:
        title: Test Report
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		content, err := os.ReadFile(outFile)
		require.NoError(t, err)
		assert.Contains(t, string(content), "# Test Report")
	})

	t.Run("ArtifactOutputAutoEnablesArtifacts", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.CreateDAGFile(t, "template-artifact-auto-enable.yaml", `
name: template-artifact-auto-enable
steps:
  - name: render
    action: template.render
    with:
      output: "${DAG_RUN_ARTIFACTS_DIR}/greeting.txt"
      data:
        greeting: hello
      template: |
        {{ .greeting }}, world!
`)

		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{
				"start",
				"--run-id", runID,
				dagFile,
			},
			ExpectedOut: []string{"DAG run finished"},
		})

		status, _ := readAttemptStatusAndOutputs(t, th, "template-artifact-auto-enable", runID)
		require.Equal(t, core.Succeeded, status.Status)
		require.NotEmpty(t, status.ArchiveDir)

		content, err := os.ReadFile(filepath.Join(status.ArchiveDir, "greeting.txt"))
		require.NoError(t, err)
		assert.Contains(t, string(content), "hello, world!")
	})

	t.Run("RelativeOutputPath", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		tmpDirForYAML := filepath.ToSlash(tmpDir)

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    working_dir: "`+tmpDirForYAML+`"
    with:
      output: "subdir/output.txt"
      data:
        msg: relative
      template: "{{ .msg }}"
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)

		content, err := os.ReadFile(filepath.Join(tmpDir, "subdir", "output.txt"))
		require.NoError(t, err)
		assert.Equal(t, "relative", string(content))
	})

	t.Run("DataFromPriorStep", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `
type: graph
steps:
  - id: producer
    run: 'echo -n "Alice"'
    output: NAME

  - id: render
    depends:
      - producer
    action: template.render
    with:
      template: "Hello, {{ .name }}!"
      data:
        name: ${NAME}
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "Hello, Alice!",
		})
	})

	t.Run("LiteralDollarPreservation", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        export FOO=${BAR}
        echo "{{ .name }}"
        value=`+"`command`"+`
      data:
        name: test
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": []test.Contains{
				test.Contains("${BAR}"),
				test.Contains("`command`"),
			},
		})
	})

	t.Run("CodeFenceDataPreserved", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        {{ .issue_text }}
      data:
        issue_text: |
          `+"```yaml"+`
          env:
            TEST_FILE: ~/dagu-test.txt

          steps:
            - run: touch $TEST_FILE
          `+"```"+`
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": []test.Contains{
				test.Contains("```yaml"),
				test.Contains("\n```"),
				test.Contains("touch $TEST_FILE"),
			},
		})
	})

	t.Run("MissingKeyError", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: "{{ .undefined_key }}"
      data:
        name: test
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunCheckErr(t, "execution error")

		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("ComplexTemplate", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        # {{ .title }}
        {{ $items := .domains | split "," }}
        Total: {{ $items | count }}
        {{ range $i, $d := $items }}
        {{ $i | add 1 }}. {{ $d | upper }}
        {{ end }}
      data:
        title: Domain Report
        domains: "example.com,test.org,demo.net"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": []test.Contains{
				test.Contains("# Domain Report"),
				test.Contains("Total: 3"),
				test.Contains("EXAMPLE.COM"),
				test.Contains("TEST.ORG"),
				test.Contains("DEMO.NET"),
			},
		})
	})

	t.Run("ConditionalAndEmpty", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        {{ if .items | empty }}No items found.{{ else }}Has items.{{ end }}
      data:
        items: ""
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": test.Contains("No items found."),
		})
	})

	t.Run("OmittedOptionalParamResolvesToEmptyString", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dagFile := th.CreateDAGFile(t, "template-optional-param.yaml", `
name: template-optional-param
type: graph
params:
  - name: name
    type: string
    required: true
  - name: age
    type: integer
    required: true
  - name: favorite_color
    type: string
steps:
  - id: render
    action: template.render
    with:
      template: |
        Hello, {{ .name }}!
        You are {{ .age }} years old.
        {{- if .favorite_color }}
        Your favorite color is {{ .favorite_color }}.
        {{- end }}
      data:
        name: ${name}
        age: ${age}
        favorite_color: ${favorite_color}
    output: RESULT
`)

		runID := uuid.Must(uuid.NewV7()).String()
		th.RunCommand(t, cmd.Start(), test.CmdTest{
			Args: []string{
				"start",
				"--run-id", runID,
				"--params", "name=tom age=21",
				dagFile,
			},
			ExpectedOut: []string{"DAG run finished"},
		})

		status, outputs := readAttemptStatusAndOutputs(t, th, "template-optional-param", runID)
		require.Equal(t, core.Succeeded, status.Status)
		require.Contains(t, outputs.Outputs, "result")
		assert.Contains(t, outputs.Outputs["result"], "Hello, tom!")
		assert.Contains(t, outputs.Outputs["result"], "You are 21 years old.")
		assert.NotContains(t, outputs.Outputs["result"], "${favorite_color}")
		assert.NotContains(t, outputs.Outputs["result"], "Your favorite color is")
	})

	t.Run("DefaultFunction", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: '{{ .name | default "Anonymous" }} ({{ .title | default "User" }})'
      data:
        name: ""
        title: Admin
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "Anonymous (Admin)",
		})
	})

	t.Run("SlimSprigStringFunctions", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: '{{ .name | trim | lower | replace " " "-" }}'
      data:
        name: "  My Service  "
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "my-service",
		})
	})

	t.Run("SlimSprigSafeMapAccess", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        name={{ get .app "name" | default "unknown" }}
        owner={{ get .app "owner" | default "unknown" }}
      data:
        app:
          name: MyApp
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": []test.Contains{
				test.Contains("name=MyApp"),
				test.Contains("owner=unknown"),
			},
		})
	})

	t.Run("SlimSprigListOperations", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: '{{ .domains | uniq | sortAlpha | join "," }}'
      data:
        domains:
          - api.example.com
          - api.example.com
          - app.example.com
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": "api.example.com,app.example.com",
		})
	})

	t.Run("SlimSprigFullExample", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render-config
    action: template.render
    with:
      template: |
        app={{ .app.name | lower | replace " " "-" }}
        owner={{ get .app "owner" | default "unknown" }}
        domains={{ get .app "domains" | default (list "localhost") | uniq | sortAlpha | join "," }}
      data:
        app:
          name: My Service
          domains:
            - api.example.com
            - api.example.com
            - app.example.com
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": []test.Contains{
				test.Contains("app=my-service"),
				test.Contains("owner=unknown"),
				test.Contains("domains=api.example.com,app.example.com"),
			},
		})
	})

	t.Run("SlimSprigBlockedFunctions", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: '{{ env "HOME" }}'
`)
		agent := dag.Agent()
		agent.RunCheckErr(t, "error")

		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("SlimSprigMissingKeyBoundary", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      data:
        app:
          name: test
      template: '{{ .nonexistent }}'
`)
		agent := dag.Agent()
		agent.RunCheckErr(t, "execution error")

		dag.AssertLatestStatus(t, core.Failed)
	})

	t.Run("SlimSprigOverlapBehavior", func(t *testing.T) {
		t.Parallel()

		th := test.Setup(t)
		dag := th.DAG(t, `steps:
  - name: render
    action: template.render
    with:
      template: |
        items={{ .csv | split "," | join ";" }}
        sum={{ 5 | add 3 }}
      data:
        csv: "a,b,c"
    output: RESULT
`)
		agent := dag.Agent()
		agent.RunSuccess(t)

		dag.AssertLatestStatus(t, core.Succeeded)
		dag.AssertOutputs(t, map[string]any{
			"RESULT": []test.Contains{
				test.Contains("items=a;b;c"),
				test.Contains("sum=8"),
			},
		})
	})
}
