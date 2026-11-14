# Spec: Project

## Status

Not implemented. This spec describes target conformance behavior and must not be
treated as current product behavior.

## Scope

This spec defines how the CLI finds a project, discovers its workflows,
resolves workflow targets and working directories, and where project-mode runs
keep their state. It also defines how a project relates to a server project of
[RFC 003](../rfcs/003-projects.md).

It does not define workflow YAML semantics, step execution, or output formats.

## Goal

Let a repository keep its workflows next to its code, and run them with no
server and no setup beyond `dagu init`.

A project is a directory that contains `.dagu.yaml`. That directory is
`project_root`.

## Project file

`.dagu.yaml` must be a YAML mapping. An empty file is valid.

Fields defined by this spec:

| Field | Required | Meaning |
| --- | --- | --- |
| `workflows` | No | Project-relative directory that contains workflow files. Defaults to `workflows`. |
| `working_dir` | No | Project-relative default working directory for steps. Defaults to `.`. |
| `project` | No | Name of the server project this checkout belongs to. See [Server projects](#server-projects). |

Rules:

- `workflows` and `working_dir` must be relative paths that resolve inside
  `project_root`. An absolute path names a place on one machine and is wrong
  on every other.
- `project` follows the project name rules of RFC 003.

Example:

```text
workflows: workflows
working_dir: .
project: billing
```

Default layout:

```text
project/
  .dagu.yaml
  base.yaml
  workflows/
    deploy.yaml
    etl/
      daily.yaml
  scripts/
    deploy.sh
  .dagu/                 run state, ignored by git; see Project state
```

## Finding the project

- The CLI looks for `.dagu.yaml` in the current directory, then in each parent
  directory, and stops at the first one it finds. That directory is
  `project_root`.
- A project nested inside another project hides the outer one.
- Without `.dagu.yaml` in the current directory or above it, commands do not
  run in project mode. A folder that a server project uses is still found
  without it; see [Server projects](#server-projects).

## Commands

| Command | In project mode |
| --- | --- |
| `dagu init` | Creates a project in the current directory. |
| `dagu list` | Prints the workflow targets. |
| `dagu validate [<target>]` | Validates the project file and every workflow, or one workflow. |
| `dagu start <target> [-- params]` | Runs one workflow. |
| Other commands that take a DAG, such as `status`, `stop`, `retry`, and `enqueue` | Resolve the DAG as a workflow target. |
| `dagu server`, `dagu start-all` | Serve the project's workflows and runs. `start-all` also schedules them. Both refuse inside a server project's DAG directory; see [Server projects](#server-projects). |

Rules:

- `dagu init` writes `.dagu.yaml`, `workflows/hello.yaml`, and
  `.dagu/.gitignore`, then prints `dagu start hello` as the next command. It
  refuses when `.dagu.yaml` already exists in the current directory, and it
  never overwrites a file.
- `dagu list` prints targets in lexicographic order, one per line.
- `dagu validate` without `<target>` validates the project file and every
  discovered workflow.
- Listing and validation must not execute workflow steps.

Command output:

| Command | Condition | Exit code | Stdout | Stderr |
| --- | --- | --- | --- | --- |
| `dagu list` | Success | `0` | Discovered workflow targets, sorted, one per line. | Empty. |
| `dagu list` | Failure | Non-zero | Empty. | Project loading error. |
| `dagu validate` | Success | `0` | Empty. | Empty. |
| `dagu validate` | Failure | Non-zero | Empty. | Validation error. |

Validation failures must not print command usage text.

`hello.yaml` as `dagu init` writes it:

```yaml
steps:
  - name: hello
    run: echo "hello from Dagu"
```

## Workflow discovery

Dagu resolves the workflow directory from `.dagu.yaml`.

Rules:

- If `workflows` is omitted, the workflow directory is
  `project_root/workflows`. When that directory does not exist, the project
  has no workflows.
- A configured `workflows` directory that does not exist, or is not a
  directory, is a loading error, so a typo never reads as an empty project.
- Dagu searches the workflow directory and its subdirectories. It skips
  directories whose names start with `.`.
- Dagu does not follow symbolic links.
- Discovered files end in `.yaml` or `.yml`.
- `base.yaml` in `project_root` is never a workflow, even when the workflow
  directory is `project_root`.
- A workflow's target is its path relative to the workflow directory, with
  `/` as the separator and without its extension: `workflows/etl/daily.yaml`
  has the target `etl/daily`.
- Two files whose targets match without regard to case are a loading error.
  `deploy.yaml` and `deploy.yml` would be one target, and `Deploy.yaml` and
  `deploy.yaml` one file on macOS and Windows.

## Target resolution

A command's `<target>` selects one discovered workflow. Dagu accepts, in this
order:

1. A target, such as `deploy` or `etl/daily`.
2. A target with its extension, such as `deploy.yaml`.
3. A path from the current directory to a discovered workflow file, such as
   `workflows/deploy.yaml`, as shell completion writes it.

Any other `<target>` fails, and the error lists the closest targets. Dagu
never runs a file that discovery did not find.

## Base config

A workflow's base config is the user's global base config, then `base.yaml` in
`project_root` when it exists, then the workflow file. This is the order a
server applies to a project's DAGs.

## Working directory

The process working directory is the directory from which step commands run.

Rules:

- The default process working directory is `project_root`, or the
  `working_dir` of `.dagu.yaml` resolved from `project_root`.
- A relative workflow root `working_dir` is resolved from the project default
  process working directory.
- An absolute workflow root `working_dir` is used as written.
- Step commands run from the resolved process working directory.
- Command path lookup is done by the shell or operating system from that
  directory.

Example:

```text
project/
  .dagu.yaml
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

It resolves the same way when `dagu start deploy` runs from
`project/scripts/` or any other directory inside the project.

## Project state

Project-mode runs keep their state in one directory, `project_root/.dagu/`,
laid out so that a person who opens it finds what they look for:

```text
.dagu/
  .gitignore     contains "*"
  runs/          run records, by workflow and date
  logs/          step logs
  artifacts/     run artifacts
  state/         workflow state, queue, scheduler, and other internal state
```

Rules:

- Dagu creates `.dagu/` and its `.gitignore` on first use, so git ignores the
  state without setup.
- One directory keeps Dagu's files apart from a project's own `logs/` or
  `runs/`, and discovery never enters it, since it skips hidden directories.
- Project mode uses the user's Dagu configuration, except that its DAG
  directory is the workflow directory and its state lives under `.dagu/` as
  shown.
- A server project on an existing folder ([RFC 003](../rfcs/003-projects.md))
  uses the same layout, so the CLI and the server read one history.
- `dagu start` prints where the run's logs are when it finishes, and
  `dagu history` lists past runs, so people rarely need to open `.dagu/`.
- Project loading and workflow discovery do not write workflow events, run
  logs, artifacts, or result files.

## Server projects

A project and a server project of [RFC 003](../rfcs/003-projects.md) are the
same thing: a checkout is a project's DAG directory on a laptop, and a server
project's DAG directory may be a checkout.

Rules:

- A server project's DAG directory may hold `.dagu.yaml`. The server then
  discovers workflows, applies base config, and sets working directories as
  this spec says, whatever the server's `dag_discovery` settings are.
- On a server, a `project` field that differs from the server project's name
  is a warning on the projects page, not a loading error, so renaming a
  project never stops it from loading. The rename asks the admin to update
  the field.
- A command runs in the first of these that applies:
  1. The server project named by `--project` or `DAGU_PROJECT`.
  2. The server project of the configured Dagu home whose DAG directory
     contains the current directory, with or without `.dagu.yaml`. This
     includes a project on an existing folder. The command uses that
     project's rules, base config, secrets, and run history.
  3. Project mode, when `.dagu.yaml` is found from the current directory,
     with its state in `.dagu/`.
  4. The default project of the configured Dagu home, as without projects.
- Inside a server project's DAG directory, as in step 2, `dagu server` and
  `dagu start-all` refuse to start. The server already schedules that
  project, and a second scheduler with its own lock would run each
  scheduled workflow twice.
- When the CLI addresses a remote server through a context, a project-mode
  command addresses the server project named by `project`, and fails when
  `project` is not set.

## Outputs

Step stdout, stderr, exit code, and runtime outputs belong to execution specs.

## Errors

Project loading must fail when:

- `.dagu.yaml` exists but is not a file.
- `.dagu.yaml` is not valid YAML, or is not a mapping.
- `.dagu.yaml` contains an invalid field defined by this spec.
- A configured workflow directory is missing or is not a directory.
- The workflow directory resolves outside `project_root`.
- Two workflow files have targets that match without regard to case.

Workflow selection or execution must fail when:

- `<target>` selects no discovered workflow.
- A discovered workflow fails YAML schema validation.
- A relative `working_dir` resolves outside `project_root`.
- The resolved `working_dir` does not exist.

A command that references a missing project file fails when the step executes,
not during project loading.

## Examples

Configured workflow directory:

```text
project/
  .dagu.yaml
  pipelines/
    deploy.yaml
    etl/
      daily.yaml
  workflows/
    backup.yaml
```

`.dagu.yaml`:

```text
workflows: pipelines
```

`dagu list` prints:

```text
deploy
etl/daily
```

From a subdirectory, the project is found above it:

```sh
cd project/pipelines/etl
dagu start deploy
```

Unknown targets do not fall back to arbitrary files:

```sh
dagu start ../other.yaml
```

If `../other.yaml` is not a discovered workflow file, the command fails.

A new project:

```sh
mkdir demo && cd demo
dagu init
dagu start hello
```
