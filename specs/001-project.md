# Spec: Project

## Status

Not implemented. This spec describes target conformance behavior and must not be
treated as current product behavior.

## Scope

This spec defines how the CLI finds a project, discovers its workflows,
resolves workflow targets and working directories, and where a project's runs
keep their state. It also defines how a project relates to a server project of
[RFC 003](../rfcs/003-projects.md).

It does not define workflow YAML semantics, step execution, or output formats.

## Goal

Let a repository keep its workflows next to its code, and run them with no
server and no setup beyond `dagu init`.

A project is a directory that contains `dagu.json`. That directory is
`project_root`.

## Project file

`dagu.json` must be a JSON object. An empty object, `{}`, is valid.

Fields defined by this spec:

| Field | Required | Meaning |
| --- | --- | --- |
| `workflows` | No | Project-relative directory that contains workflow files. Defaults to `workflows`. |
| `working_dir` | No | Project-relative default working directory for steps. Defaults to `.`. |
| `project` | No | Name of the project. Defaults to a valid name made from the name of `project_root`, as RFC 003 makes one. The project's runs and settings are kept under this name; see [Project state](#project-state). |

Rules:

- `workflows` and `working_dir` must be relative paths that resolve inside
  `project_root`. An absolute path names a place on one machine and is wrong
  on every other.
- `project` follows the project name rules of RFC 003.

Example:

```json
{
  "workflows": "workflows",
  "working_dir": ".",
  "project": "billing"
}
```

Default layout:

```text
project/
  dagu.json
  base.yaml
  workflows/
    deploy.yaml
    etl/
      daily.yaml
  scripts/
    deploy.sh
```

## Finding the project

- The CLI looks for `dagu.json` in the current directory, then in each parent
  directory, and stops at the first one it finds. That directory is
  `project_root`.
- A project nested inside another project hides the outer one.
- Without `dagu.json` in the current directory or above it, a command
  addresses a server project or the default project, as
  [Server projects](#server-projects) says.

## Commands

| Command | Inside a project |
| --- | --- |
| `dagu init` | Creates a project in the current directory. |
| `dagu list` | Prints the workflow targets. |
| `dagu validate [<target>]` | Validates the project file and every workflow, or one workflow. |
| `dagu start <target> [-- params]` | Runs one workflow. |
| Other commands that take a DAG, such as `status`, `stop`, `retry`, and `enqueue` | Resolve the DAG as a workflow target. |
| `dagu server`, `dagu start-all` | Serve the server projects of the configured Dagu home, as outside a project. The folder is served only as a server project's DAG directory. |

Rules:

- `dagu init` writes `dagu.json`, naming the project after the current
  directory, such as `{"project": "myapp"}`, and `workflows/hello.yaml`,
  then prints `dagu start hello` as the next command. It refuses when
  `dagu.json` already exists in the current directory, and it never
  overwrites a file.
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

Dagu resolves the workflow directory from `dagu.json`.

Rules:

- If `workflows` is omitted, the workflow directory is
  `project_root/workflows`. When that directory does not exist, the project
  has no workflows.
- A configured `workflows` directory that does not exist, or is not a
  directory, is a loading error, so a typo never reads as an empty project.
- Dagu searches the workflow directory and its subdirectories. It skips
  directories whose names start with `.`.
- Dagu skips the directory `skills` at the root of the workflow directory.
  It holds skill folders, not workflows ([Spec 096](096-skills.md)).
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

A workflow's base config is `base.yaml` in `project_root` when it exists, then
the workflow file. No other base config applies, as on a server
([RFC 003](../rfcs/003-projects.md#base-config)).

## Working directory

The process working directory is the directory from which step commands run.

Rules:

- The default process working directory is `project_root`, or the
  `working_dir` of `dagu.json` resolved from `project_root`.
- A relative workflow root `working_dir` is resolved from the project default
  process working directory.
- An absolute workflow root `working_dir` is used as written.
- Step commands run from the resolved process working directory.
- Command path lookup is done by the shell or operating system from that
  directory.

Example:

```text
project/
  dagu.json
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

A project keeps its runs and its own settings in the data directory of the
user's Dagu home, under its name, never inside `project_root`. A checkout
stays clean, and `git clean -x` or a fresh clone loses nothing.

```text
<data_dir>/projects/<name>/
  runs/          run records, by workflow and date
  logs/          step logs
  artifacts/     run artifacts
  settings/      secrets, profiles, notification routes, incident policies
  state/         workflow state, queue, scheduler, and other internal state
```

Rules:

- The project's name selects its state. Two folders whose `dagu.json` names
  one project that no server project of the Dagu home registers, such as
  two clones of one repository, share its runs and settings. A retry runs
  the definition its run recorded.
- Inside a project, the CLI uses the user's Dagu configuration, except that
  its DAG directory is the workflow directory.
- A workflow uses only the project's secrets, profiles, notification routes,
  and incident policies, as [RFC 003](../rfcs/003-projects.md#settings)
  defines. Inside a project, `dagu secret` and `dagu profile` act on the
  project's entries.
- Secrets are encrypted with the key of the user's Dagu home, as on a
  server.
- A server project ([RFC 003](../rfcs/003-projects.md#layout)) keeps its
  state the same way, in the server's data directory. Only its DAG
  directory may use its name: a command outside every server project's DAG
  directory whose `dagu.json` names it fails, and the error names that DAG
  directory.
- A project that no server project of the Dagu home registers has no
  scheduler: `dagu start` runs its workflows, `dagu enqueue` and a dispatch
  to a worker fail with an error that says only a registered project can,
  and no `schedule` runs.
- `dagu start` prints where the run's logs are when it finishes, and
  `dagu history` lists past runs.
- Project loading and workflow discovery do not write workflow events, run
  logs, artifacts, or result files.

## Server projects

A project and a server project of [RFC 003](../rfcs/003-projects.md) are the
same thing: a checkout is a project's DAG directory on a laptop, and a server
project's DAG directory may be a checkout.

Rules:

- A server project's DAG directory may hold `dagu.json`. The server then
  discovers workflows, applies base config, and sets working directories as
  this spec says, whatever the server's `dag_discovery` settings are.
- On a server, a `project` field that differs from the server project's name
  is a warning on the projects page, not a loading error.
- A command runs in the first of these that applies:
  1. The server project named by `--project` or `DAGU_PROJECT`.
  2. The server project of the configured Dagu home whose DAG directory
     contains the current directory, with or without `dagu.json`. The
     command uses that project's rules, base config, secrets, and run
     history.
  3. The project named by the `dagu.json` found from the current
     directory, unless a server project of that name is registered, which
     fails as [Project state](#project-state) says.
  4. The default project of the configured Dagu home, as without projects.
- When the CLI addresses a remote server through a context, a command
  inside a project addresses the server project of the project's name.

## Outputs

Step stdout, stderr, exit code, and runtime outputs belong to execution specs.

## Errors

Project loading must fail when:

- `dagu.json` exists but is not a file.
- `dagu.json` is not valid JSON, or is not an object.
- `dagu.json` contains an invalid field defined by this spec.
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
  dagu.json
  pipelines/
    deploy.yaml
    etl/
      daily.yaml
  workflows/
    backup.yaml
```

`dagu.json`:

```json
{ "workflows": "pipelines" }
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
