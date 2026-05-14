// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package filedag

// Example DAG templates for first-time users
var exampleDAGs = map[string]string{
	"example-01-basic-sequential.yaml": `# Basic Sequential Execution
# Steps execute one after another in order

description: Execute steps one after another

steps:
  - run: echo "Step 1 - Starting workflow"
  - run: echo "Step 2 - Processing data"
  - run: echo "Step 3 - Workflow complete"
`,

	"example-02-parallel-execution.yaml": `# Parallel Execution
# Run multiple tasks simultaneously

description: Execute multiple tasks in parallel
type: graph # Explicitly define dependency graph

steps:
  - name: setup
    run: echo "Setting up environment"

  # These steps run in parallel after setup
  - name: task-a
    run: |
      echo "Task A starting"
      sleep 2
      echo "Task A complete"
    depends:
      - setup

  - name: task-b
    run: |
      echo "Task B starting"
      sleep 2
      echo "Task B complete"
    depends:
      - setup

  - name: task-c
    run: |
      echo "Task C starting"
      sleep 2
      echo "Task C complete"
    depends:
      - setup

  # Wait for all parallel tasks to complete
  - name: merge-results
    run: echo "All parallel tasks completed"
    depends:
      - task-a
      - task-b
      - task-c
`,

	"example-03-scheduling.yaml": `# Scheduled Workflows
# Run workflows automatically on a schedule

description: Example of a scheduled workflow
# Uncomment to run daily at 2:00 AM
# schedule: "0 2 * * *"

# Schedule examples:
#   "0 * * * *"      - Every hour
#   "*/5 * * * *"    - Every 5 minutes
#   "0 9 * * 1-5"    - Weekdays at 9 AM
#   "0 0 1 * *"      - First day of each month

hist_retention_days: 7  # Keep 7 days of history (or use hist_retention_runs)

steps:
  - run: |
      #!env sh
      echo "Running scheduled task"
      echo "Current time: $(date)"
  - run: echo "Cleaning up old data"
`,

	"example-04-nested-workflows.yaml": `# Nested Workflows
# Call other workflows as sub-workflows

description: Example of nested workflows

steps:
  - run: echo "Preparing data for sub-workflows"
  - action: dag.run
    with:
      dag: sub-workflow
      params: "TASK_ID=123"
  - run: echo "Main workflow completed"

---
# Sub-workflow definition
name: sub-workflow
description: Sub-workflow that gets called by main
params:
  - TASK_ID: "000"

steps:
  - run: echo "Sub-workflow executing with TASK_ID=${TASK_ID}"
  - run: echo "Sub-workflow step 2"
`,

	"example-05-container-workflow.yaml": `# Container-based Workflow
# Using a container for all steps

description: Run workflow steps in a Python container

container:
  image: python:3.13
  volumes:
    - /tmp/data:/data

steps:
  # write data to a file
  - run: |
    python -c "with open('/data/output.txt', 'w') as f: f.write('Hello from Dagu!')"

  # read data from the file
  - run: |
    python -c "with open('/data/output.txt') as f: print(f.read())"
`,
}
