# Spec: Project

Scope: project root, workflow definition discovery, and relative path
resolution for data-plane execution.

## Objective

Define the filesystem shape of one Dagu project.

## Inputs

A project is a directory that contains `.dagu/`.

`.dagu/` is the workflow definitions directory.

Workflow definition files must be direct children of `.dagu/`.

Workflow definition files must have the `.yaml` or `.yml` extension.

Example project:

```text
project/
  .dagu/
    deploy.yaml
    backup.yaml
  scripts/
    deploy.sh
  files/
    config.json
```

## Behavior

`project_root` is the directory containing `.dagu/`.

Workflow definitions are discovered by listing direct `.yaml` and `.yml` files
under `.dagu/`.

Subdirectories under `.dagu/` are not valid workflow definition locations.

`workflow_id` is the workflow definition filename, such as `deploy.yaml`.

The first YAML document in a workflow definition is the entrypoint DAG.

Later YAML documents in the same workflow definition are inline sub-DAG
definitions for that workflow.

Later YAML documents are not separate top-level workflow definitions.

The default process working directory is `project_root`.

When root `working_dir` is relative, it is resolved from `project_root`.

When root `working_dir` is absolute, it is used as written.

Step commands run from the step's process working directory.

A command path such as `./scripts/deploy.sh` is discovered by the operating
system or shell relative to the step's process working directory.

Project-relative behavior must not depend on the caller's current working
directory after `project_root` has been resolved.

## Outputs

Project loading succeeds before step execution starts.

Project loading by itself does not write workflow result events, run logs, or
artifacts.

Step stdout, stderr, exit code, and runtime outputs belong to execution specs.

## Errors

A missing `.dagu/` directory must fail project loading.

A `.dagu` path that is not a directory must fail project loading.

A workflow definition nested below `.dagu/` must fail project loading.

A workflow definition that fails YAML validation must fail before execution.

A relative `working_dir` that resolves outside `project_root` must fail before
execution.

A `working_dir` path that does not exist must fail before execution.

A command that references a missing project file fails at step execution time.

## Examples

Project with a local script:

```text
project/
  .dagu/
    deploy.yaml
  scripts/
    deploy.sh
```

`.dagu/deploy.yaml`:

```yaml
steps:
  - name: deploy
    run: ./scripts/deploy.sh
```

`./scripts/deploy.sh` is discovered at:

```text
project/scripts/deploy.sh
```

Invalid project because workflow definitions must not be nested:

```text
project/
  .dagu/
    maintenance/
      cleanup.yaml
```

## Acceptance Criteria

- A black-box fixture discovers direct `.yaml` files under `.dagu/`.
- A black-box fixture discovers direct `.yml` files under `.dagu/`.
- A black-box fixture rejects a project without `.dagu/`.
- A black-box fixture rejects `.dagu` when it is not a directory.
- A black-box fixture rejects nested workflow definitions under `.dagu/`.
- A black-box fixture does not treat later `---` documents as top-level
  workflow definitions.
- A black-box fixture runs `.dagu/deploy.yaml` with `project_root` as the
  default process working directory.
- A black-box fixture resolves `./scripts/deploy.sh` from `project_root`.
- A black-box fixture gives the same result when invoked from outside
  `project_root`.
