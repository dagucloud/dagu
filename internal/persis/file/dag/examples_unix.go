// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package dag

// Example DAG templates for first-time users
var exampleDAGs = map[string]string{
	"example-01-basic-sequential.yaml": `# Basic Sequential Execution
# Steps execute one after another in order

description: Execute steps one after another
type: graph

steps:
  - id: start
    run: echo "Step 1 - Starting workflow"

  - id: process
    run: echo "Step 2 - Processing data"
    depends: [start]

  - id: finish
    run: echo "Step 3 - Workflow complete"
    depends: [process]
`,

	"example-02-parallel-execution.yaml": `# Parallel Execution
# Run multiple tasks simultaneously

description: Execute multiple tasks in parallel
type: graph

steps:
  - id: setup
    run: echo "Setting up environment"

  # These steps run in parallel after setup
  - id: task_a
    run: |
      echo "Task A starting"
      sleep 1
      echo "Task A complete"
    depends: [setup]

  - id: task_b
    run: |
      echo "Task B starting"
      sleep 1
      echo "Task B complete"
    depends: [setup]

  - id: task_c
    run: |
      echo "Task C starting"
      sleep 1
      echo "Task C complete"
    depends: [setup]

  # Wait for all parallel tasks to complete
  - id: merge_results
    run: echo "All parallel tasks completed"
    depends: [task_a, task_b, task_c]
`,

	"example-03-scheduling.yaml": `# Scheduled Workflows
# Add a schedule to run workflows automatically

description: Example of a scheduled workflow
type: graph

# Uncomment to run daily at 2:00 AM
# schedule: "0 2 * * *"

# Schedule examples:
#   "0 * * * *"      - Every hour
#   "*/5 * * * *"    - Every 5 minutes
#   "0 9 * * 1-5"    - Weekdays at 9 AM
#   "0 0 1 * *"      - First day of each month

hist_retention_days: 7  # Keep 7 days of history (or use hist_retention_runs)

params:
  - name: environment
    type: string
    enum: [dev, staging, prod]
    default: staging
    description: Target environment for this run
  - name: batch_size
    type: integer
    minimum: 1
    maximum: 1000
    default: 100
    description: Number of records to process

env:
  - LOG_LEVEL: info

steps:
  - id: plan
    run: echo "Planning ${params.batch_size} records for ${params.environment}"

  - id: run_batch
    run: echo "Running batch with LOG_LEVEL=${env.LOG_LEVEL}"
    depends: [plan]

  - id: cleanup
    run: echo "Scheduled workflow complete"
    depends: [run_batch]
`,

	"example-04-nested-workflows.yaml": `# Nested Workflows
# Call other workflows as sub-workflows

description: Example of nested workflows
type: graph

steps:
  - id: prepare
    run: echo "Preparing data for child workflow"

  - id: run_child
    action: dag.run
    with:
      dag: child-workflow
      params:
        task_id: "123"
    depends: [prepare]

  - id: done
    run: echo "Main workflow completed"
    depends: [run_child]

---
# Child workflow definition
name: child-workflow
description: Child workflow called by the main workflow
type: graph
params:
  - name: task_id
    type: string
    default: "000"
    description: Task ID passed by the parent DAG

steps:
  - id: child_start
    run: echo "Child workflow processing ${params.task_id}"

  - id: child_finish
    run: echo "Child workflow complete"
    depends: [child_start]
`,

	"example-05-template-and-file.yaml": `# Template and File Actions
# Render a small report and read it back

description: Render and read a file without external tools
type: graph
artifacts:
  enabled: true

steps:
  - id: render_report
    action: template.render
    with:
      template: |
        # First Launch Report

        status={{ .status }}
        source={{ .source }}
      output: ${DAG_RUN_ARTIFACTS_DIR}/first-launch-report.md
      data:
        status: ok
        source: Dagu

  - id: read_report
    action: file.read
    with:
      path: ${DAG_RUN_ARTIFACTS_DIR}/first-launch-report.md
    depends: [render_report]
`,
}
