// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type exampleEntry struct {
	ID          int
	Name        string
	Description string
	Content     string
}

var examples = []exampleEntry{
	{
		ID:          1,
		Name:        "parallel-steps",
		Description: "Run steps in parallel using depends",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
steps:
  - name: setup
    run: echo "preparing data"
  - name: task-a
    run: echo "processing batch A"
    depends: [setup]
  - name: task-b
    run: echo "processing batch B"
    depends: [setup]
  - name: task-c
    run: echo "processing batch C"
    depends: [setup]
  - name: aggregate
    run: echo "all tasks finished"
    depends: [task-a, task-b, task-c]
`,
	},
	{
		ID:          2,
		Name:        "output-passing",
		Description: "Use publish-only object-form output between steps",
		Content: `type: graph
steps:
  - id: publish_version
    output:
      version: "2.5.0"
  - id: publish_metadata
    output:
      build: abc123
      env: staging
  - id: publish_release
    output:
      version_label: "v${publish_version.output.version}"
      target_env: "${publish_metadata.output.env}"
    depends: [publish_version, publish_metadata]
  - id: deploy
    run: echo "deploying ${publish_release.output.version_label} build ${publish_metadata.output.build} to ${publish_release.output.target_env}"
    depends: [publish_release]
`,
	},
	{
		ID:          3,
		Name:        "schedule-params-env",
		Description: "Schedule a DAG with params and env vars",
		Content: `type: graph
schedule: "0 2 * * *"
catchup_window: "12h"
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
params:
  - name: ENV
    type: string
    enum: [DEV, STG, PROD]
    description: Target environment for the scheduled batch run
    required: true
    default: STG
  - name: BATCH_SIZE
    type: integer
    minimum: 1
    maximum: 1000
    description: Number of records processed per batch
    required: true
    default: 100
env:
  - LOG_LEVEL: "info"
  - TIMESTAMP: "` + "`date +%Y%m%d`" + `"
steps:
  - name: extract
    run: echo "extracting ${BATCH_SIZE} records in ${ENV}"
  - name: transform
    run: echo "transforming with LOG_LEVEL=${LOG_LEVEL}"
    depends: [extract]
  - name: load
    run: echo "loading batch from ${TIMESTAMP}"
    depends: [transform]
`,
	},
	{
		ID:          4,
		Name:        "defaults-and-retry",
		Description: "Set step defaults with retry and continue_on",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 3
    interval_sec: 5
  continue_on: failed
steps:
  - name: fetch-data
    run: "curl -sf https://httpbin.org/status/200 || exit 1"
  - name: process
    run: echo "processing data"
    depends: [fetch-data]
  - name: cleanup
    run: echo "done"
    retry_policy:
      limit: 1
      interval_sec: 1
    depends: [process]
`,
	},
	{
		ID:          5,
		Name:        "preconditions",
		Description: "Guard steps with preconditions",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
params:
  - name: ENV
    type: string
    enum: [DEV, STG, PROD]
    description: Deployment environment; only PROD satisfies the gate
    required: true
    default: STG
steps:
  - name: check-env
    run: echo "verifying environment"
  - name: deploy
    run: echo "deploying application"
    preconditions:
      - condition: "${ENV}"
        expected: "PROD"
    depends: [check-env]
  - name: notify
    run: echo "deployment complete"
    depends: [deploy]
`,
	},
	{
		ID:          6,
		Name:        "lifecycle-hooks",
		Description: "Use handler_on for init, success, failure, exit",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
handler_on:
  init:
    run: echo "workflow starting"
  success:
    run: echo "all steps succeeded"
  failure:
    run: echo "a step failed"
  exit:
    run: echo "cleanup complete"
steps:
  - name: step-1
    run: echo "running step 1"
  - name: step-2
    run: echo "running step 2"
    depends: [step-1]
`,
	},
	{
		ID:          7,
		Name:        "http-requests",
		Description: "Make HTTP requests and parse JSON response fields",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
steps:
  - id: get_todo
    action: http.request
    with:
      method: GET
      url: https://jsonplaceholder.typicode.com/todos/1
    output:
      # decode + select act as a lightweight contract check.
      # malformed JSON or a missing selected field fails the step,
      # so the normal retry_policy can retry it.
      title:
        from: stdout
        decode: json
        select: .title
      completed:
        from: stdout
        decode: json
        select: .completed
  - id: show_result
    run: 'echo "Todo: ${get_todo.output.title} (completed=${get_todo.output.completed})"'
    depends: [get_todo]
`,
	},
	{
		ID:          8,
		Name:        "docker-container",
		Description: "Run steps inside a Docker container",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
container:
  image: python:3.13-slim
  volumes:
    - /tmp/dagu-example:/work
steps:
  - name: write-data
    run: >-
      python -c "with open('/work/data.txt', 'w') as f: f.write('Hello from Dagu!')"
  - name: process
    run: >-
      python -c "with open('/work/data.txt') as f: print(f.read().upper())"
    depends: [write-data]
`,
	},
	{
		ID:          9,
		Name:        "sub-dag",
		Description: "Call another DAG as a sub-workflow",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
steps:
  - name: prepare
    run: echo "starting main workflow"
  - name: run-etl
    action: dag.run
    with:
      dag: etl-job
      params:
        SOURCE: /data/input.csv
        TARGET: /data/output.csv
    depends: [prepare]
  - name: done
    run: echo "pipeline complete"
    depends: [run-etl]
---
name: etl-job
params:
  - name: SOURCE
    type: string
    description: Input dataset or file path received from the parent DAG
    required: true
    default: /data/default-input.csv
  - name: TARGET
    type: string
    description: Output dataset or file path produced by the sub-DAG
    required: true
    default: /data/default-output.csv
type: graph
steps:
  - name: extract
    run: echo "extracting from ${SOURCE}"
  - name: load
    run: echo "loading into ${TARGET}"
    depends: [extract]
`,
	},
	{
		ID:          10,
		Name:        "conditional-routing",
		Description: "Route execution based on step output",
		Content: `type: graph
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
steps:
  - id: check_status
    output:
      status: success
  - id: route
    action: router.route
    with:
      value: ${check_status.output.status}
      routes:
        success: [on_success]
        "re:.*": [on_failure]
    depends: [check_status]
  - id: on_success
    run: echo "status was success"
  - id: on_failure
    run: echo "status was something else"
`,
	},
	{
		ID:          11,
		Name:        "approval-gate",
		Description: "Draft release notes with an agent, push back with rewind_to, then deploy",
		Content: `type: graph
artifacts:
  enabled: true
steps:
  - id: build
    output:
      version: "v1.2.3"
  - id: draft_release_notes
    depends: [build]
    action: agent.run
    with:
      task: |
        Draft concise release notes for version ${build.output.version}.

        Current draft path: ${DAG_RUN_ARTIFACTS_DIR}/release-notes.md

        Reviewer feedback from the latest push-back: ${FEEDBACK}

        Return Markdown with a summary and deployment notes.
        If FEEDBACK is empty, produce the first draft.
        If FEEDBACK is set, read the existing draft from the path above and revise it to address the feedback.
    stdout: ${DAG_RUN_ARTIFACTS_DIR}/release-notes.md
    approval:
      prompt: "Review the release-notes.md artifact. Push back with FEEDBACK to regenerate it, or approve to continue to deploy."
      input: [FEEDBACK]
      rewind_to: draft_release_notes
  - id: deploy
    depends: [draft_release_notes]
    run: echo "deploying ${build.output.version} with reviewed release notes"
`,
	},
	{
		ID:          12,
		Name:        "agent-step",
		Description: "Build the agent prompt with a template and write a report artifact",
		Content: `type: graph
artifacts:
  enabled: true
defaults:
  retry_policy:
    limit: 2
    interval_sec: 5
steps:
  - id: gather_logs
    run: 'echo "error: connection timeout at 10:23 AM"'
    # output captures stdout into a variable, but the default max_output_size
    # is 1048576 bytes (1 MiB). If logs can exceed that, write them to a
    # temporary file instead and pass the file path to later steps.
    output: ERROR_LOG
  - id: build_prompt
    action: template.render
    with:
      template: |
        Analyze this incident log and suggest a fix:

        {{ .error_log }}
      data:
        error_log: ${ERROR_LOG}
    output: ANALYSIS_PROMPT
    depends: [gather_logs]
  - id: analyze
    action: agent.run
    with:
      task: ${ANALYSIS_PROMPT}
      max_iterations: 10
    output: ANALYSIS
    depends: [build_prompt]
  - id: report
    action: template.render
    with:
      template: |
        # Incident Report

        ## Error Log

        {{ .error_log }}

        ## Analysis

        {{ .analysis }}
      output: ${DAG_RUN_ARTIFACTS_DIR}/report.md
      data:
        error_log: ${ERROR_LOG}
        analysis: ${ANALYSIS}
    depends: [analyze]
`,
	},
	{
		ID:          13,
		Name:        "custom-action",
		Description: "Define a typed reusable action with actions and with",
		Content: `type: graph
actions:
  release.announce:
    description: Print a reusable release announcement
    input_schema:
      type: object
      additionalProperties: false
      required: [channel, version]
      properties:
        channel:
          type: string
          enum: [changelog, email, slack]
        version:
          type: string
        summary:
          type: string
          default: Ready for rollout
    template:
      run: echo {{ json .input.channel }} release {{ json .input.version }} - {{ json .input.summary }}
steps:
  - id: build
    output:
      version: "v1.2.3"
  - id: announce_changelog
    action: release.announce
    with:
      channel: changelog
      version: ${build.output.version}
    depends: [build]
  - id: announce_email
    action: release.announce
    with:
      channel: email
      version: ${build.output.version}
      summary: Sent to subscribers
    depends: [build]
`,
	},
	{
		ID:          14,
		Name:        "template-step",
		Description: "Render a deployment config artifact with structured data",
		Content: `type: graph
artifacts:
  enabled: true
params:
  - name: ENV
    type: string
    enum: [DEV, STG, PROD]
    description: Target environment for the rendered config
    required: true
    default: STG
steps:
  - id: build
    output:
      version: "v1.2.3"
  - id: render_config
    action: template.render
    with:
      template: |
        APP_ENV={{ .env }}
        APP_VERSION={{ .version }}
        FEATURE_FLAG=true
      output: ${DAG_RUN_ARTIFACTS_DIR}/deploy.env
      data:
        env: ${ENV}
        version: ${build.output.version}
    depends: [build]
  - id: preview
    run: cat ${DAG_RUN_ARTIFACTS_DIR}/deploy.env
    depends: [render_config]
`,
	},
	{
		ID:          15,
		Name:        "harness-step",
		Description: "Build a harness prompt with template and write the result as an artifact",
		Content: `type: graph
artifacts:
  enabled: true
harness:
  # DAG-level defaults for harness steps. provider may be built-in or from harnesses:.
  provider: claude
  model: sonnet
  bare: true
steps:
  - id: gather_issue
    output:
      issue: "scheduler retries the same task after it already succeeded"
  - id: build_prompt
    action: template.render
    with:
      template: |
        Review this workflow issue and suggest a fix:

        {{ .issue }}
      data:
        issue: ${gather_issue.output.issue}
    output: HARNESS_PROMPT
    depends: [gather_issue]
  - id: analyze
    action: harness.run
    with:
      prompt: ${HARNESS_PROMPT}
      effort: high
    output: ANALYSIS
    depends: [build_prompt]
  - id: report
    action: template.render
    with:
      template: |
        # Harness Review

        ## Issue

        {{ .issue }}

        ## Suggested Fix

        {{ .analysis }}
      output: ${DAG_RUN_ARTIFACTS_DIR}/harness-report.md
      data:
        issue: ${gather_issue.output.issue}
        analysis: ${ANALYSIS}
    depends: [analyze]
`,
	},
	{
		ID:          16,
		Name:        "named-harnesses",
		Description: "Define a named harness under harnesses and call it from a step",
		Content: `type: graph
artifacts:
  enabled: true
harnesses:
  # Named custom harness adapters for CLIs that are not built in.
  gemini:
    binary: gemini
    prompt_mode: flag
    prompt_flag: --prompt
    option_flags:
      model: --model
steps:
  - id: gather_task
    output:
      task: "Summarize the deployment checklist for the next engineer"
  - id: build_prompt
    action: template.render
    with:
      template: |
        {{ .task }}

        Return a short handoff note.
      data:
        task: ${gather_task.output.task}
    output: PROMPT
    depends: [gather_task]
  - id: summarize
    action: harness.run
    with:
      prompt: ${PROMPT}
      provider: gemini
      model: gemini-2.5-pro
    output: SUMMARY
    depends: [build_prompt]
  - id: save_summary
    action: template.render
    with:
      template: |
        {{ .summary }}
      output: ${DAG_RUN_ARTIFACTS_DIR}/handoff.md
      data:
        summary: ${SUMMARY}
    depends: [summarize]
`,
	},
}

// ExampleCount returns the number of available examples.
func ExampleCount() int { return len(examples) }

// Example creates the 'example' CLI command that displays example DAG definitions.
func Example() *cobra.Command {
	return &cobra.Command{
		Use:   "example [id]",
		Short: "Show example DAG definitions",
		Long: `Display example DAG definitions to help you get started.

Run without arguments to list all available examples.
Use a numeric ID to show a specific example.`,
		Example: `  dagu example      List all available examples
  dagu example 1    Show the parallel-steps example
  dagu example 7    Show the http-requests example`,
		ValidArgs: func() []string {
			args := make([]string, len(examples))
			for i, e := range examples {
				args[i] = strconv.Itoa(e.ID)
			}
			return args
		}(),
		Args: cobra.MaximumNArgs(1),
		RunE: runExample,
	}
}

func runExample(cmd *cobra.Command, args []string) error {
	w := cmd.OutOrStdout()

	if len(args) == 0 {
		return listExamples(cmd)
	}

	if args[0] == "help" {
		return cmd.Help()
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid example ID %q: must be a number between 1 and %d", args[0], len(examples))
	}

	if id < 1 || id > len(examples) {
		return fmt.Errorf("invalid example ID %q: must be between 1 and %d", args[0], len(examples))
	}

	e := examples[id-1]
	_, _ = fmt.Fprintf(w, "# Example %d: %s\n", e.ID, titleCase(e.Name))
	_, _ = fmt.Fprintf(w, "# %s\n\n", e.Description)
	_, _ = fmt.Fprint(w, e.Content)

	return nil
}

func listExamples(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()

	_, _ = fmt.Fprintln(w, "Available DAG examples:")
	_, _ = fmt.Fprintln(w)

	for _, e := range examples {
		_, _ = fmt.Fprintf(w, "  %-4d %-24s %s\n", e.ID, e.Name, e.Description)
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Usage: dagu example <id>")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, `Tip: Use "dagu schema dag" to explore all DAG fields and options.`)

	return nil
}

func titleCase(s string) string {
	words := strings.Split(s, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
