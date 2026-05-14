// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package filedag

// Example DAG templates for first-time users (Windows version)
var exampleDAGs = map[string]string{
	"example-01-basic-sequential.yaml": `# Basic Sequential Execution
# Steps execute one after another in order

description: Execute steps one after another
shell: powershell

steps:
  - run: Write-Output "Step 1 - Starting workflow"
  - run: Write-Output "Step 2 - Processing data"
  - run: Write-Output "Step 3 - Workflow complete"
`,

	"example-02-parallel-execution.yaml": `# Parallel Execution
# Run multiple tasks simultaneously

description: Execute multiple tasks in parallel
type: graph # Explicitly define dependency graph
shell: powershell

steps:
  - name: setup
    run: Write-Output "Setting up environment"

  # These steps run in parallel after setup
  - name: task-a
    run: |
      Write-Output "Task A starting"
      Start-Sleep -Seconds 2
      Write-Output "Task A complete"
    depends:
      - setup

  - name: task-b
    run: |
      Write-Output "Task B starting"
      Start-Sleep -Seconds 2
      Write-Output "Task B complete"
    depends:
      - setup

  - name: task-c
    run: |
      Write-Output "Task C starting"
      Start-Sleep -Seconds 2
      Write-Output "Task C complete"
    depends:
      - setup

  # Wait for all parallel tasks to complete
  - name: merge-results
    run: Write-Output "All parallel tasks completed"
    depends:
      - task-a
      - task-b
      - task-c
`,

	"example-03-scheduling.yaml": `# Scheduled Workflows
# Run workflows automatically on a schedule

description: Example of a scheduled workflow
shell: powershell
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
      Write-Output "Running scheduled task"
      Write-Output "Current time: $(Get-Date)"
  - run: Write-Output "Cleaning up old data"
`,

	"example-04-nested-workflows.yaml": `# Nested Workflows
# Call other workflows as sub-workflows

description: Example of nested workflows
shell: powershell

steps:
  - run: Write-Output "Preparing data for sub-workflows"
  - action: dag.run
    with:
      dag: sub-workflow
      params: "TASK_ID=123"
  - run: Write-Output "Main workflow completed"

---
# Sub-workflow definition
name: sub-workflow
description: Sub-workflow that gets called by main
shell: powershell
params:
  - TASK_ID: "000"

steps:
  - run: Write-Output "Sub-workflow executing with TASK_ID=$env:TASK_ID"
  - run: Write-Output "Sub-workflow step 2"
`,

	"example-05-container-workflow.yaml": `# Container-based Workflow
# Using a container for all steps

description: Run workflow steps in a Python container

container:
  image: python:3.13
  volumes:
    - C:/temp/data:/data

steps:
  # write data to a file
  - run: |
    python -c "with open('/data/output.txt', 'w') as f: f.write('Hello from Dagu!')"

  # read data from the file
  - run: |
    python -c "with open('/data/output.txt') as f: print(f.read())"
`,
}
