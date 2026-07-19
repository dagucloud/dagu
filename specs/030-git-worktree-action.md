# Spec: Git Worktree Action

## Status

Not implemented.

This spec defines conformance behavior for the built-in `git.worktree.add` and
`git.worktree.remove` actions.

## Scope

This spec defines linked-worktree management for a local Git repository:

- `git.worktree.add`, which ensures a linked worktree exists for a branch.
- `git.worktree.remove`, which removes a linked worktree.

This spec covers:

- accepted `with` fields for both operations
- repository detection from within the step working directory's repository
- path resolution and the default worktree path
- explicit branch creation from a base revision
- idempotent add and remove behavior
- conflict detection
- repository-scoped mutation serialization
- the result document
- validation and runtime errors

This spec does not define:

- the `git.checkout` operation
- cloning, fetching, pushing, or any network operation
- repository authentication fields
- copying untracked files into a new worktree
- worktree locking, moving, repairing, or pruning
- scheduler, queue, API, UI, or distributed worker behavior
- mutations performed outside Dagu while an action is running

## Goal

Workflow authors can give each branch an isolated working directory inside a
DAG, and later remove it, without shelling out to external worktree helper
tools.

Both operations must be safe to re-run: a repeated add reuses the existing
worktree, and a repeated remove succeeds without one.

Workflow authors select the repository through the standard step working
directory and do not repeat that path in `with`.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Step run: [Spec 013: Step Run](013-step-run.md)

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
| `branch` | Yes | Branch checked out in the linked worktree. |
| `path` | No | Worktree path. Defaults to the default worktree path. |
| `create_branch` | No | Permit creation of `branch` when it does not exist. Defaults to `false`. |
| `base` | No | Base revision used only when `create_branch` is `true` and `branch` does not exist. Defaults to the detected repository `HEAD`. |

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
- `base` requires `create_branch: true`.
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

- The step `working_dir` and the string-valued `branch`, `path`, and `base`
  fields are value-resolved at step start according to Spec 003.
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
- The default worktree path is `<repository root>.worktrees/<branch>`.
- `/` separators in `branch` are preserved as directory separators in the
  default worktree path.
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
  fails without creating a branch or worktree.
- If `branch` does not exist and `create_branch` is `true`, the add operation
  creates it at the commit resolved from `base`.
- If `branch` does not exist, `create_branch` is `true`, and `base` is omitted,
  the branch is created at the detected repository `HEAD` commit.
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
  execute their inspect-and-mutate sequences serially, including when they
  belong to different DAG runs in the same filesystem environment.
- Serialization begins before worktree registrations or refs are inspected and
  ends after the result document is written or the operation fails.
- Waiting for serialization is context-cancellable. Abort or timeout while
  waiting makes no repository change and writes no result document.
- Serialization does not coordinate Git mutations performed outside Dagu.

### Result Document

Rules:

- A successful operation writes exactly one JSON object to stdout, on one
  line, followed by one newline.
- A failed operation writes no result document to stdout.
- Diagnostic text goes to stderr only.
- How later steps consume the result document is owned by the step output and
  value-resolution specs and is not defined by this spec.

`git.worktree.add` result fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `operation` | string | Always `worktree_add`. |
| `path` | string | The worktree path. |
| `branch` | string | The branch checked out in the worktree. |
| `commit` | string | `HEAD` commit hash of the worktree after the operation. |
| `worktree_created` | boolean | `true` when this run registered the worktree. |
| `branch_created` | boolean | `true` when this run created the branch. |

`git.worktree.remove` result fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `operation` | string | Always `worktree_remove`. |
| `path` | string | The worktree path, empty when no worktree was registered and `path` was omitted. |
| `branch` | string | The branch checked out in the resolved target. When no target resolves, the `branch` input; empty when neither is available. |
| `worktree_removed` | boolean | `true` when this run removed a registered worktree. |
| `branch_deleted` | boolean | `true` when this run deleted the branch. |

### Lifecycle

Rules:

- A failed operation fails the step.
- Validation, revision resolution, registration conflict checks, and remove
  preflight checks complete before the first mutation.
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
- `git.worktree.add` has missing, empty, or non-string `branch`.
- `git.worktree.add` has empty or non-string `path` or `base`.
- `git.worktree.add` has non-boolean `create_branch`.
- `git.worktree.add` has `base` without `create_branch: true`.
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

The step must fail when:

- After value resolution, required `branch` or a present `path` or `base`
  resolves to an empty string.
- The step working directory is not inside a Git repository and is not a bare
  repository directory.
- `git.worktree.add` receives an invalid local branch name.
- `git.worktree.add` cannot resolve `base` while the branch does not exist and
  `create_branch` is `true`.
- `git.worktree.add` names a branch that does not exist while `create_branch`
  is `false`.
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
- Stdout is one JSON line with `operation` `worktree_add`, `worktree_created`
  `true`, and `branch_created` `true`.

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
- `second` reports `worktree_created` `false` and `branch_created` `false`.
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
- The result `path` is the resolved absolute form of that directory.

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
- `remove` reports `worktree_removed` `true` and `branch_deleted` `true`.

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
- The result reports `worktree_removed` `false` and `branch_deleted` `false`.

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
    with:
      branch: feature-x
      create_branch: true
    output:
      path:
        from: stdout
        decode: json
        select: .path

  - id: test
    depends: create_worktree
    working_dir: "${create_worktree.output.path}"
    run: go test ./...
```

Expected behavior:

- `create_worktree.output.path` is the absolute worktree path from the action
  result.
- `test` runs in the created or reused worktree.
