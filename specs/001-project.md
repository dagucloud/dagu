# Spec: Project

Scope: project root, workflow discovery, and relative path resolution for data-plane execution.

## Objective

Define the filesystem shape and project-aware CLI behavior of one Dagu project.

## Inputs

A project is a directory that contains `.dagu.json`. The `.dagu.json` file marks the project root and may define project-level defaults.

**Default project layout:**

```text
project/
  .dagu.json
  workflows/
    deploy.yaml
    backup.yml
  scripts/
    deploy.sh
  files/
    config.json
```

**Project configuration:**

`.dagu.json` must contain a JSON object. An empty object is valid. These fields are defined by this spec:

| Field | Required | Value |
| --- | --- | --- |
| `workflows` | No. | Project-relative workflow definition directory. Defaults to `workflows`. |
| `working_dir` | No. | Project default process working directory. |

When `workflows` is omitted, Dagu discovers direct files with the `.yaml` or `.yml` extension under the project-root `workflows` directory.

**Example `.dagu.json`:**

```json
{
  "workflows": "workflows",
  "working_dir": "."
}
```

## Commands

Project workflow listing command:

```sh
dagu list [--project <project_root>]
```

Project validation command:

```sh
dagu validate [--project <project_root>] [<workflow>]
dagu validate --file <workflow_file>
```

Workflow execution command:

```sh
dagu run [--project <project_root>] <workflow>
dagu run --file <workflow_file>
```

**Command behavior:**

- `--project <project_root>` selects the project root.
- When `--project` is omitted, Dagu resolves `project_root` by searching from the caller's current working directory upward for `.dagu.json`.
- `<workflow>` is a project workflow target, not a literal filesystem path.
- `--file <workflow_file>` selects one literal workflow file and bypasses project workflow discovery.
- `--file` does not load `.dagu.json`.
- `--file` must not be combined with `--project`.
- `dagu list` prints discovered workflow targets.
- `dagu validate` without `<workflow>` validates the project configuration and all discovered workflows.
- `dagu validate <workflow>` validates one discovered workflow target.
- `dagu validate --file <workflow_file>` validates one literal workflow file and does not validate project structure.
- `dagu run <workflow>` runs one discovered workflow target.
- `dagu run --file <workflow_file>` runs one literal workflow file.
- Validation and listing commands must not execute steps.

**Command output:**

| Command | Condition | Exit Code | Stdout | Stderr |
| --- | --- | --- | --- | --- |
| `dagu list` | Success | `0` | Discovered workflow targets in sorted order, one per line. | Empty. |
| `dagu list` | Failure | Non-zero. | Empty. | Project loading error. |
| `dagu validate` | Success | `0` | Empty. | Empty. |
| `dagu validate` | Failure | Non-zero. | Empty. | Validation error. |

## Behavior

**Project resolution rules:**

- `project_root` is the directory containing `.dagu.json`.
- An explicit `--project <project_root>` value must point to a directory containing `.dagu.json`.
- Project-relative behavior must not depend on the caller's current working directory after `project_root` has been resolved.
- Workflow definition content is validated by the YAML schema spec.

**Workflow discovery rules:**

- A workflow target is a path relative to the configured workflow definition directory.
- Default workflow discovery includes only direct `.yaml` and `.yml` files under `<project_root>/workflows`.
- The configured `workflows` directory is resolved relative to `project_root`.
- The configured `workflows` directory must resolve inside `project_root`.
- Subdirectories under the configured `workflows` directory are not searched by this spec.
- Discovered workflow definitions must have the `.yaml` or `.yml` extension.
- Discovered workflow targets must be sorted lexicographically for command output.
- Dagu must not infer `.yaml` or `.yml` extensions from extensionless workflow targets.
- Dagu must not fall back from an unknown project workflow target to a literal filesystem path.
- A command that needs a literal workflow file must use `--file`.

**Working directory rules:**

- The default process working directory is `project_root`.
- When `.dagu.json` `working_dir` is relative, it is resolved from `project_root`.
- When `.dagu.json` `working_dir` is absolute, it is used as written.
- When workflow root `working_dir` is relative, it is resolved from the project default process working directory.
- When workflow root `working_dir` is absolute, it is used as written.
- Step commands run from the step's process working directory.
- A command path such as `./scripts/deploy.sh` is discovered by the operating system or shell relative to the step's process working directory.

**Literal file mode rules:**

- `dagu validate --file <workflow_file>` and `dagu run --file <workflow_file>` do not require a project.
- When `<workflow_file>` is relative, it is resolved from the caller's current working directory.
- The default process working directory is the caller's current working directory.
- When workflow root `working_dir` is relative, it is resolved from the default process working directory.
- When workflow root `working_dir` is absolute, it is used as written.

## Outputs

Project loading succeeds before step execution starts.

**Output rules:**

- Project loading by itself does not write workflow result events, run logs, or artifacts.
- Step stdout, stderr, exit code, and runtime outputs belong to execution specs.

## Errors

**Project loading errors:**

- A missing `.dagu.json` must fail project loading.
- A `.dagu.json` path that is not a file must fail project loading.
- Invalid `.dagu.json` syntax must fail project loading.
- Invalid `.dagu.json` fields defined by this spec must fail project loading.
- A missing workflow definition directory must fail project loading.
- A workflow definition directory path that is not a directory must fail project loading.
- A project with no discovered workflows must fail validation.
- An unknown `<workflow>` target must fail before execution.
- Combining `--file` and `--project` must fail before validation or execution.
- A discovered workflow definition that fails YAML schema validation must fail before execution.
- A relative `working_dir` that resolves outside `project_root` must fail before execution.
- A `working_dir` path that does not exist must fail before execution.
- A command that references a missing project file fails at step execution time.

## Examples

Project with default workflow directory and a local script:

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

Run the workflow by target:

```sh
dagu run deploy.yaml
```

`./scripts/deploy.sh` is discovered at:

```text
project/scripts/deploy.sh
```

Project with configured workflow directory:

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

Run a literal workflow file:

```sh
dagu run --file ./tmp/one-off.yaml
```

## Acceptance Criteria

- A black-box fixture resolves `project_root` from `.dagu.json`.
- A black-box fixture discovers direct `workflows/*.yaml` files by default.
- A black-box fixture discovers direct `workflows/*.yml` files by default.
- A black-box fixture does not discover workflow files outside the configured workflow definition directory.
- A black-box fixture verifies `dagu list` prints discovered workflow targets in sorted order.
- A black-box fixture verifies `dagu list --project <project_root>` lists the selected project.
- A black-box fixture verifies `dagu validate` validates the current project.
- A black-box fixture verifies `dagu validate --project <project_root>` validates the selected project.
- A black-box fixture verifies `dagu validate <workflow>` validates one discovered workflow target.
- A black-box fixture verifies `dagu validate --file <workflow_file>` validates a literal workflow file without loading a project.
- A black-box fixture verifies `dagu validate` does not execute steps.
- A black-box fixture rejects a project without `.dagu.json`.
- A black-box fixture rejects `.dagu.json` when it is not a file.
- A black-box fixture rejects invalid `.dagu.json` syntax.
- A black-box fixture rejects a missing workflow definition directory.
- A black-box fixture rejects a workflow definition directory path that is not a directory.
- A black-box fixture rejects an unknown workflow target without falling back to a literal file path.
- A black-box fixture verifies configured `workflows` directory controls discovery.
- A black-box fixture verifies `dagu run deploy.yaml` runs `project_root/workflows/deploy.yaml`.
- A black-box fixture verifies `dagu run --file <workflow_file>` runs a literal workflow file.
- A black-box fixture rejects combining `--file` and `--project`.
- A black-box fixture verifies `dagu run` executes from `project_root` by default.
- A black-box fixture verifies `dagu run --file <workflow_file>` executes from the caller's current working directory by default.
- A black-box fixture resolves `./scripts/deploy.sh` from `project_root`.
- A black-box fixture gives the same project-mode result when invoked from outside `project_root`.
