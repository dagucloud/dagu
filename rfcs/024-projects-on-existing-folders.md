# RFC 024: Projects on Existing Folders

- **Status:** Proposed
- **Date:** 2026-11-23
- **Depends on:** [RFC 003](003-projects.md), [spec 001](../specs/001-project.md)
- **Affected areas:** `persis`, `cmd`, `service/scheduler`,
  `service/frontend`, `api/v1`, and `ui`

## Summary

Let an admin add a project whose DAG directory is an existing folder on the
server's machine, such as a repository checkout, instead of a directory
under `paths.projects_dir`. The folder holds `.dagu.json`, and the
project's state lives in the folder's `.dagu/`, so the CLI in the folder
and the server read one history. Ships after RFC 003's v3.0 core.

## Problem statement

RFC 003 puts every project's DAG files under `paths.projects_dir` and its
state under `paths.data_dir`. A repository that keeps its workflows next to
its code, as spec 001 describes, must then be copied or linked into
`paths.projects_dir`, and runs started from the checkout with the CLI keep
a history apart from the server's.

## Goals

1. Serve a spec 001 checkout as a server project without moving it.
2. Keep one run history, one set of secrets, and one scheduler per folder.
3. Never move, delete, or read beyond the folder's DAG files and `.dagu/`.

## Non-goals

- Folders on another machine than the one Dagu runs on.
- Choosing a folder with `permissions.write_dags` off.

## Design

### Creating

- An admin chooses the folder by its absolute path when creating a
  project, such as `/home/dagu/code/myapp`, `C:\code\myapp`, or
  `\\server\share\myapp`. A relative path, or a Windows path without a
  drive such as `\code\myapp`, is refused, since it would depend on the
  directory Dagu started in. A leading `~` expands to the home directory
  of the user Dagu runs as, and the UI shows the expanded path to confirm.
  The registry stores the path cleaned and absolute.
- The folder must hold `.dagu.json`, and Dagu reads its workflows and
  `base.yaml` as spec 001 says, so a repository's other YAML files, such
  as `docker-compose.yaml`, never become DAGs. For a folder without one,
  the UI offers to write one.
- An admin may choose any folder that Dagu can read and write: an admin
  who can edit DAGs can already run any command as Dagu's user, so a
  narrower rule would protect nothing. With `permissions.write_dags` off,
  choosing a folder would let an admin run any YAML on the machine, so
  only the default directory is offered.
- A filesystem root such as `/` or `C:\` is refused, and so is a folder
  inside, or containing, a folder another project uses,
  `paths.projects_dir`, `paths.dags_dir`, `paths.data_dir`, or the
  library. Either way one DAG would belong to two projects, or Dagu would
  read its own state as DAGs. Paths are compared as the operating system
  compares them.
- The UI suggests the folder's name as the display name, and a valid
  project name made from it, such as `my-app` for `My App`.

### State

- On the file backend, every project store lives in the folder's `.dagu/`,
  laid out as [spec 001](../specs/001-project.md#project-state) lays out a
  checkout's state. Dagu creates `.dagu/` with a `.gitignore` that ignores
  it. Runs, secrets, and profiles made in project mode before the folder
  was added are the project's from the start.
- `.dagu/` holds the project's run history and secrets, so `git clean -x`
  or a fresh clone deletes them, and Docker build contexts, archives, and
  copies of the folder carry them unless they exclude it. The UI says so
  before it creates the project.
- Secrets set in project mode are encrypted with the key of that user's
  Dagu home. When the server's key differs, the projects page lists the
  secrets it cannot decrypt, for an admin to enter again.
- On a backend that is not file-based, the project keeps its state in the
  backend like every other project, and the folder holds no `.dagu/`.

### CLI and other processes

- Dagu marks `.dagu/` with the project's ID and the ID of the Dagu home
  that serves it, kept in that home's `paths.data_dir`, so a home reached
  by another path, such as from inside a container, is still the same
  home.
- A command run for any other Dagu home, `dagu server` and
  `dagu start-all` included, refuses to write that state and names the
  Dagu home to use. One folder then never has two histories or two
  schedulers.
- A command that would write the project's state refuses when it runs as
  a user other than the owner of that state, and names the user to run as,
  such as with `sudo -u dagu`. On Windows, which gives what an elevated
  shell creates to `Administrators`, a member of that group also passes
  when the group owns the state.
- A Dagu process that cannot reach the folder at its path, such as a
  scheduler or coordinator on another host, lists the project as missing.

### Discovery and watching

- Discovery in the folder never enters a directory whose name starts with
  `.`, so it never reads `.dagu/` or `.git/`.
- The file watcher of RFC 003 covers the folder itself, without its
  subdirectories, and its workflow directory. Watching a whole repository,
  with its `node_modules/` or build output, would use up the Linux watch
  limit (`max_user_watches`).

### Lifecycle

- **Delete** removes the registry entry, grants, and binding, and asks
  whether to remove `.dagu/` too. The rest of the folder stays as it is.
- **Moved folder.** The project is listed as missing. An admin points it
  at the new path, and nothing else changes, since its state moved with
  the folder.
- **Lost state.** When the folder remains but its `.dagu/` is gone or names
  another project, the project is listed as missing its state and is not
  scheduled. Dagu does not create an empty `.dagu/` in its place, so the
  scheduler never starts over without its checkpoints and run history. An
  admin restores `.dagu/` from a backup, or starts the project again with
  empty state.

## Performance

The watcher adds two directories per folder project. Reads go to the
folder's `.dagu/` instead of `paths.data_dir`, at the same rate as any
other project. A folder on a network share makes every read of that
project slower.

## Verification and acceptance criteria

1. With default settings, an admin can create a project on any existing
   folder that holds `.dagu.json` and that Dagu can read and write. Dagu
   reads its DAGs as spec 001 says, keeps its state in the folder's
   `.dagu/`, and never moves or deletes the folder.
2. `dagu start` inside the folder shows and extends the same run history,
   and uses the same secrets and profiles, before and after the folder is
   added to a server.
3. A filesystem root, a folder without `.dagu.json`, a folder already used
   by a project, and a folder inside or containing another project's or
   Dagu's own directories are refused, and with `permissions.write_dags`
   off no existing folder is offered.
4. A relative path, or a Windows path without a drive, is refused. A
   leading `~` expands to the home directory of Dagu's user, and the
   registry stores the cleaned absolute path.
5. A command run for a Dagu home that does not serve the folder,
   `dagu server` and `dagu start-all` included, refuses to write its
   `.dagu/` and names the Dagu home that serves it.
6. A command run as a user other than the owner of the project's state
   refuses to write to it and names the user to run as.
7. When the server's key differs from the key the folder's secrets were
   set with, the projects page lists those secrets for an admin to enter
   again.
8. A folder whose `.dagu/` is gone or names another project is not
   scheduled, and Dagu does not recreate `.dagu/`, until an admin restores
   it or starts the project with empty state.

## Alternatives considered

- **State in `paths.data_dir`, as for other projects.** The CLI in the
  folder would keep a second history, and a scheduler started there would
  run each scheduled workflow again.
- **Link the folder into `paths.projects_dir`.** Its state would still live
  in `paths.data_dir`, with the same second history.
