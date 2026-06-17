# Spec: Project

## Implementation Status

Not implemented. This spec describes target conformance behavior and must not be
treated as current product behavior.

## Scope

This spec defines project root detection, workflow discovery, and relative path handling for data-plane execution.

It does not define workflow YAML semantics, step execution, or output formats.

## Goal

Define what counts as a Dagu project and how project commands find workflows.

A project is a directory that contains `.dagu.json`. Commands run in project mode use that directory as `project_root`.

## Project file

`.dagu.json` must be a JSON object. An empty object is valid.

Fields defined by this spec:

| Field | Required | Meaning |
| --- | --- | --- |
| `workflows` | No | Project-relative directory that contains workflow files. Defaults to `workflows`. |
| `working_dir` | No | Default process working directory for workflow steps. |

Example:

```json
{
  "workflows": "workflows",
  "working_dir": "."
}
```

Default layout:

```text
project/
  .dagu.json
  workflows/
    deploy.yaml
    backup.yml
  scripts/
    deploy.sh
```

## Commands

Project commands:

```sh
dagu list
dagu validate [<workflow_target>]
dagu run <workflow_target>
```

Rules:

- The caller's current working directory is `project_root`.
- Dagu does not search parent directories for `.dagu.json`.
- `<workflow_target>` is a workflow target discovered from the configured workflow directory.
- `dagu list` prints discovered workflow targets.
- `dagu validate` without `<workflow_target>` validates the project file and every discovered workflow.
- `dagu validate <workflow_target>` validates one discovered workflow.
- `dagu run <workflow_target>` runs one discovered workflow.
- Listing and validation must not execute workflow steps.

Command output:

| Command | Condition | Exit code | Stdout | Stderr |
| --- | --- | --- | --- | --- |
| `dagu list` | Success | `0` | Discovered workflow targets, sorted, one per line. | Empty. |
| `dagu list` | Failure | Non-zero | Empty. | Project loading error. |
| `dagu validate` | Success | `0` | Empty. | Empty. |
| `dagu validate` | Failure | Non-zero | Empty. | Validation error. |

Validation failures must not print command usage text.

## Workflow discovery

Dagu resolves the workflow directory from `.dagu.json`.

Rules:

- If `workflows` is omitted, Dagu uses `project_root/workflows`.
- A relative `workflows` value is resolved from `project_root`.
- The resolved workflow directory must be inside `project_root`.
- The workflow directory must exist and must be a directory.
- This spec searches only direct files in the workflow directory.
- Subdirectories are not searched.
- Discovered files must end in `.yaml` or `.yml`.
- Workflow targets are paths relative to the workflow directory.
- A workflow target selects exactly the discovered file at that relative path inside the workflow directory.
- `dagu list` prints targets in lexicographic order.
- Dagu must not append `.yaml` or `.yml` to an extensionless target.
- If `<workflow_target>` does not match a discovered target, Dagu must fail. It must not try another filesystem path.

Workflow file contents are validated by the YAML schema spec and later field specs.

## Working directory

The process working directory is the directory from which step commands run.

Rules:

- The default process working directory is `project_root`.
- A relative `.dagu.json` `working_dir` is resolved from `project_root`.
- An absolute `.dagu.json` `working_dir` is used as written.
- A relative workflow root `working_dir` is resolved from the project default process working directory.
- An absolute workflow root `working_dir` is used as written.
- Step commands run from the resolved process working directory.
- Command path lookup is done by the shell or operating system from that directory.

Example:

```text
project/
  .dagu.json
  workflows/
    deploy.yaml
  scripts/
    deploy.sh
```

`workflows/deploy.yaml`:

```yaml
steps:
  - name: deploy
    run: ./scripts/deploy.sh
```

With the default project configuration, `./scripts/deploy.sh` resolves from:

```text
project/scripts/deploy.sh
```

## Outputs

Project loading and workflow discovery do not write workflow events, run logs, artifacts, or result files.

Step stdout, stderr, exit code, and runtime outputs belong to execution specs.

## Errors

Project loading must fail when:

- `.dagu.json` is missing from the caller's current working directory.
- `.dagu.json` exists but is not a file.
- `.dagu.json` is not valid JSON.
- `.dagu.json` contains an invalid field defined by this spec.
- The workflow directory is missing.
- The workflow directory path is not a directory.
- The workflow directory resolves outside `project_root`.
- No workflows are discovered.

Workflow selection or execution must fail when:

- `<workflow_target>` is not a discovered target.
- A discovered workflow fails YAML schema validation.
- A relative `working_dir` resolves outside `project_root`.
- The resolved `working_dir` does not exist.

A command that references a missing project file fails when the step executes, not during project loading.

## Examples

Configured workflow directory:

```text
project/
  .dagu.json
  pipelines/
    deploy.yaml
  workflows/
    backup.yaml
```

`.dagu.json`:

```json
{
  "workflows": "pipelines"
}
```

`dagu list` prints:

```text
deploy.yaml
```

Unknown targets do not fall back to arbitrary files:

```sh
dagu run ../other.yaml
```

If `../other.yaml` is not a discovered target, the command fails.
