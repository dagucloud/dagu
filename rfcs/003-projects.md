# RFC 003: Replace Workspaces with Projects

- **Status:** Proposed
- **Date:** 2026-10-09, revised 2026-10-24, 2026-11-14, and 2026-11-23
- **Affected areas:** `workspace`, `auth`, `spec` (base config), `runtime`
  (`dag.run` resolution), `secret`, `profile`, notification routes, incident
  policies, wiki, `audit`, `gitsync`, `persis`, `cmn/crypto`, `cloudreport`,
  `service/scheduler`, `service/coordinator`, `service/mcp`, `api/v1`,
  `proto`, `ui`, and `cmn/config`

## Summary

Replace workspaces with projects in v3.0. A project is a directory of DAG
files with its own runs, logs, base config, secrets, profiles, notification
routes, incident policies, and wiki. Secrets, profiles, notification routes,
and incident policies also have a server layer that every project inherits,
as base config does. Users, API keys, authentication, and workers stay
server-wide. A project's DAG files live under `paths.projects_dir`, and its
state lives in the server's data directory. One server process serves every
project. Child DAGs that several projects call live in a library at
`paths.alt_dags_dir` and run in the project that calls them.

A project's name never changes; its display name can. Each project also has
an ID, so a project created again under a deleted one's name is a new
project. Dagu Cloud tells projects apart by an explicit binding, never by
name.

v3.0 moves each v2 workspace into a project on its first start.
`dagu migrate workspaces` previews that move and changes nothing. Installs
without workspaces run as the default project; the same first start moves
its state into `<data_dir>/projects/default/`, like every project's.

v3.0 ships the core, with checkouts. Moving DAGs between projects, adding a
folder outside `paths.projects_dir` as a project, the All projects view, and
Dagu Cloud bindings follow, as
[Compatibility and rollout](#compatibility-and-rollout) lists.

## Problem statement

Since v2.1.0, a workspace has scoped a group of DAGs across many subsystems:
access grants, base config, secrets, profiles, notification routes, incident
policies, wiki paths, and audit. Membership comes from the `workspace` label
on each DAG, and every workspace shares one set of stores. That design has
three problems.

1. **Every subsystem filters.** Each store, query, and API path checks the
   label. The API spec mentions workspaces over 300 times and the UI in over
   200 files.
2. **The boundary leaks.** Unlabelled DAGs are visible to every user. Child
   runs and distributed tasks must carry their parent's workspace to load the
   right base config (`base_config_workspace` on the coordinator task).
3. **The name is taken.** Dagu Cloud uses "workspace" for the account that
   servers connect to. Keeping both would give one word two levels in the same
   product.

Feedback in [#3288](https://github.com/dagucloud/dagu/issues/3288) shows that
multi-team installs depend on what workspaces provide: roles per group,
server-side visibility limits, and base config shared within a group. Removing
workspaces without a replacement would keep those installs on v2.

## Goals

1. Keep per-group roles, visibility, base config, secrets, notification
   routes, and incident policies.
2. Make the group a hard boundary: a DAG belongs to the project whose
   directory holds it.
3. Keep most subsystems unaware of projects. Only startup wiring, the API, MCP,
   and CLI edge, and authorization resolve a project.
4. Upgrade installs without workspaces with no manual step. Their DAG
   files, URLs, and commands stay the same; only their state moves.
5. Migrate workspaces on upgrade with no manual step, and never change
   silently which settings or secrets a workflow runs with.
6. Leave "workspace" to mean only the Dagu Cloud account.
7. Give each project an identity that Dagu Cloud can tell apart across
   servers, without putting Dagu Cloud in the run path.

## Non-goals

- A process per project.
- Users or authentication configured per project.
- `dag.run` across projects.
- Spreading projects across several scheduler processes.
- Renaming `internal/runtime/workspacebundle`, which packs directories for
  distributed runs and is unrelated.

## Projects

### Layout

```text
$DAGU_HOME/
  config.yaml            server-wide settings
  base.yaml              global base config
  dags/                  the default project's DAG files
  logs/admin/            admin and audit logs
  data/                  paths.data_dir
    projects.json        the project registry
    server/              the server layer and queues defined in config.yaml
    users/ apikeys/ auth/ ...  other server-wide state
    projects/
      default/           the default project's state
      weather/           project state
        runs/ logs/ artifacts/ settings/ state/
  projects/              paths.projects_dir
    weather/
      base.yaml          project base config
      *.yaml             DAG files
      wiki/              project wiki
```

A project has a DAG directory and a state directory.

- The DAG directory is `<projects_dir>/<name>/`. It holds the project's DAG
  files, its `base.yaml`, which is the project base config, not a DAG, and
  its wiki in `wiki/`, as the default project keeps its wiki in
  `dags_dir/wiki`. Wiki pages then stay in git with the DAGs they describe.
- `paths.projects_dir` defaults to `$DAGU_HOME/projects`, or to `projects`
  beside `dags` in the XDG config directory when `DAGU_HOME` is unset.
  Pointing it at a git checkout keeps every project's DAG files and base
  config in git.
- The state directory is `<data_dir>/projects/<name>/`, laid out as
  [spec 001](../specs/001-project.md#project-state) lays out project state.
  It holds everything else the project has: runs, logs, artifacts,
  secrets, profiles, and the rest of [Scope](#scope). State never lives in
  a DAG directory, so a checkout stays clean.
- When `config.yaml` sets `paths.log_dir` or `paths.artifact_dir`, a
  project's logs or artifacts live there instead, in `.projects/<name>/`.
  A volume set aside for logs or artifacts then holds every project's, and
  migration moves them within that volume.
- `paths.data_dir` itself holds only server-wide state: the registry in
  `projects.json`, the server layer and the queues defined in
  `config.yaml` in `server/`, and the rest of the Server row of
  [Scope](#scope). Every project's state, the default project's included,
  is under `projects/`.
- `dag_discovery` settings in `config.yaml` apply to every project's DAG
  directory, as they do to the default project. Discovery never follows a
  symbolic link out of a project's DAG directory, whatever
  `dag_discovery.symlinks` says, so one project never reads another's DAGs.
- When one of `paths.dags_dir`, `paths.projects_dir`, and
  `paths.alt_dags_dir` lies inside another, discovery in the outer one
  skips the inner one, so each DAG file belongs to one project or the
  library.
- The default project's DAG directory is `paths.dags_dir`, and its wiki
  stays in `dags_dir/wiki`. Its DAG files, names, and URLs do not change;
  its state lives in `<data_dir>/projects/default/` like every project's.
- Settings that place one per-project store, such as `paths.dag_runs_dir`,
  `paths.queue_dir`, or `paths.suspend_flags_dir`, only tell migration
  where v2 kept that data. v3.0 warns about them and does not use them
  afterwards.
- Project names follow [Names](#names).
- The server knows its projects only from the registry in
  `paths.data_dir` ([Identity and references](#identity-and-references)).
  A directory under `paths.projects_dir` is a project's DAG directory only
  when a project of that name is registered, so a checkout's `scripts/`,
  `docs/`, or `.git/` never becomes one. `dagu.json` is optional in a
  project's DAG directory; see [Checkouts](#checkouts).

### Names

A project name is a directory name, so it must work as one on macOS,
Windows, and Linux.

- It matches `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`. Run files nest several
  levels below the project directory, and many Windows tools stop at 260
  characters in a path. A name never starts with `-`, so `--project` never
  reads it as a flag, or with `_`, which API paths keep for their own
  segments, such as `_global`.
- It is not a Windows device name: `con`, `prn`, `aux`, `nul`, `com0` to
  `com9`, or `lpt0` to `lpt9`, in any case.
- `default` is reserved, in any case.
- Names are unique without regard to case, because macOS and Windows compare
  file names that way and Linux does not. A request that names a project in
  another case resolves to the stored spelling, so every path uses one
  spelling.
- A name never changes. Commands, scripts, API clients, links,
  `project_mappings`, and directory paths all carry it, and a rename would
  break each of them.

A project also has a display name: any text up to 200 characters. It is
the name until an admin sets one, and an admin can change it at any time.
Pages show the display name; commands, API calls, and links use the name.

### Scope

| Scope | Contents |
| --- | --- |
| Server | `config.yaml`, global `base.yaml`, the [server layer](#server-layer) of secrets, profiles, notification routes, and incident policies, library DAG files (`paths.alt_dags_dir`), business calendars, the project registry, v2 webhook URLs that migration kept, users and their grants, API keys, OIDC and proxy authentication, the encryption key, license, upgrade check, Dagu Cloud connection with its report and remote access state, remote nodes, coordinator and worker state (dispatch tasks, leases, active distributed runs, worker heartbeats), service registry, the scheduler lock, pause flag, and restart backoff, audit log, admin logs, queues defined in `config.yaml` |
| Project | DAG files, project `base.yaml`, runs, logs, artifacts, run work directories, process heartbeats, run events, DAG state, suspend flags, DAG settings, pins, views, scheduler state and checkpoints, watch state, slot index, backfills, materializations, agent session cleanups, secrets, profiles, notification routes, incident policies and incidents, wiki, per-DAG webhooks and their public URLs, node inventories ([RFC 012](012-node-inventory.md)), other queues |

A store added later is per project unless it holds identity, authentication,
licensing, or the state of the processes and workers that serve every
project.

### Storage

```text
request, scheduler unit, or task ──▶ project ID ──▶ that project's stores
                                                     (file backend: the
                                                      project's directories)
```

- A project's stores are the stores the default project uses today, opened
  on the project's paths. Store contracts take no project argument. The API,
  MCP, and CLI edge, the scheduler supervisor, and the coordinator choose
  which project's stores to use. A backend that is not file-based keys its
  records by project ID instead.
- **Encryption key.** Every project encrypts its secrets with the server's
  key: `DAGU_ENCRYPTION_KEY`, or `auth/encryption_key` in `paths.data_dir`.
  A project never creates its own key, so migrated secrets decrypt
  unchanged. A project directory copied to another server decrypts there
  only with the same key.
- **Paths in run records.** Run records store the paths of logs, step
  output, and artifacts inside the project's state directory relative to
  it, so moving a DAG leaves them valid. Records written before v3.0 hold
  absolute paths, and migration does not rewrite them: a project maps a
  path under its v2 directories to the new place when it reads the record.
  A later move of the DAG rewrites them. A path a DAG sets outside the
  project's directories stays absolute and does not move.
- **Reads across projects.** Most pages show one project. Search and the
  All projects view read the stores of each project the user can see, a few
  at a time, and merge the results. Their cost grows with the number of
  projects.

### Base config

A DAG's base config is the global `base.yaml`, then its project's `base.yaml`,
then the DAG file. The order matches v2's global, workspace, DAG. A child
started with `dag.run` runs in its caller's project, so it gets that project's
base config without carrying provenance. This holds whether the child is in
the caller's file, in its own file, or in the library.

### Server layer

Secrets, profiles, notification routes, and incident policies have a server
layer that every project inherits, as v2 workspaces inherit global entries.
A credential or alert route that every team uses is kept once, not copied
into each project.

| Setting | A project's DAG uses |
| --- | --- |
| Secret | The project's secret of that name, else the server's |
| Profile | The server's defaults, then the project's defaults, then the selected profile. A profile named by a run resolves in the project, then the server |
| Notification routes | The project's routes, and the server's when the project's route set inherits them |
| Incident policies | The DAG's policy, then the project's, then the server's, each inheriting its parent as in v2 |

- Editing the server layer needs a server-wide role, as editing global
  entries does in v2.
- Every project's DAG authors can read server-layer secrets, since a DAG
  can print what it receives, as in v2. The server layer holds only
  credentials that every project may use.
- Project pages list inherited entries read-only, marked as coming from the
  server. Secret values are never shown either way.
- The server layer is stored in `<data_dir>/server/`, apart from every
  project's own entries, the default project's included.

### Identity and references

Each project has an ID, a random UUID assigned when the project is created. A
project deleted and created again under the same name
gets a new ID, so its grants, audit trail, and Dagu Cloud history never mix
with the old one's.

- A server-wide registry in `paths.data_dir` holds each project's ID, name,
  display name, creation time, and
  [Dagu Cloud binding](002-dagu-cloud-connector.md#binding). It is the only
  list of projects: creating, deleting, and migration change it, and
  nothing on disk adds to it. Every write to it holds one lock and checks
  the name rules before it stores. Writes are rare, so one lock costs
  nothing.
- Records that outlive a request refer to a project by ID: grants, audit
  entries, dispatch tasks, leases, and status reports. Names are for people:
  `--project`, the API's `project` parameter, and `project_mappings` take a
  name and resolve it to an ID.
- The default project gets its ID at the first v3.0 start.
- A DAG is identified by its project and name. Names are unique within a
  project.
- `dag.run` resolves its target in the caller's project, then in the
  library. A path target must lie inside the caller's project DAG directory
  or the library once symbolic links resolve. A target in another project
  is a load error.
- A CLI command addresses the project named by `--project`, else by
  `DAGU_PROJECT`, else the server project whose DAG directory contains the
  current directory, else the project named by the `dagu.json` found from
  it, else `default`, as
  [Checkouts](#checkouts) details.
- API paths and MCP tools that address DAGs, runs, secrets, profiles, routes,
  policies, or wiki pages take `project`. The default is `default`, so
  existing clients keep working against the default project. A request for
  a DAG the project lacks fails, and the error names the projects the
  caller can see that have a DAG of that name.
- A DAG in a subdirectory of its project, such as `etl/daily.yaml`, is
  named `etl/daily`, as v2 recursive discovery and spec 001 name it. API
  paths take such a name as one segment, with `/` encoded as `%2F`.
- A per-DAG webhook URL is `/webhooks/{project}/{name}`: the first segment
  after `/webhooks/` is the project, and the rest, `/` included, is the
  DAG, so `/webhooks/billing/etl/daily` reaches `etl/daily` in `billing`.
  The URL selects the DAG, and the webhook's token or signature
  authenticates the request, as in v2.
- `/webhooks/{name}` reaches the DAG that migration moved to a project
  under that name while it exists, so callers outside Dagu need no change,
  and otherwise the default project's DAG, as in v2. A v2 server has one
  DAG of each name, so each such URL reaches one DAG. It matches only a
  name without `/`, which every v2 webhook URL has. A default-project DAG
  in a subdirectory uses `/webhooks/default/{name}`.
- `/projects` lists and creates projects. The `/workspaces*`,
  `/settings/workspaces/*`, `/notification-routes/workspaces/*`,
  `/incident-policies/workspaces/*`, and `/profiles/_workspaces/*` paths and
  the `?workspace=` parameter are removed.
- Local runs start their child process with `--project`. `DAGU_HOME` stays the
  server root, so the child still reads `config.yaml` and the global base.
- Audit entries record the project's ID and its name at the time. Old
  entries keep `workspace`.

### Checkouts

[Spec 001](../specs/001-project.md) calls a directory that holds
`dagu.json` a project and runs its workflows from the CLI. That is the same
project seen from a checkout, not a second concept. Checkouts ship with spec
001 in v3.0.

- A project's DAG directory may hold `dagu.json`. The server then discovers
  workflows, applies base config, and sets working directories as spec 001
  says, whatever `dag_discovery` says, so one checkout behaves the same on a
  laptop and as a server project.
- A path in `dagu.json` that leads out of the project's DAG directory, such
  as `../billing`, is a load error, so one project never reads another's
  DAGs.
- The `project` field of `dagu.json` names the project. Under
  `paths.projects_dir`, a `project` that differs from the directory's name
  is a warning on the projects page, not a load error.
- [Spec 001](../specs/001-project.md#server-projects) defines which project
  a command addresses: `--project`, then `DAGU_PROJECT`, then the server
  project whose DAG directory contains the current directory, with or
  without `dagu.json`, then the project named by the `dagu.json` found from
  it, then `default`.
- A command keeps a project's state in `<data_dir>/projects/<name>/` of its
  Dagu home and writes nothing inside the folder. A folder whose
  `dagu.json` names an existing project, from anywhere on the machine,
  shares that project's runs, settings, and secrets, as two clones of one
  repository do. On a server this needs Dagu's user and the server's Dagu
  home, as running a v2 workspace DAG from the CLI does, so it opens no
  new access.
- State that no project owns, such as runs from a folder whose `dagu.json`
  names no project of the server, is listed on the projects page, where an
  admin can delete it. A project created under that name takes it over.

### Library

Child DAGs that several projects call live in a library at
`paths.alt_dags_dir`. The library belongs to no project.

```text
projects/intraday    intraday DAG  ──dag.run──┐
                                              ├──▶ library: facilities/ABC
projects/day-ahead   day-ahead DAG ──dag.run──┘
                     each run is recorded in its caller's project
                     and uses that project's base config
```

- Library DAGs are not listed among a project's DAGs or scheduled on their
  own. Validation warns when one has a `schedule`.
- A library DAG runs in the project that starts it: the caller's project for
  `dag.run`, or the project given to an explicit start. Its run is recorded
  there, and it uses that project's base config, secrets, and profiles.
- A project keeps a library DAG's runs and DAG state apart from its own
  DAGs', so a project DAG added later under the same name starts without
  the library DAG's history.
- A project DAG shadows a library DAG of the same name. `dag.run` finds the
  project's DAG, and an explicit start of the library DAG in that project
  is refused, since the name there addresses the project's own DAG.
- A library DAG that sets no `working_dir` runs in its file's directory,
  as in v2, whichever project calls it.
- A `dag.run` inside a library DAG resolves in the same project, then the
  library.
- A library run is visible only to users with a role in the project that ran
  it. Library definitions are readable through every project.
- Every project can call the library. A library call is not a cross-project
  call.
- A Library page lists the library DAGs, and every user can read them. Only
  admins change them through Dagu, because a library DAG runs with the
  secrets of every project that calls it. On disk, [Access](#access)
  applies.

### Access

- A grant is a user or group, a project, and a role below `admin`. A
  server-wide role applies to every project, and a user or API key may have
  none.
- A user's role in a project is the higher of the server-wide role and the
  project grant.
- A user with neither gets `404` for the project's DAGs, runs, logs, and wiki
  pages. The project does not appear in lists or search.
- `admin` is server-wide only.
- API keys stay server-wide, since a request names its key before its
  project. Grants limit a key to projects, as they limit a user. A user
  with the `manager` role in a project creates, edits, and deletes keys
  whose grants all name projects where that user is `manager`. A key with
  a server-wide role, or a grant on another project, needs an admin.
- Projects separate people who work through the UI, API, MCP, and CLI. They
  do not separate people who can write DAG files. Whoever can change a
  project's DAG directory can run code with that project's secrets, and
  whoever can change the library can run code with the secrets of every
  project that calls it. A multi-team install therefore gives each project
  its own repository, or limits each project's directory to its team with
  code owners, and limits `paths.alt_dags_dir` to admins.
- Only admins grant users and groups roles in a project, since users are
  server-wide.
- OIDC and proxy `project_mappings` and `default_project_access` replace
  `workspace_mappings` and `default_workspace_access` with the same shape.
  v3.0 reads the old keys with a warning. v3.1 drops them.

### Lifecycle

- **Create.** An admin creates a project in the UI or API, or with `dagu
  project create`. The registry assigns its ID, the scheduler starts its
  unit without a restart, and Dagu uses `<projects_dir>/<name>/` as its DAG
  directory, creating it when it does not exist. A directory that git
  already brought, such as from a checkout used as `paths.projects_dir`,
  becomes a project only when an admin creates a project of its name.
- **No rename.** A project's name never changes; see [Names](#names). An
  admin changes the display name in the UI or API at any time, and only
  the registry changes.
- **Delete.** An admin deletes a project in the UI or API, or with `dagu
  project delete`. Delete is refused while the project has runs queued,
  in its own queues or a server-wide one, running, or waiting. The
  scheduler unit, the coordinator, and the server stop using the
  project's stores first, since Windows cannot remove a directory with
  open files. Delete then removes the DAG directory, the state directory,
  the registry entry, grants, and Dagu Cloud binding.
  - When the DAG directory is in a git checkout, delete is refused while
    the directory exists: the checkout owns it, so it is removed there
    first.
- **Changed on disk.** A project whose DAG directory disappears outside
  Dagu is listed as missing and is not scheduled. It keeps its registry
  entry, grants, binding, and state until the directory returns or an
  admin deletes it. A directory renamed outside Dagu is not a project until
  an admin creates one of its new name, since names never change.
- **The default project** cannot be deleted. Its display name can change.

### Moving a DAG

Teams reorganize, so DAGs move between projects. In v2 a label change moved
a DAG and kept its history; a move between projects keeps it too. The move
ships after v3.0. Until then, a DAG file moved by hand starts over in its
new project, and its runs stay in the old one.

- An admin, or a user with the `manager` role in both projects, moves a DAG
  in the UI or API, or with `dagu move`. Its file, runs, logs, artifacts,
  DAG state, suspend flag, settings, and per-DAG webhooks move with it. Its
  webhook gets the target project's URL, and the old URL stops working;
  the move shows both before it starts, so callers can switch. Runs in
  which it was a child stay with their parent runs.
- The target project's users then see the DAG's past runs and logs, which
  can hold data the source project never shared. The move asks to confirm
  that before it starts.
- Before anything moves, the move lists the secrets, profiles, and
  notification routes the DAG uses that the target project lacks, and the
  nodes its literal node selectors ([RFC 013](013-node-targeting.md)) match
  in each project, so what the DAG runs with never changes silently.
- The source project's scheduler unit stops scheduling the DAG before its
  files move, and the target's unit picks it up after.
- The move is refused while a DAG in the source project calls it with
  `dag.run`, since that call would become a load error. It is also refused
  while the DAG has runs queued, running, or waiting, and when the target
  project already has a DAG of that name.
- When git moves a DAG file from one project's directory to another's, the
  projects page offers the same move for its history.

### UI

- The workspace selector becomes a project selector. It lists the projects
  the user can see by display name, and appears only when the user can see
  more than one. A single-project install looks as it does today.
- After v3.0, the selector also offers All projects for the run list,
  failures, and the dashboard, so a person on call for several projects
  finds every failure in one place. It shows only the projects the user can
  see.
- Project IDs never appear in the UI. The name appears only where people
  type it: CLI hints, `project_mappings`, and links.
- Creating a project asks for a display name and suggests a name from it,
  which the admin can change. When the display name has no ASCII letters or
  digits, such as `天気予報`, the admin types the name. The dialog says that
  the name cannot change later.
- The access editor, the base config page, and the pages for secrets,
  profiles, routes, and policies work per project.
- The projects page shows admins missing projects.

## Scheduler

One scheduler process serves every project.

```text
scheduler process   HA lock, health server, service registry, signals, license
 └─ supervisor      starts and stops units as projects appear and disappear
     ├─ unit: default
     ├─ unit: weather
     └─ unit: sim
          entry reader, tick planner, queue processor, retry scanner,
          zombie detector, notification and incident monitors
```

- A unit is the current per-home scheduler, built from one project's stores.
  It does not know about other projects.
- The HA lock, health server, and service registration move out of the unit
  and exist once per process. Kept per unit, they would give each project its
  own lock and collide on the health port.
- The pause flag and restart backoff also belong to the process. Pausing the
  scheduler pauses every unit.
- The supervisor owns each queue defined in `config.yaml`. Items from every
  project wait in one server-wide store in `<data_dir>/server/`, and the
  supervisor admits them
  oldest first under the queue's limit, as v2's single queue did, then
  hands each run to its project's unit. Other queues are per project, and
  their unit processes them.
- One file watcher covers `paths.projects_dir` and routes events by path.
  A watcher per project would reach the Linux inotify instance limit (128
  by default) on servers with many projects.

## Distributed execution

- Dispatch tasks, leases, and status reports carry the project ID. The
  coordinator records attempts in that project's run store.
- Tasks already carry base config content and the profile name. Both now come
  from the task's project. Profile and secret values resolve from the task's
  project, then the server layer.
- `base_config_workspace` is removed from the task.
- Workers stay shared and do not read project directories. A `dag.run` inside
  a dispatched run fetches its child from the coordinator, which resolves it
  in the task's project, then the library.
- Every coordinator call that reads or writes a project's state carries
  the project ID: fetching a DAG, resolving a secret or profile, DAG state,
  run status and cancel requests, log and artifact streams, and agent
  session cleanups. A call without one addresses the default project, and
  the coordinator refuses it for a task of another project, so a project
  run never gets another project's secret.
- A worker sees a project's files only through a step's `dependencies`
  ([spec 035](../specs/035-file-dependencies.md)), which resolve from the
  DAG's working directory. For a checkout, that is the directory spec 001
  sets.
- A worker's own `dags_dir` and `alt_dags_dir` serve only default-project
  tasks, so a project run never picks up a default-project DAG with the same
  name.
- Coordinators and workers older than v3.0 cannot place a task in a
  project. A v3.0 coordinator gives an older worker only default-project
  tasks, and a v3.0 worker does not connect to an older coordinator; each
  says why in its log and on the workers page. Upgrade the coordinator and
  server first, then the workers. Project runs wait in their queues until
  an upgraded worker connects, while default-project runs keep running on
  older workers.

## Dagu Cloud

Workflows never depend on Dagu Cloud
([RFC 002](002-dagu-cloud-connector.md)), and projects keep it that way.
[RFC 002](002-dagu-cloud-connector.md#projects) specifies how a connected
server reports its projects and binds them; this RFC fixes what that needs:

- Reports identify a project by its ID, never its name. From v3.0, report
  protocol 3 names the project of each run, so `etl` in two projects is two
  workflows in Dagu Cloud.
- Inventory sync and deploys reach a project only through an explicit
  [binding](002-dagu-cloud-connector.md#binding), never by matching names.
- The registry stores the binding, and [Lifecycle](#lifecycle) says what
  delete and changes on disk do to it.

## Migration

v3.0 moves each workspace into a project on its first start, before it
schedules or serves anything. `dagu migrate workspaces` prints what that
start would do, including each item that needs attention, and changes
nothing, so it can run against a live v2 install,
or a copy of one, before the upgrade.

- Stop every v2 process before the first v3.0 start. The first v3.0
  process to start for a `DAGU_HOME` migrates under a lock in
  `paths.data_dir`, and other v3.0 processes wait for it.
- Downgrading after migration is not supported. Back up `paths.data_dir`
  and the DAG directories before the upgrade, and restore them to go back
  to v2.
- Migration waits while a v2 scheduler or coordinator is in the service
  registry, or a run has a live process heartbeat, and logs what it waits
  for. It never moves the files of a run that is still running.
- A process killed during migration resumes it on its next start. The
  health endpoint stays up and reports progress, so orchestrators do not
  restart a large install midway.
- When it finishes, the server logs a report, and the projects page shows
  it to admins: what moved, and each item below that needs attention.
- Every install migrates the default project, with or without workspaces:
  its runs, DAG state, queues, suspend flags, DAG settings, and the rest of
  its state move from v2's directories in `paths.data_dir`, and its logs
  from the default log directory, into `<data_dir>/projects/default/`.
  Its DAG files stay in `paths.dags_dir`. Tools outside Dagu that read v2's
  paths, such as log shippers and backup scripts, need the new ones; the
  preview lists each path that moves.

| v2 | v3.0 |
| --- | --- |
| Workspace `X` | Project `X` in the registry, with a new ID |
| DAG labelled `workspace=X` | `<projects_dir>/X/`, with its runs, logs, artifacts, DAG state, suspend flags, and DAG settings in `<data_dir>/projects/X/` |
| DAG or base file already at its v3 path | Left in place. Its runs, logs, and state still move |
| DAG without a `workspace` label | Stays in the default project. Its file stays in `paths.dags_dir`; its runs, logs, and state move to `<data_dir>/projects/default/` |
| DAG under `paths.alt_dags_dir` | Stays in place as a library DAG |
| `<dags>/workspaces/X/base.yaml` | `<projects_dir>/X/base.yaml` |
| Secrets and profile entries of `X` | Project `X`'s secrets and profiles, still encrypted with the server's key |
| Notification routes and incident policies of `X` | Project `X`'s routes and policies |
| Global secrets, profile defaults, notification routes, and incident policies | The server layer in `<data_dir>/server/`, which every project inherits |
| Per-DAG wiki directory `<wiki>/X/<dag>`, pages under `<wiki>/X/` | Project `X`'s wiki in `<projects_dir>/X/wiki/` |
| User or API key with roles in `X` | Grants on project `X` |
| User or API key with `workspace_access.all: false` | No server-wide role, and `viewer` on the default project, which keeps v2 read access to unlabelled DAGs |
| `workspace_mappings`, `default_workspace_access` | Read as `project_mappings`, `default_project_access` until v3.1 |
| `workspace` label on a moved DAG | Kept as an ordinary label |

- Per-DAG webhook URLs keep their paths and tokens, and reach the DAG in its
  new project, as [Identity and references](#identity-and-references)
  describes.
- Migration moves a file only while it is still at its v2 path, and never
  moves or writes a file inside a git checkout, since the next pull would
  undo the move and git would not track the new file. Such a DAG is held
  until its file is moved in git to `<projects_dir>/X/`: no project lists,
  schedules, or starts it. A held base file holds its project's DAGs the
  same way, so none runs without its base config. Runs and state move at
  once.
- From v2.28, v2 reads DAG files and `base.yaml` in `<projects_dir>/X/` as
  workspace `X`'s, so operators who keep DAG files in git move them there
  before the upgrade, and nothing is held. v2 skips and logs a file whose
  DAG name another v2 DAG already uses.
- Before it copies a directory to another file system, migration checks
  free space there. When there is not enough, it stops before moving
  anything and says how much it needs.
- Migration keeps the directory each moved DAG runs in. On v2, a DAG that
  sets no `working_dir` runs in its file's directory, and a relative
  `working_dir` resolves from there. Migration writes the directory the DAG
  ran in on v2, as an absolute `working_dir`, into each moved file that
  sets none or a relative one, and changes nothing else in the file. Paths
  that resolve from the working directory, such as `dependencies`
  ([spec 035](../specs/035-file-dependencies.md)), then resolve as on v2.

The report lists these items, which migration cannot settle alone:

- A DAG file, base file, or wiki page that a git checkout holds or would
  receive, as above. When such a DAG sets no `working_dir`, or a relative
  one, the report names the directory to set before the file moves, since
  migration does not edit it.
- A DAG or base file at both its v2 and v3 paths. The v3 file is used, and
  the v2 file is held, as above.
- A moved DAG whose v2 working directory one absolute `working_dir`
  cannot keep, such as one whose `working_dir` contains a variable. It is
  left as it is.
- A workspace name that breaks the [name rules](#names), including
  `default` and two names that differ only in case. The project gets a
  valid name made from it, as the UI suggests one, and keeps the workspace
  name as its display name. When that name is taken, a number is added to
  it. The preview shows these names before the upgrade.
- A `dag.run` target that lands in another project. The step fails at run
  time with a load error that names both projects. Moving the target into
  the library fixes it.
- A queue not defined in `config.yaml` that DAGs in different projects share.
  Each project then limits it on its own; defining it in `config.yaml`
  keeps one limit.
- Each library DAG and its static callers. In v2, a child in its own file
  loads the base config of its own `workspace` label, so a library DAG called
  from workspace `X` did not load `X`'s base config. In v3, it gets the
  caller's project base config.
- A DAG under `paths.alt_dags_dir` that v2 scheduled, because
  `alt_dags_dir` lay inside `dags_dir`. v3 treats it as a library DAG and
  does not schedule it; moving its file into a project schedules it again.
- Each `dag.run` target that contains a variable, such as
  `${JOBS_ROOT}/facilities/${ITEM}`, since migration cannot resolve it. At
  run time, a target found in neither the caller's project nor the library
  fails the step with a load error that names both.

```text
dagu: moved 2 workspaces into projects
  billing        12 DAGs, base config, 3 secrets, 1 incident policy
  ops            4 DAGs; workspace "-ops", which is not a valid name
needs attention:
  git checkout   dags/etl.yaml is held until moved to projects/billing/
  dag.run        billing/report calls sim/score in another project
```

## Performance

Cost grows with the number of projects, so an idle project adds little.

- **Periodic tasks.** Each unit runs the current scheduler's periodic
  tasks, such as the tick planner, retry scanner, and zombie detector, on
  its own timers ([Scheduler](#scheduler)). A waiting timer costs nothing;
  the cost is each tick's reads of the project's stores. At the current
  intervals, an idle project's queue processor and retry scanner each read
  its stores about every 30 seconds, so 100 idle projects add about seven
  such reads a second.
- **Memory.** Each project's caches keyed by DAG or run keep the bounds
  the default project's caches have today, so each project adds at most
  one set of those bounds.
- **SSE.** Topic keys gain the project ID, so `etl` in two projects never
  shares a topic, and payloads keep their size. A page subscribes only to
  the topics of the project it shows.
- **File watching.** One watcher covers every project
  ([Scheduler](#scheduler)).
- **Reads across projects.** Search and the All projects view cost one
  index read per visible project ([Storage](#storage)).
- **Migration.** Run, log, and state directories move by rename, on every
  install, since the default project's state moves too. A directory on
  another file system, such as one that `paths.dag_runs_dir` placed, is
  copied. No run record is rewritten
  ([Storage](#storage)), so the first start costs one rename per moved
  directory, not one write per stored run.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.22 | Warnings and an admin notice that list workspace settings in use. |
| v2.28 | The warning and notice say that v3.0 moves workspaces into projects on its first start, and stop asking operators to move settings by hand. v2 reads `<projects_dir>/X/` as workspace `X`, so DAG files kept in git can move there before the upgrade. |
| v3.0 | Projects with IDs, names, and display names; the server layer; store scopes and relative run paths; the default project's state in `projects/default/`; create and delete; migration on first start, with its preview `dagu migrate workspaces`; report protocol 3 (RFC 002); checkouts, with spec 001. |
| v3.1 | Drops the `workspace_mappings` and `default_workspace_access` aliases. |
| After v3.0 | Moving DAGs between projects; adding a folder outside `paths.projects_dir` as a project; the All projects view; Dagu Cloud bindings (RFC 002). |

v2 gets no new workspace features beyond the v2.28 upgrade aids above.

These specs define workspace behavior, and v3.0 rewrites them for projects:

- [Spec 020](../specs/020-mcp-server.md) and
  [spec 021](../specs/021-mcp-read-tool.md): the `workspace` parameter
  becomes `project`, and wiki URIs become `dagu://wiki/{project}` and
  `dagu://wiki/{project}/{path}`.
- [Spec 034](../specs/034-wiki-page-format.md): a wiki link resolves in the
  page's project.
- [Spec 070](../specs/070-runtime-profiles.md): workspace profile defaults
  become the project's, applied to every DAG of the project instead of by
  `workspace` label.

## Verification and acceptance criteria

Criteria 23 and 24 apply from the release that ships the feature.

1. A v2 data directory with no workspaces starts on v3.0 as the default
   project, with no manual step. Its DAG files and URLs do not change, and
   its runs, logs, and settings move to `<data_dir>/projects/default/` and
   open as before.
2. After migration, each DAG outside the library runs with the same base
   config, secrets, profile, notification routes, and incident policies, and
   in the same working directory, as on v2. The report lists every library
   DAG whose base config changes.
3. After migration, each user and API key can see and do what it could on v2,
   and no more.
4. A user without a role in a project gets `404` for its DAGs, runs, logs, and
   wiki pages, and does not see the project in lists or search.
5. `dag.run` to a DAG in another project fails at load.
6. One scheduler process schedules every project. A newly created project
   starts scheduling without a restart.
7. A distributed run of a project DAG records its status in that project.
8. The first v3.0 start migrates every workspace with no manual step,
   waits while a v2 process or a run is still live, resumes after being
   killed, keeps its health endpoint up, and reports every item that needs
   attention.
9. `dagu migrate workspaces` changes nothing on disk and shows the names,
   display names, and moves that the first start would make, and every item
   its report would list as needing attention.
10. A library DAG called from project X runs with X's base config, secrets,
    and profiles, and only users with a role in X see its run.
11. Library DAGs are not listed among a project's DAGs or scheduled.
12. A `dag.run` inside a distributed run resolves only in the task's project
    and the library.
13. A project's DAG files, base config, and wiki live in
    `<projects_dir>/<name>/`, and its state lives in
    `<data_dir>/projects/<name>/`, never in `paths.projects_dir`. Outside
    `projects/`, `paths.data_dir` holds only server-wide state. With
    `paths.log_dir` or `paths.artifact_dir` set, its logs or artifacts live
    there, in `.projects/<name>/`.
14. Migration leaves DAG files already at their v3 path in place, never
    moves a file inside a git checkout or writes one into a checkout, and
    still moves their runs and state.
15. A project name that breaks the name rules is refused at creation.
16. No UI, API, or CLI path changes a project's name. Delete is refused
    while the project has runs queued, running, or waiting, and for the
    default project.
17. A project directory that disappears outside Dagu is listed as missing
    and not scheduled, and keeps its grants and state until the directory
    returns or an admin deletes it.
18. Every project's secrets decrypt with the server's key, and no project
    directory holds a key of its own.
19. A project DAG uses the server layer's secrets, profile defaults,
    notification routes, and incident policies unless its project overrides
    them, as a v2 workspace DAG uses global entries.
20. A display name takes any text up to 200 characters, and changing it
    moves nothing.
21. A user who can see one project sees no project selector.
22. Every user can read library DAGs on the Library page, and only admins
    change them.
23. Moving a DAG to another project keeps its runs, logs, and state, and
    shows its old and new webhook URLs. It lists what the target project
    lacks before it moves, and is refused while a `dag.run` caller remains
    in the source project.
24. All projects shows the runs and failures of every project the user can
    see, and of no other.
25. A `dagu` command run anywhere inside a project's DAG directory, with or
    without `dagu.json`, and without `--project` or `DAGU_PROJECT`,
    addresses that project.
26. After migration or a move, the logs, step output, and artifacts of every
    moved run open as before.
27. A `dagu.json` path that leads out of its project's DAG directory is a
    load error.
28. A v3.0 coordinator gives an older worker only default-project tasks,
    and both say why it gets no project task.
29. A `dagu.json` under `paths.projects_dir` whose `project` differs from
    its directory's name loads, with a warning on the projects page.
30. `/webhooks/{project}/{name}` reaches that project's DAG, including a
    DAG in a subdirectory, such as `/webhooks/billing/etl/daily`. Every v2
    webhook URL reaches the same DAG after migration.
31. The server lists exactly the projects in its registry. A directory
    under `paths.projects_dir` that no registered project uses is not a
    project, whatever it holds.
32. Each DAG file belongs to one project or the library: discovery skips a
    `dags_dir`, `projects_dir`, or `alt_dags_dir` nested in another, and
    never follows a symbolic link out of a project.
33. A `manager` of project X creates an API key limited to X, and cannot
    create one with a server-wide role or a grant on a project where the
    user is not `manager`.
34. An explicit start of a library DAG is refused in a project that has a
    DAG of the same name.
35. A v2.28 server runs a DAG file moved in git to `<projects_dir>/X/` as
    workspace `X`'s, and the first v3.0 start holds none of those DAGs.
36. A DAG that migration holds is not listed, scheduled, or startable in any
    project, and a user who could not see it on v2 cannot see it on v3.0.
37. A secret, profile, or DAG state that a worker reads through the
    coordinator comes from the task's project, then the server layer, and
    the coordinator refuses a call without a project for a task of another
    project.
38. A project DAG added under a library DAG's name starts with no runs,
    even when the library DAG has run in that project.
39. A `dagu` command inside a folder that holds `dagu.json` keeps the
    project's state in `<data_dir>/projects/<name>/` of its Dagu home and
    writes nothing inside the folder. State that no project owns is listed
    on the projects page, and a project created under its name takes it
    over.

## Alternatives considered

- **Remove workspaces without a replacement.** The first revision of this
  RFC. Multi-team installs would lose roles per group, visibility limits, and
  shared base config, and would stay on v2.
- **Rename workspaces to projects and keep the label model.** Fixes the name
  only. Every subsystem keeps filtering, and the boundary still leaks.
- **Label- or folder-based grants on one set of stores.** The same filtering
  in every subsystem under a new name.
- **One Dagu instance per group.** Works today, since `DAGU_HOME` already holds
  every path. Each group then needs its own scheduler, coordinator, workers,
  authentication setup, and URL, and no one sees across groups. It remains
  supported.
- **A scheduler process per project.** Multiplies locks, ports, and
  registrations. Runs already execute in their own processes, so isolation
  barely improves.
- **A migration command that must run before v3.0 starts.** An earlier
  revision of this RFC. Every upgrade with workspaces gains a manual step,
  and a server upgraded without it runs nothing until someone runs it. The
  preview gives the same review without blocking.
- **Refuse to start until migrated.** An image upgrade would leave the
  server restarting in a loop, with the reason only in its log.
- **Keep resolving moved DAGs for clients that omit `project`.** A second
  resolution path, with warnings and an admin page, for a one-time move. The
  error that names the DAG's projects shows the fix.
- **Copy shared child DAGs into each project.** Copies drift apart, and tests
  no longer run the files that production runs.
- **Merge projects that share child DAGs.** Brings back the coarse access
  that projects replace.
- **Settings per project with no server layer.** A credential or alert route
  that every team uses would be copied into each project and rotated in
  each, and migration would have to make those copies.
- **Renaming projects.** Commands, scripts, API clients, links,
  `project_mappings`, and directory paths carry the name, and a rename
  breaks each of them. The display name changes instead.
- **The name as the project's identity.** A project deleted and created
  again under one name would share grants in the audit trail and history in
  Dagu Cloud with the old one.
- **Project state inside its DAG directory, such as in a `.dagu/`
  folder.** `git clean -x`, a fresh clone, or a sync that replaces a
  checkout would delete runs and secrets. They would travel with the folder
  into build contexts and copies, several users or Dagu homes writing one
  folder would need ownership and home checks, and a backup would have to
  cover every folder.
- **State kept by folder path.** A moved folder would lose its history,
  and the CLI would have to remember every folder it ever ran in.
- **Keep spec 001's project separate.** A checkout and the server project
  it becomes would use one word for two things, as workspaces did.
- **Matching projects across servers by name.** A guess: two servers may
  use one name for unrelated work, and macOS and Windows ignore the case
  that Linux keeps.
- **An encryption key per project.** Keeps a copied directory's secrets
  unreadable elsewhere, but migration must re-encrypt every secret, and each
  key is one more file to back up.
- **Keep the default project's state at v2's paths.** Installs without
  workspaces would move nothing, but `paths.data_dir` would mix server-wide
  state with the default project's, the server layer would collide with
  the default project's own secrets and routes, and every store would need
  two layouts.
- **Move the default project with a command the operator runs.** Until it
  runs, v3.0 would have to serve both layouts, and refusing to start
  without it would leave an upgraded image restarting in a loop.

## Consequences

- Multi-team installs keep roles per group, visibility limits, and base
  config, with membership fixed by location.
- Settings every project shares stay in one place, in the server layer.
- Upgrading needs no manual step, with or without workspaces, and a preview
  shows each change beforehand. Every install's state moves once, so tools
  that read v2's paths need the new ones.
- Every project, the default one included, keeps its state in one layout,
  and `paths.data_dir` keeps server-wide state apart from it.
- Project-scoped API paths, MCP tools, and CLI commands gain a project
  parameter. Clients that omit it address the default project.
- `dag.run` cannot cross projects. Shared child DAGs live in the library and
  run in the caller's project.
- A DAG moves between projects with its history, as a v2 label change kept
  it.
- The scheduler gains a supervisor, and project units reuse the current
  scheduling logic.
- "Workspace" means only the Dagu Cloud account, and "project" means one
  thing in a checkout, on the server, and in Dagu Cloud.
- Project names never change and are valid directory names on macOS,
  Windows, and Linux. Display names can be any text and change at any time.
- Every store is either per project or server-wide, and stores added later
  follow the same rule.
- Dagu Cloud tells projects apart by binding, and report protocol 3 names
  each run's project.

## Open questions

1. **Git sync per project.** With `paths.projects_dir` at a git checkout,
   one repository holds every project as `<name>/` directories, as v2's
   `dags_dir` holds every workspace. A multi-team install that cannot limit
   paths with code owners needs one repository per project
   ([Access](#access)). Open: whether git sync supports that in v3.0.
2. **A server-wide cap on concurrent runs** across projects, beyond queues
   defined in `config.yaml`.
3. **An index for reads across projects.** Search and the All projects view
   read every visible project's stores. Open: whether servers with many
   projects need a server-wide index.
