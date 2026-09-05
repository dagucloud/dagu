# Spec: Git Checkout Action

## Status

Implemented.

This spec defines conformance behavior for the built-in `git.checkout`
action.

## Scope

This spec defines `git.checkout`, which clones a repository into a local
path if it does not yet exist there, or fetches and checks it out in
place if it does -- unlike [Spec 030: Git Worktree
Action](030-git-worktree-action.md), which manages linked worktrees
inside an existing repository.

This spec covers:

- the required `with.repository` and `with.path`
- `with.ref`: a branch, tag, or commit to check out; when unset,
  the repository's default HEAD is used
- clone-if-absent, fetch-and-checkout-if-present behavior at
  `with.path`, and the checkout result's `cloned` and `changed` fields
- `with.depth`: shallow clone/fetch depth
- `with.force`: whether local modifications at an existing target block
  a checkout
- authentication fields (`with.token`, `with.username`, `with.password`,
  `with.ssh_key_path`, `with.ssh_passphrase`) and the field combinations
  that are rejected
- that every configuration error this spec documents is rejected at
  DAG-build-time validation, not only at runtime
- validation and runtime errors

This spec does not define:

- live SSH or HTTPS token/password authentication against a real git
  server -- this spec exercises unauthenticated local-path repositories
  only
- linked worktree management (`git.worktree.add`, `git.worktree.remove`)
  -- see [Spec 030: Git Worktree Action](030-git-worktree-action.md)
- submodules, LFS, or sparse checkout

## Goal

Workflow authors bring a git repository to a known ref on the local
filesystem as a DAG step, whether that means an initial clone or
bringing an existing checkout up to date, without shelling out to `git`
directly.

## Behavior

### Clone or fetch

If `with.path` does not yet contain a repository, `git.checkout` clones
`with.repository` there (creating missing parent directories) and
checks out `with.ref`, or the default HEAD if `with.ref` is unset. If
`with.path` already contains a repository, it fetches from
`with.repository` instead of cloning, then checks out the same way. The
result reports `cloned` (true only on a fresh clone) and `changed` (true
when the checked-out commit differs from what was checked out before, or
on a fresh clone), along with the resolved `commit` hash.

`with.repository` accepts a local filesystem path as well as a remote
URL.

### Ref resolution

`with.ref` may be a branch name, a tag name, or a commit hash; it is
resolved in that order of preference. Omitting it checks out the
repository's default HEAD, re-resolved on every run -- so a later
`git.checkout` with no `with.ref` against an existing target picks up
new commits added upstream since the last run.

### Local modifications

Without `with.force`, checking out over a target whose worktree has
uncommitted local modifications fails rather than discarding them. With
`with.force: true`, local modifications are discarded and the checkout
proceeds.

### Shallow clone

`with.depth`, when greater than zero, limits how much history a fresh
clone (or a fetch against an existing shallow clone) retrieves.

### Authentication

Exactly one authentication method may be configured: `with.ssh_key_path`
(optionally with `with.ssh_passphrase`) for SSH, or `with.token`, or
`with.username`/`with.password` for HTTPS. `with.ssh_key_path` cannot be
combined with `with.token` or `with.password`; `with.token` cannot be
combined with `with.username` or `with.password`.

## Errors

### Validation

Every one of these is rejected at DAG-build-time validation (`dagu
validate`), not only when the step runs:

- `with.repository` missing: an error containing `"repository is
  required"`.
- `with.path` missing: an error containing `"path is required"`.
- `with.depth` negative: a schema `minimum` error.
- `with.ssh_key_path` combined with `with.token` or `with.password`: an
  error containing `"ssh_key_path cannot be combined with token or
  password"`.
- `with.token` combined with `with.username` or `with.password`: an
  error containing `"token cannot be combined with username/password"`.
- Any `with:` field git.checkout does not support (for example,
  `with.branch`, which belongs to `git.worktree.add`): an error
  containing `"invalid keys"`.

### Runtime

- `with.path` exists and is not a directory: an error containing `"not
  a directory"`.
- `with.path` exists, is a directory, is not a git repository, and is
  not empty: an error containing `"target directory is not a git
  repository and is not empty"`.
- `with.ref` does not resolve to a branch, tag, or commit in the
  repository: an error containing `"not found"`.
- `with.repository` cannot be cloned (does not exist, or is
  unreachable): an error containing `"clone failed"`.
- An existing target's worktree has local modifications and
  `with.force` is not set: an error containing `"unstaged changes"`.

## Related Specs

- Git worktree management: [Spec 030: Git Worktree
  Action](030-git-worktree-action.md)

## Examples

Clone a repository at a specific tag:

```yaml
steps:
  - action: git.checkout
    with:
      repository: https://github.com/example/example.git
      path: ./build/src
      ref: v1.4.0
      depth: 1
```

Bring an existing checkout up to date with its default branch,
discarding any local changes:

```yaml
steps:
  - action: git.checkout
    with:
      repository: https://github.com/example/example.git
      path: ./build/src
      force: true
```
