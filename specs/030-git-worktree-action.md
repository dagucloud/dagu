# Spec: Git Worktree Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `git.worktree.add` and
`git.worktree.remove` actions.

## Scope

This spec defines linked-worktree management for a local Git repository:

- `git.worktree.add`, which ensures a linked worktree exists for an explicit or
  Dagu-generated branch.
- `git.worktree.remove`, which removes a linked worktree.

This spec covers:

- accepted `with` fields for both operations
- repository detection from within the step working directory's repository
- path resolution and the default worktree path
- deterministic branch generation when `branch` is omitted
- explicit branch creation from a base revision
- idempotent add and remove behavior
- conflict detection
- repository-scoped mutation serialization
- the action output contract
- validation and runtime errors

This spec does not define:

- the `git.checkout` operation
- cloning, fetching, pushing, or any network operation
- repository authentication fields
- copying untracked files into a new worktree
- worktree locking, moving, repairing, or pruning
- mutations performed outside Dagu while an action is running

## Goal

Workflow authors can give each branch an isolated working directory inside a
DAG, and later remove it, without shelling out to external worktree helper
tools.

Both operations must be safe to re-run: a repeated add reuses the existing
worktree, and a repeated remove succeeds without one.

Workflow authors select the repository through the standard step working
directory and do not repeat that path in `with`.

Workflow authors control the worktree lifecycle explicitly with
`git.worktree.remove`.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Step output references: [Spec 007: Value Resolution Steps](007-value-resolution-steps.md)
- Step identity: [Spec 009: Step Reference](009-step-reference.md)
- Step outputs: [Spec 012: Step Outputs](012-step-outputs.md)
- Step run: [Spec 013: Step Run](013-step-run.md)
- Built-in run context: [Spec 017: Built-in Run Context](017-built-in-run-context.md)
- Cloning and checkout: [Spec 055: Git Checkout Action](055-git-checkout.md)

## Terms

The primary working tree is the working tree created with the repository
itself.

A linked worktree is an additional working tree registered to the repository
with its own checked-out branch.

The repository root is the top-level directory of the non-bare working tree
that contains the resolved step working directory. For a bare repository, the
repository root is the bare repository directory.

The common Git directory is the repository metadata directory shared by the
primary working tree and all linked worktrees.

The worktree path is the resolved directory of the linked worktree named by
one operation.

The default worktree path is the worktree path Dagu derives when `path` is
omitted.

A registration is stale when a linked worktree is registered and its directory
does not exist.

The selected branch is the explicit `branch` input when it is present and the
Dagu-generated branch when `branch` is omitted.

## Behavior

### Operation Selection

Rules:

- `action: git.worktree.add` selects the add operation.
- `action: git.worktree.remove` selects the remove operation.
- Any other `git.worktree.*` action name is a validation error.

### Field Shape

`with` fields for `git.worktree.add`:

| Field | Required | Meaning |
| --- | --- | --- |
| `branch` | No | Branch checked out in the linked worktree. When omitted, Dagu generates a branch for the current DAG run and step. |
| `path` | No | Worktree path. Defaults to the default worktree path. |
| `create_branch` | No | Permit creation of an explicitly named `branch` when it does not exist. Defaults to `false`. A generated branch is always eligible for creation. |
| `base` | No | Base revision used when the selected branch is created. Defaults to the detected repository `HEAD`. |

`with` fields for `git.worktree.remove`:

| Field | Required | Meaning |
| --- | --- | --- |
| `branch` | One of `branch`, `path` | Branch whose registered linked worktree is removed. |
| `path` | One of `branch`, `path` | Worktree path to remove. |
| `force` | No | Remove even when the worktree has local changes. Defaults to `false`. |
| `delete_branch` | No | Also delete the local branch when it is fully merged into the detected repository `HEAD`. Requires `branch`. Defaults to `false`. |
| `force_delete_branch` | No | Permit deletion of an unmerged local branch. Requires `delete_branch: true`. Defaults to `false`. |

Rules:

- `branch`, `path`, and `base` must be non-empty strings when present.
- `create_branch`, `force`, `delete_branch`, and `force_delete_branch` must be
  booleans when present.
- A `with` field not listed for the selected operation is a validation error.
- When `branch` is present, `base` requires `create_branch: true`.
- When `branch` is omitted, `base` is allowed without `create_branch: true`.
- `git.worktree.remove` accepts `branch`, `path`, or both.
- `delete_branch: true` requires `branch`.
- `force_delete_branch: true` requires `delete_branch: true`.
- When `git.worktree.remove` receives both `branch` and `path` and either
  resolves to a registered linked worktree, the registered worktree for
  `branch` must be the one at the resolved `path`.
- When `git.worktree.remove` receives both `branch` and `path` and neither
  resolves to a registered linked worktree, the remove operation follows the
  no-target rules.

### Field Evaluation

Rules:

- The step `working_dir` and the reference-capable string fields `branch`,
  `path`, and `base` are value-resolved at step start according to Spec 003.
- Supported Dagu references such as `${params.name}`, `${env.NAME}`, and step
  output references may be used in those string-valued fields when their
  namespace is available.
- Value resolution completes before repository discovery, path resolution,
  branch lookup, or base revision lookup.
- Non-empty string requirements apply to the resolved runtime values as well
  as literal values that can be checked during workflow validation.
- `create_branch`, `force`, `delete_branch`, and `force_delete_branch` are YAML
  booleans and are not string-valued reference fields. A value such as
  `create_branch: "${params.create_branch}"` is a string and is invalid.

### Branch Selection

Rules:

- When `branch` is present, its resolved value is the selected branch.
- When `branch` is omitted, Dagu generates the selected branch from the current
  DAG run identity and the current executable step identity.
- A generated branch name starts with `dagu/` and is a valid local Git branch
  name.
- Generation is deterministic for the same DAG run and executable step. Step
  retries in the same DAG run select the same branch.
- Different executable steps in one DAG run, and executions in different DAG
  runs, select different generated branches.
- Runtime step identity components that are not valid in a Git branch name are
  encoded or replaced. The exact encoding and branch suffix are implementation
  details.
- A generated branch is created when it does not exist; `create_branch: true`
  is not required.
- `create_branch` does not disable creation of a generated branch. Its value
  controls only an explicitly supplied branch.
- If a generated branch already exists after an interrupted or partially
  completed attempt, the action treats it as an existing selected branch. This
  permits a retry to finish creating or reuse the worktree.
- The selected branch name is published as the `branch` output so later steps
  do not need to reconstruct a generated name.
- A later remove operation can receive the published `branch` and `path`
  outputs to remove the generated worktree and, when requested, its branch.

### Path Resolution

Rules:

- The resolved step working directory may be the repository root or any
  directory beneath it.
- The nearest containing non-bare Git working tree is selected. Repository
  discovery must not cross a filesystem boundary that Git itself would not
  cross.
- A primary working tree with a `.git` directory and a linked worktree with a
  `.git` file are both valid discovery roots.
- Relative `path` values resolve from the detected repository root, not from
  the nested step working directory.
- Resolved paths are cleaned before use.
- The default worktree path is `<repository root>.worktrees/<selected branch>`.
- `/` separators in the selected branch are preserved as directory separators
  in the default worktree path.
- Branch names are not flattened: `feature/auth` and `feature-auth` resolve to
  different default worktree paths.
- A bare repository is a valid step working directory for both operations.
- A bare repository has no primary working tree; rules that reference the
  primary working tree are inapplicable to it.
- Linked worktrees are identified only through the repository's worktree
  registration metadata.

Example: with step working directory `/work/repo/services/api` and
`branch: feature/auth`, the repository root is `/work/repo` and the default
worktree path is `/work/repo.worktrees/feature/auth`.

### Add Operation

Rules:

- In this section, `branch` means the selected branch.
- The action validates the branch name, resolves `base` when needed, and checks
  the requested path and existing registrations before creating a branch or
  worktree.
- If the repository has a registered linked worktree for `branch` at the
  worktree path and the registration is not stale, the add operation succeeds
  without changing the worktree.
- Reuse must not discard uncommitted changes in the existing worktree.
- Reuse must not move the existing worktree's `HEAD`.
- A stale registration for `branch` at the worktree path is a runtime error
  for the add operation.
- If no linked worktree is registered for `branch` at the worktree path, the
  add operation registers a linked worktree at the worktree path with `branch`
  checked out.
- If `branch` exists, the new worktree checks out the existing branch without
  moving it.
- If `branch` does not exist and `create_branch` is `false`, the operation
  fails without creating a branch or worktree when `branch` was explicitly
  supplied.
- If `branch` does not exist and `create_branch` is `true`, the add operation
  creates it at the commit resolved from `base`.
- If `branch` was generated and does not exist, the add operation creates it at
  the commit resolved from `base`.
- Whenever a branch is created and `base` is omitted, it is created at the
  detected repository `HEAD` commit.
- `base` resolves in this order: commit hash, `refs/heads/<base>`,
  `refs/remotes/origin/<base>`, `refs/tags/<base>`.
- `base` is ignored when `branch` already exists.
- The add operation must not fetch, push, or contact any remote.

### Remove Operation

Rules:

- The target worktree is located by `path` when `path` is present, otherwise
  by the registered linked worktree of `branch`.
- Before changing the repository, the action resolves both selectors and
  checks path conflicts, dirty state, primary-worktree protection, branch
  checkout conflicts, and branch deletion eligibility.
- A preflight failure must leave the worktree, its registration, and the branch
  unchanged.
- If the target worktree is registered, the remove operation removes its
  directory and unregisters it.
- If the target registration is stale, the remove operation unregisters it,
  reports `worktree_removed` `true`, and does not check dirty state.
- If no target worktree is registered, the remove operation succeeds without
  removing anything.
- A worktree with uncommitted changes is removed only when `force` is `true`.
- The remove operation must refuse to remove the primary working tree.
- With `delete_branch: true`, the local branch is deleted after the worktree
  is removed.
- With `delete_branch: true`, the branch must be fully merged into the detected
  repository `HEAD` unless `force_delete_branch` is `true`.
- `force` does not permit deletion of an unmerged branch.
- With `force_delete_branch: true`, the branch is deleted even when it is not
  merged.
- `delete_branch` must not delete a branch that is checked out in another
  worktree.
- With `delete_branch: true` and no registered worktree for the branch, the
  branch is still deleted when it exists.
- The remove operation must not fetch, push, or contact any remote.

### Mutation Serialization

Rules:

- Add and remove operations that resolve to the same common Git directory must
  execute their inspect-and-mutate sequences serially,
  including when they belong to different DAG runs in the same filesystem
  environment.
- Serialization begins before worktree registrations or refs are inspected and
  ends after action outputs are published or the operation's failure is
  recorded.
- Waiting for serialization is context-cancellable. Abort or timeout while
  waiting makes no repository change and publishes no outputs.
- Serialization does not coordinate Git mutations performed outside Dagu.

### Action Outputs

Rules:

- Both actions have a fixed output contract. Workflow authors do not declare an
  `output`, declare top-level `outputs`, or decode stdout to use it.
- Action normalization makes the fixed contract available to step-output
  reference validation without requiring a top-level `outputs` field in the
  workflow.
- After a successful operation, the executor publishes all output fields as one
  atomic set on the same lookup surface as declared step outputs.
- The built-in executor publishes these values directly. It does not use
  `DAGU_OUTPUT_FILE`; this spec owns that exception to the file-based producer
  defined by Spec 012.
- A dependent step reads a field with
  `${steps.<step-id>.outputs.<field>}` according to Specs 007 and 012.
- A failed, aborted, or timed-out operation publishes no outputs from that
  attempt. Outputs from a failed attempt must not be available to later steps
  or a retry.
- Stdout is not a result channel for these actions. Workflow behavior must not
  depend on parsing action stdout.
- Diagnostic text goes to stderr.

`git.worktree.add` output fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `path` | string | The worktree path. |
| `branch` | string | The selected branch checked out in the worktree, including the generated name when the input omitted `branch`. |
| `commit` | string | `HEAD` commit hash of the worktree after the operation. |
| `worktree_created` | boolean | `true` when this run registered the worktree. |
| `branch_created` | boolean | `true` when this run created the branch. |

`git.worktree.remove` output fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `path` | string | The worktree path, empty when no worktree was registered and `path` was omitted. |
| `branch` | string | The branch checked out in the resolved target. When no target resolves, the `branch` input; empty when neither is available. |
| `worktree_removed` | boolean | `true` when this run removed a registered worktree. |
| `branch_deleted` | boolean | `true` when this run deleted the branch. |

String fields use the `string` output type. Boolean fields use the `json`
output type and publish JSON booleans. When inserted into a string-valued field,
the booleans resolve as `true` or `false` according to Spec 003.

### Lifecycle

Rules:

- A failed operation fails the step.
- Validation, revision resolution, registration conflict checks, and remove
  preflight checks complete before the first mutation.
- Output publication occurs only after every required Git mutation succeeds.
- If an unexpected failure occurs after the add operation creates the branch,
  the created branch may remain.
- A failed or interrupted add operation must not leave a registered worktree
  whose directory does not exist.
- After successful preflight, the remove operation removes the worktree before
  deleting the branch.
- If branch deletion fails after the worktree was removed, the worktree
  removal is not rolled back and the step fails.
- Workflow abort and step timeout interrupt the operation and the step follows
  the abort or timeout behavior.

## Errors

### Validation Errors

Validation must fail when:

- The action name is not `git.worktree.add` or `git.worktree.remove` in the
  `git.worktree.*` namespace.
- `git.worktree.add` has empty or non-string `branch` when present.
- `git.worktree.add` has empty or non-string `path` or `base`.
- `git.worktree.add` has non-boolean `create_branch`.
- `git.worktree.add` has both an explicit `branch` and `base` without
  `create_branch: true`.
- `git.worktree.remove` has neither `branch` nor `path`.
- `git.worktree.remove` has empty or non-string `branch` or `path`.
- `git.worktree.remove` has `delete_branch: true` without `branch`.
- `git.worktree.remove` has `force_delete_branch: true` without
  `delete_branch: true`.
- `force`, `delete_branch`, or `force_delete_branch` is present and not a
  boolean.
- A `with` field is not listed for the selected operation.

Validation must not:

- Check whether the step working directory is inside a Git repository.
- Check whether `branch`, `path`, or `base` can be resolved.

### Runtime Errors

The action step must fail when:

- After value resolution, a present `branch`, `path`, or `base` resolves to an
  empty string.
- The step working directory is not inside a Git repository and is not a bare
  repository directory.
- `git.worktree.add` receives an invalid explicit local branch name.
- `git.worktree.add` cannot resolve `base` while the branch does not exist and
  the action is permitted to create it.
- `git.worktree.add` explicitly names a branch that does not exist while
  `create_branch` is `false`.
- In `git.worktree.add`, the worktree path exists and is not an empty
  directory and is not the registered worktree for `branch`.
- In `git.worktree.add`, `branch` is checked out in the primary working tree.
- In `git.worktree.add`, `branch` is registered to a linked worktree at a
  different path than the worktree path.
- In `git.worktree.add`, the registration for `branch` at the worktree path
  is stale.
- `git.worktree.remove` targets the primary working tree.
- `git.worktree.remove` receives both `branch` and `path`, at least one of
  them resolves to a registered linked worktree, and the registered worktree
  for `branch` is not the one at the resolved `path`.
- In `git.worktree.remove`, the target worktree has uncommitted changes and
  `force` is `false`.
- `delete_branch` is `true`, the branch is not fully merged into the detected
  repository `HEAD`, and `force_delete_branch` is `false`.
- `delete_branch` is `true` and the branch is checked out in another worktree
  after removal.

Runtime diagnostics must identify the selected operation, detected repository
root, relevant branch or path, and the reason for failure. A stale-registration
diagnostic must explain that `git.worktree.remove` can unregister the stale
target before retrying the add operation.

## Examples

Each example assumes a fixture repository at `./repo` with an initial commit
on branch `main`, prepared by test setup.

### Add With Generated Branch

```yaml
working_dir: ./repo
steps:
  - id: create_worktree
    action: git.worktree.add
```

Expected behavior:

- Dagu generates a valid branch name beginning with `dagu/`.
- The generated branch starts at the detected repository `HEAD` commit.
- The worktree is created at
  `./repo.worktrees/<generated branch>`.
- The action publishes the generated name in `branch`, with
  `worktree_created` and `branch_created` both `true`.

### Parameterized Inputs

```yaml
params:
  - name: branch
    type: string
    required: true
  - name: base
    type: string
    required: true

working_dir: ./repo
steps:
  - id: create_worktree
    action: git.worktree.add
    with:
      branch: "${params.branch}"
      create_branch: true
      base: "${params.base}"
```

Expected behavior:

- `branch` and `base` resolve from the run parameters when the step starts.
- `create_branch` remains a literal boolean.

### Add Creates Branch And Worktree

```yaml
working_dir: ./repo
steps:
  - id: create_worktree
    action: git.worktree.add
    with:
      branch: feature-x
      create_branch: true
      path: ../wt/feature-x
```

Expected behavior:

- `./wt/feature-x` is a linked worktree of `./repo`.
- Branch `feature-x` exists and is checked out in `./wt/feature-x`.
- The branch points at the commit that `HEAD` of `./repo` pointed at.
- The action outputs `worktree_created` and `branch_created` are both `true`.

### Add Is Idempotent

```yaml
working_dir: ./repo
steps:
  - id: first
    action: git.worktree.add
    with:
      branch: feature-x
      create_branch: true
      path: ../wt/feature-x

  - id: second
    depends: first
    action: git.worktree.add
    with:
      branch: feature-x
      create_branch: true
      path: ../wt/feature-x
```

Expected behavior:

- Both steps succeed.
- `second` publishes `worktree_created` `false` and `branch_created` `false`.
- Files created in `./wt/feature-x` between the two steps remain.

### Add From Base Revision

```yaml
working_dir: ./repo
steps:
  - id: base_tag
    action: git.worktree.add
    with:
      branch: hotfix
      create_branch: true
      base: v1.0.0
      path: ../wt/hotfix
```

Expected behavior:

- Branch `hotfix` is created at the commit tagged `v1.0.0`.
- `./wt/hotfix` checks out `hotfix`.

### Default Worktree Path

```yaml
working_dir: ./repo
steps:
  - id: default_path
    action: git.worktree.add
    with:
      branch: feature/auth
      create_branch: true
```

Expected behavior:

- The worktree is created at `./repo.worktrees/feature/auth`.
- The `path` output is the resolved absolute form of that directory.

### Remove With Branch Delete

```yaml
working_dir: ./repo
steps:
  - id: create
    action: git.worktree.add
    with:
      branch: short-lived
      create_branch: true
      path: ../wt/short-lived

  - id: remove
    depends: create
    action: git.worktree.remove
    with:
      branch: short-lived
      delete_branch: true
```

Expected behavior:

- `./wt/short-lived` does not exist after `remove`.
- Branch `short-lived` does not exist after `remove`.
- `remove` publishes `worktree_removed` `true` and `branch_deleted` `true`.

### Remove Is Idempotent

```yaml
working_dir: ./repo
steps:
  - id: remove_missing
    action: git.worktree.remove
    with:
      branch: never-created
```

Expected behavior:

- The step succeeds.
- The action publishes `worktree_removed` `false` and `branch_deleted` `false`.

### Unmerged Branch Deletion Requires Explicit Force

```yaml
working_dir: ./repo
steps:
  - id: remove_unmerged
    action: git.worktree.remove
    with:
      branch: experimental
      delete_branch: true
      force_delete_branch: true
```

Expected behavior:

- Without `force_delete_branch: true`, the step fails before removing the
  worktree when `experimental` is not merged into `HEAD`.
- With `force_delete_branch: true`, the worktree and unmerged branch are
  removed.
- `force: true` alone does not permit unmerged branch deletion.

### Dirty Remove Requires Force

```yaml
working_dir: ./repo
steps:
  - id: create
    action: git.worktree.add
    with:
      branch: dirty
      create_branch: true
      path: ../wt/dirty

  - id: make_dirty
    depends: create
    run: touch ../wt/dirty/untracked-file

  - id: remove
    depends: make_dirty
    action: git.worktree.remove
    with:
      branch: dirty
```

Expected behavior:

- `remove` fails.
- `./wt/dirty` still exists with `untracked-file`.
- The same remove with `force: true` succeeds and deletes the directory.

### Occupied Path Fails

```yaml
working_dir: ./repo
steps:
  - id: occupy
    run: mkdir -p ../wt/taken && touch ../wt/taken/file

  - id: add
    depends: occupy
    action: git.worktree.add
    with:
      branch: taken
      create_branch: true
      path: ../wt/taken
```

Expected behavior:

- `add` fails.
- `./wt/taken/file` still exists.
- No worktree for `taken` is registered in `./repo`.
- Branch `taken` was not created because path preflight failed.

### Use The Worktree In A Later Step

```yaml
working_dir: ./repo
steps:
  - id: create_worktree
    action: git.worktree.add

  - id: test
    depends: create_worktree
    working_dir: "${steps.create_worktree.outputs.path}"
    run: go test ./...

  - id: remove_worktree
    depends: test
    action: git.worktree.remove
    with:
      path: "${steps.create_worktree.outputs.path}"
      branch: "${steps.create_worktree.outputs.branch}"
```

Expected behavior:

- `${steps.create_worktree.outputs.path}` resolves to the absolute worktree
  path published by the action without a workflow-side output declaration.
- `${steps.create_worktree.outputs.branch}` is the generated branch name and
  can be passed to a later remove operation.
- `test` runs in the created or reused worktree.
- `remove_worktree` removes the same worktree without reconstructing its path or
  generated branch name.
