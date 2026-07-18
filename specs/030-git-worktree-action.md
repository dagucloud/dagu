# Spec: Git Worktree Action

## Status

Not implemented.

This spec defines conformance behavior for the built-in `git.worktree_add` and
`git.worktree_remove` actions.

## Scope

This spec defines linked-worktree management for a local Git repository:

- `git.worktree_add`, which ensures a linked worktree exists for a branch.
- `git.worktree_remove`, which removes a linked worktree.

This spec covers:

- accepted `with` fields for both operations
- path resolution and the default worktree path
- branch creation from a start point
- idempotent add and remove behavior
- conflict detection
- the result document
- validation and runtime errors

This spec does not define:

- the `git.checkout` operation
- cloning, fetching, pushing, or any network operation
- repository authentication fields
- copying untracked files into a new worktree
- worktree locking, moving, repairing, or pruning
- scheduler, queue, API, UI, or distributed worker behavior
- concurrent DAG runs mutating the same repository

## Goal

Workflow authors can give each branch an isolated working directory inside a
DAG, and later remove it, without shelling out to external worktree helper
tools.

Both operations must be safe to re-run: a repeated add reuses the existing
worktree, and a repeated remove succeeds without one.

## Related Specs

- YAML schema: [Spec 002: YAML Schema](002-yaml-schema.md)
- Value resolution: [Spec 003: Value Resolution and Field Evaluation](003-value-resolution.md)
- Step run: [Spec 013: Step Run](013-step-run.md)

## Terms

The primary working tree is the working tree created with the repository
itself.

A linked worktree is an additional working tree registered to the repository
with its own checked-out branch.

The repository path is the resolved value of `repository`.

The worktree path is the resolved directory of the linked worktree named by
one operation.

The default worktree path is the worktree path Dagu derives when `path` is
omitted.

A registration is stale when a linked worktree is registered and its directory
does not exist.

## Behavior

### Operation Selection

Rules:

- `action: git.worktree_add` selects the add operation.
- `action: git.worktree_remove` selects the remove operation.
- Any other `git.worktree*` action name is a validation error.

### Field Shape

`with` fields for `git.worktree_add`:

| Field | Required | Meaning |
| --- | --- | --- |
| `repository` | Yes | Local path of an existing Git repository. |
| `branch` | Yes | Branch checked out in the linked worktree. |
| `path` | No | Worktree path. Defaults to the default worktree path. |
| `from` | No | Start point used only when `branch` does not exist. |

`with` fields for `git.worktree_remove`:

| Field | Required | Meaning |
| --- | --- | --- |
| `repository` | Yes | Local path of an existing Git repository. |
| `branch` | One of `branch`, `path` | Branch whose registered linked worktree is removed. |
| `path` | One of `branch`, `path` | Worktree path to remove. |
| `force` | No | Remove even when the worktree has local changes. Defaults to `false`. |
| `delete_branch` | No | Also delete the local branch after removal. Requires `branch`. Defaults to `false`. |

Rules:

- `repository` must be a non-empty string.
- `branch`, `path`, and `from` must be non-empty strings when present.
- `force` and `delete_branch` must be booleans when present.
- A `with` field not listed for the selected operation is a validation error.
- `git.worktree_remove` accepts `branch`, `path`, or both.
- `delete_branch: true` requires `branch`.
- When `git.worktree_remove` receives both `branch` and `path` and either
  resolves to a registered linked worktree, the registered worktree for
  `branch` must be the one at the resolved `path`.
- When `git.worktree_remove` receives both `branch` and `path` and neither
  resolves to a registered linked worktree, the remove operation follows the
  no-target rules.

### Path Resolution

Rules:

- Relative `repository` and `path` values resolve from the step working
  directory.
- Resolved paths are cleaned before use.
- The default worktree path is `<repository path>.worktrees/<folder name>`.
- The folder name is `branch` with every `/` replaced by `-`.
- The repository path must contain an existing Git repository.
- A bare repository is a valid `repository` for both operations.
- A bare repository has no primary working tree; rules that reference the
  primary working tree are inapplicable to it.
- Linked worktrees are identified only through the repository's worktree
  registration metadata.

Example: with `repository: /work/repo` and `branch: feature/auth`, the default
worktree path is `/work/repo.worktrees/feature-auth`.

### Add Operation

Rules:

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
- If `branch` does not exist, the add operation creates it at the commit
  resolved from `from`.
- If `branch` does not exist and `from` is omitted, the branch is created at
  the repository `HEAD` commit.
- `from` resolves in this order: commit hash, `refs/heads/<from>`,
  `refs/remotes/origin/<from>`, `refs/tags/<from>`.
- `from` is ignored when `branch` already exists.
- The add operation must not fetch, push, or contact any remote.

### Remove Operation

Rules:

- The target worktree is located by `path` when `path` is present, otherwise
  by the registered linked worktree of `branch`.
- If the target worktree is registered, the remove operation removes its
  directory and unregisters it.
- If the target registration is stale, the remove operation unregisters it,
  reports `removed` `true`, and does not check dirty state.
- If no target worktree is registered, the remove operation succeeds without
  removing anything.
- A worktree with uncommitted changes is removed only when `force` is `true`.
- The remove operation must refuse to remove the primary working tree.
- With `delete_branch: true`, the local branch is deleted after the worktree
  is removed.
- With `delete_branch: true`, the branch is deleted even when it is not merged.
- `delete_branch` must not delete a branch that is checked out in another
  worktree.
- With `delete_branch: true` and no registered worktree for the branch, the
  branch is still deleted when it exists.
- The remove operation must not fetch, push, or contact any remote.

### Result Document

Rules:

- A successful operation writes exactly one JSON object to stdout, on one
  line, followed by one newline.
- A failed operation writes no result document to stdout.
- Diagnostic text goes to stderr only.
- How later steps consume the result document is owned by the step output and
  value-resolution specs and is not defined by this spec.

`git.worktree_add` result fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `operation` | string | Always `worktree_add`. |
| `path` | string | The worktree path. |
| `branch` | string | The branch checked out in the worktree. |
| `commit` | string | `HEAD` commit hash of the worktree after the operation. |
| `created` | boolean | `true` when this run registered the worktree. |
| `branch_created` | boolean | `true` when this run created the branch. |

`git.worktree_remove` result fields:

| Field | Type | Meaning |
| --- | --- | --- |
| `operation` | string | Always `worktree_remove`. |
| `path` | string | The worktree path, empty when no worktree was registered and `path` was omitted. |
| `branch` | string | The `branch` input verbatim, empty when omitted. The removed worktree's checked-out branch is not derived or reported. |
| `removed` | boolean | `true` when this run removed a registered worktree. |
| `branch_deleted` | boolean | `true` when this run deleted the branch. |

### Lifecycle

Rules:

- A failed operation fails the step.
- If the add operation created the branch and a later part of the operation
  fails, the created branch may remain.
- A failed or interrupted add operation must not leave a registered worktree
  whose directory does not exist.
- The remove operation removes the worktree before deleting the branch.
- If branch deletion fails after the worktree was removed, the worktree
  removal is not rolled back and the step fails.
- Workflow abort and step timeout interrupt the operation and the step follows
  the abort or timeout behavior.

## Errors

### Validation Errors

Validation must fail when:

- The action name selects an operation other than `worktree_add` or
  `worktree_remove` in the `git.worktree*` name space.
- `repository` is missing, empty, or not a string.
- `git.worktree_add` has missing, empty, or non-string `branch`.
- `git.worktree_add` has empty or non-string `path` or `from`.
- `git.worktree_remove` has neither `branch` nor `path`.
- `git.worktree_remove` has empty or non-string `branch` or `path`.
- `git.worktree_remove` has `delete_branch: true` without `branch`.
- `force` or `delete_branch` is present and not a boolean.
- A `with` field is not listed for the selected operation.

Validation must not:

- Check whether `repository` exists.
- Check whether `branch`, `path`, or `from` can be resolved.

### Runtime Errors

The step must fail when:

- The repository path does not contain a Git repository.
- `git.worktree_add` cannot resolve `from` while the branch does not exist.
- In `git.worktree_add`, the worktree path exists and is not an empty
  directory and is not the registered worktree for `branch`.
- In `git.worktree_add`, `branch` is checked out in the primary working tree.
- In `git.worktree_add`, `branch` is registered to a linked worktree at a
  different path than the worktree path.
- In `git.worktree_add`, the registration for `branch` at the worktree path
  is stale.
- `git.worktree_remove` targets the primary working tree.
- `git.worktree_remove` receives both `branch` and `path`, at least one of
  them resolves to a registered linked worktree, and the registered worktree
  for `branch` is not the one at the resolved `path`.
- In `git.worktree_remove`, the target worktree has uncommitted changes and
  `force` is `false`.
- `delete_branch` is `true` and the branch is checked out in another worktree
  after removal.

## Examples

Each example assumes a fixture repository at `./repo` with an initial commit
on branch `main`, prepared by test setup.

### Add Creates Branch And Worktree

```yaml
steps:
  - id: create_worktree
    action: git.worktree_add
    with:
      repository: ./repo
      branch: feature-x
      path: ./wt/feature-x
```

Expected behavior:

- `./wt/feature-x` is a linked worktree of `./repo`.
- Branch `feature-x` exists and is checked out in `./wt/feature-x`.
- The branch points at the commit that `HEAD` of `./repo` pointed at.
- Stdout is one JSON line with `operation` `worktree_add`, `created` `true`,
  and `branch_created` `true`.

### Add Is Idempotent

```yaml
steps:
  - id: first
    action: git.worktree_add
    with:
      repository: ./repo
      branch: feature-x
      path: ./wt/feature-x

  - id: second
    depends: first
    action: git.worktree_add
    with:
      repository: ./repo
      branch: feature-x
      path: ./wt/feature-x
```

Expected behavior:

- Both steps succeed.
- `second` reports `created` `false` and `branch_created` `false`.
- Files created in `./wt/feature-x` between the two steps remain.

### Add From Start Point

```yaml
steps:
  - id: from_tag
    action: git.worktree_add
    with:
      repository: ./repo
      branch: hotfix
      from: v1.0.0
      path: ./wt/hotfix
```

Expected behavior:

- Branch `hotfix` is created at the commit tagged `v1.0.0`.
- `./wt/hotfix` checks out `hotfix`.

### Default Worktree Path

```yaml
steps:
  - id: default_path
    action: git.worktree_add
    with:
      repository: ./repo
      branch: feature/auth
```

Expected behavior:

- The worktree is created at `./repo.worktrees/feature-auth`.
- The result `path` is the resolved absolute form of that directory.

### Remove With Branch Delete

```yaml
steps:
  - id: create
    action: git.worktree_add
    with:
      repository: ./repo
      branch: short-lived
      path: ./wt/short-lived

  - id: remove
    depends: create
    action: git.worktree_remove
    with:
      repository: ./repo
      branch: short-lived
      delete_branch: true
```

Expected behavior:

- `./wt/short-lived` does not exist after `remove`.
- Branch `short-lived` does not exist after `remove`.
- `remove` reports `removed` `true` and `branch_deleted` `true`.

### Remove Is Idempotent

```yaml
steps:
  - id: remove_missing
    action: git.worktree_remove
    with:
      repository: ./repo
      branch: never-created
```

Expected behavior:

- The step succeeds.
- The result reports `removed` `false` and `branch_deleted` `false`.

### Dirty Remove Requires Force

```yaml
steps:
  - id: create
    action: git.worktree_add
    with:
      repository: ./repo
      branch: dirty
      path: ./wt/dirty

  - id: make_dirty
    depends: create
    run: touch ./wt/dirty/untracked-file

  - id: remove
    depends: make_dirty
    action: git.worktree_remove
    with:
      repository: ./repo
      branch: dirty
```

Expected behavior:

- `remove` fails.
- `./wt/dirty` still exists with `untracked-file`.
- The same remove with `force: true` succeeds and deletes the directory.

### Occupied Path Fails

```yaml
steps:
  - id: occupy
    run: mkdir -p ./wt/taken && touch ./wt/taken/file

  - id: add
    depends: occupy
    action: git.worktree_add
    with:
      repository: ./repo
      branch: taken
      path: ./wt/taken
```

Expected behavior:

- `add` fails.
- `./wt/taken/file` still exists.
- No worktree for `taken` is registered in `./repo`.
