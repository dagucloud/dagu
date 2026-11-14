# RFC 003: Replace Workspaces with Projects

- **Status:** Proposed
- **Date:** 2026-10-09, revised 2026-10-24 and 2026-11-14
- **Affected areas:** `workspace`, `auth`, `spec` (base config), `runtime`
  (`dag.run` resolution), `secret`, `profile`, notification routes, incident
  policies, wiki, `audit`, `gitsync`, `persis`, `cmn/crypto`, `cloudreport`,
  `service/scheduler`, `service/coordinator`, `service/mcp`, `api/v1`,
  `proto`, `ui`, and `cmn/config`

## Summary

Replace workspaces with projects in v3.0. A project is a directory with the
same layout as `DAGU_HOME`. It holds its DAGs, runs, logs, base config,
secrets, profiles, notification routes, incident policies, and wiki. Secrets,
profiles, notification routes, and incident policies also have a server layer
that every project inherits, as base config does. Users, API keys,
authentication, and workers stay server-wide. One server process serves every
project. Child DAGs that several projects call live in a library at
`paths.alt_dags_dir` and run in the project that calls them.

Each project has an ID that survives renames. Grants, audit entries, and
distributed tasks refer to the ID; people refer to the name. Dagu Cloud tells
projects apart by an explicit binding, never by name.

`dagu migrate workspaces` moves each v2 workspace into a project. While
unmigrated workspace items remain, v3.0 runs no workflows and serves the
migration plan instead of failing to start. Installs without workspaces
run unchanged as the default project.

v3.0 ships the core. Moving DAGs between projects, the All projects view,
renames on disk that finish without an admin, Dagu Cloud bindings, and
checkouts follow, as [Compatibility and rollout](#compatibility-and-rollout)
lists.

## Problem statement

Since v2.1.0, a workspace has scoped a group of DAGs across many subsystems:
access grants, base config, secrets, profiles, notification routes, incident
policies, wiki paths, and audit. Membership comes from the `workspace` label
on each DAG, and every workspace shares one set of stores. That design has
three problems.

1. **Every subsystem filters.** Each store, query, and API path checks the
   label. The API spec mentions workspaces about 270 times and the UI in 85
   files.
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
4. Leave installs without workspaces unchanged.
5. Migrate workspaces with one reviewed command, and never change silently
   which settings or secrets a workflow runs with.
6. Leave "workspace" to mean only the Dagu Cloud account.
7. Give each project an identity that survives renames and that Dagu Cloud
   can tell apart across servers, without putting Dagu Cloud in the run path.

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
  dags/ data/ logs/      the default project
  projects/
    weather/
      base.yaml          project base config
      dags/ data/ logs/
    sim/
      ...
```

- A project directory has the same layout as `DAGU_HOME`, without
  `config.yaml`.
- `dag_discovery` settings in `config.yaml` apply to every project's DAG
  directory, as they do to the default project.
- `paths.projects_dir` sets where projects live. The default is
  `$DAGU_HOME/projects`, or the XDG data directory when `DAGU_HOME` is unset.
- The default project is the existing home. Its paths, including any set in
  `config.yaml`, do not change.
- Project names follow [Names](#names).
- A new directory under `paths.projects_dir`, or under
  `paths.projects_dags_dir` when set, is a new project. Admins can also create
  one in the UI or API.

`paths.projects_dags_dir` keeps project DAG files apart from runtime state, as
`paths.dags_dir` can for the default project. A git checkout can then hold
every project's DAG files and base config.

```text
/srv/dagu-dags/          paths.projects_dags_dir
  weather/
    base.yaml            project base config
    *.yaml               DAG files
$DAGU_HOME/projects/
  weather/
    data/ logs/          runtime state
```

| `paths.projects_dags_dir` | DAG files | Project base config | Everything else |
| --- | --- | --- | --- |
| Unset | `projects/<name>/dags/` | `projects/<name>/base.yaml` | `projects/<name>/` |
| Set | `<projects_dags_dir>/<name>/` | `<projects_dags_dir>/<name>/base.yaml` | `projects/<name>/` |

`base.yaml` at the root of a project's DAG directory is the project base
config, not a DAG.

A project can instead keep its DAG files in an existing folder that the
admin chooses when creating it, such as a repository checkout elsewhere on
the machine. Its runtime state stays under `projects/<name>/`. See
[Lifecycle](#lifecycle).

### Names

A project name is a directory name, so it must work as one on macOS,
Windows, and Linux.

- It matches `^[A-Za-z0-9_][A-Za-z0-9_-]{0,63}$`. Run files nest several
  levels below the project directory, and many Windows tools stop at 260
  characters in a path. A name never starts with `-`, so `--project` never
  reads it as a flag.
- It is not a Windows device name: `con`, `prn`, `aux`, `nul`, `com1` to
  `com9`, or `lpt1` to `lpt9`, in any case.
- `default` is reserved, in any case.
- Names are unique without regard to case, because macOS and Windows compare
  file names that way and Linux does not. A request that names a project in
  another case resolves to the stored spelling, so every path uses one
  spelling.
- Discovery skips a directory whose name breaks these rules or differs only
  in case from another project's name. The server logs it, and the projects
  page lists it for admins.

A project also has a display name: any text up to 200 characters, unique
without regard to case. It is the name until an admin sets one. Pages show
the display name; commands, API calls, and links use the name.

### Scope

| Scope | Contents |
| --- | --- |
| Server | `config.yaml`, global `base.yaml`, the [server layer](#server-layer) of secrets, profiles, notification routes, and incident policies, library DAG files (`paths.alt_dags_dir`), business calendars, the project registry, users and their grants, API keys, OIDC and proxy authentication, the encryption key, license, upgrade check, Dagu Cloud connection with its report and remote access state, remote nodes, coordinator and worker state (dispatch tasks, leases, active distributed runs, worker heartbeats), service registry, the scheduler lock, pause flag, and restart backoff, audit log, admin logs, queues defined in `config.yaml` |
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
  output, and artifacts inside the project's directories relative to the
  project's root, so moving a project or a DAG leaves them valid. Records
  written before v3.0 hold absolute paths; whatever moves them, migration
  included, rewrites those paths. A path a DAG sets outside the project's
  directories stays absolute and does not move.
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
- The server layer is stored in `paths.data_dir`, apart from the default
  project's own entries.

### Identity and references

Each project has an ID, a random UUID assigned when the project is created or
first discovered. The ID never changes; the name can.

- A server-wide registry holds each project's ID, name, display name,
  creation time, and
  [Dagu Cloud binding](002-dagu-cloud-connector.md#binding). Every write to
  it holds one lock and checks the name rules before it stores. Writes are
  rare, so one lock costs nothing.
- Records that outlive a request refer to a project by ID: grants, audit
  entries, dispatch tasks, leases, and status reports. Names are for people:
  `--project`, the API's `project` parameter, and `project_mappings` take a
  name and resolve it to an ID.
- The default project gets its ID at the first v3.0 start.
- A DAG is identified by its project and name. Names are unique within a
  project.
- `dag.run` resolves its target in the caller's project, then in the
  library. A path target must lie inside the caller's project DAG directory
  or the library. A target in another project is a load error.
- A CLI command addresses the project named by `--project`, else by
  `DAGU_PROJECT`, else `default`. Once [checkouts](#checkouts) ship, a
  checkout found from the current directory comes before `default`.
- API paths and MCP tools that address DAGs, runs, secrets, profiles, routes,
  policies, or wiki pages take `project`. The default is `default`, so
  existing clients keep working against the default project. Clients that
  name a DAG which migration moved keep working too, as
  [Existing clients](#existing-clients) describes.
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
`.dagu.yaml` a project and runs its workflows from the CLI. That is the same
project seen from a checkout, not a second concept. Checkouts ship with spec
001, after v3.0.

- A project's DAG directory may hold `.dagu.yaml`. The server then discovers
  workflows, applies base config, and sets working directories as spec 001
  says, whatever `dag_discovery` says, so one checkout behaves the same on a
  laptop and as a server project.
- A path in `.dagu.yaml` that leads out of the project's DAG directory, such
  as `../billing`, is a load error, so one project never reads another's
  DAGs.
- The `project` field of `.dagu.yaml` names the server project a checkout
  belongs to. On a server, a `project` that differs from the project's name
  is a load error.
- [Spec 001](../specs/001-project.md#server-projects) defines which project
  a command addresses from inside a checkout: `--project`, then
  `DAGU_PROJECT`, then the server project whose DAG directory is the
  checkout, then the checkout on its own, then `default`.

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
  server-wide role applies to every project.
- A user's role in a project is the higher of the server-wide role and the
  project grant.
- A user with neither gets `404` for the project's DAGs, runs, logs, and wiki
  pages. The project does not appear in lists or search.
- `admin` is server-wide only.
- Projects separate people who work through the UI, API, MCP, and CLI. They
  do not separate people who can write DAG files. Whoever can change a
  project's DAG directory can run code with that project's secrets, and
  whoever can change the library can run code with the secrets of every
  project that calls it. A multi-team install therefore gives each project
  its own repository, or limits each project's directory to its team with
  code owners, and limits `paths.alt_dags_dir` to admins.
- OIDC and proxy `project_mappings` and `default_project_access` replace
  `workspace_mappings` and `default_workspace_access` with the same shape.
  v3.0 reads the old keys with a warning. v3.1 drops them.

### Lifecycle

- **Create.** An admin creates a project in the UI or API, or with `dagu
  project create`, or adds a directory that discovery finds. Either way the
  registry assigns its ID, and the scheduler starts its unit without a
  restart. When creating, the admin chooses where its DAG files live:
  - **Default.** Dagu creates the project's DAG directory where
    [Layout](#layout) puts it.
  - **An existing folder,** such as a repository checkout, by its absolute
    path. Dagu reads the DAG files and `base.yaml` there, and spec 001 rules
    when the folder holds `.dagu.yaml`. Dagu never moves or deletes the
    folder. The project's runtime state still lives under
    `projects/<name>/`, and the registry records the folder's path.

  The UI suggests the folder's name as the project name and display name.
  An existing folder must lie under one of the directories that
  `paths.allowed_project_folders` in `config.yaml` lists. With none listed,
  only the default is offered, so an admin of the UI cannot expose files of
  the server's machine that its operator did not allow. A folder already
  used by a project, inside another project's directories, inside
  `paths.projects_dir` or `paths.projects_dags_dir`, or inside the library
  is refused. Paths are compared as the operating system compares them.
- **Display name.** An admin changes it in the UI or API at any time. Only
  the registry changes.
- **Rename.** An admin renames a project in the UI or API, or with `dagu
  project rename`. The rename runs in three steps:
  1. The registry marks the project as moving. The scheduler unit, the
     coordinator, and the server stop using its stores and confirm in the
     registry. A process that does not confirm in time abandons the rename,
     and the project resumes. Windows cannot move a directory with open
     files, so no move starts before every process lets go.
  2. Under the registry lock, which discovery also takes, the rename moves
     the project's directories and updates the registry. No process sees
     the moved directory as a new project.
  3. The processes open the project's stores at the new paths.

  The ID, grants, Dagu Cloud binding, and run history stay. Clients that
  address the project by name use the new name. With
  `paths.projects_dags_dir` set, a git checkout shows the moved DAG
  directory as a change to commit. A project on an existing folder keeps
  its folder; only its runtime state moves.
- **Delete.** An admin deletes a project in the UI or API, or with `dagu
  project delete`. After the same first step as a rename, it removes the
  project's directories, registry entry, grants, and Dagu Cloud binding.
  With `paths.projects_dags_dir` set, it is refused while the project's DAG
  directory exists: the checkout owns that directory, so it is removed
  there first. Deleting a project on an existing folder leaves the folder
  as it is.
- Rename and delete are refused while the project has runs queued, running,
  or waiting.
- **Changed on disk.** A project whose DAG directory disappears outside
  Dagu, as it does when a git checkout renames it, is listed as missing. Its
  entry keeps its ID, grants, binding, and run history. When a new directory
  appears in the same discovery pass:
  - After v3.0: if it holds the same DAG file names as one missing project,
    and no other new directory does, the server finishes the rename itself
    and logs it, so schedules keep running. Whoever can rename the
    directory in the checkout can already change those DAGs, so this grants
    nothing new.
  - Otherwise, and for every rename in v3.0, the projects page asks an
    admin whether the project was renamed to it, and the new directory is
    not scheduled until the admin answers. Confirming finishes the rename;
    declining registers a new project, with no grants and no binding.
  - Finishing a rename moves the runtime state to the new name in the same
    steps as a rename, and the project keeps everything else.

  An admin can also relink a missing project to any unregistered directory,
  or delete it. A project on an existing folder that was moved or removed
  is listed as missing the same way, and the admin relinks it to the
  folder's new path.
- **The default project** cannot be renamed or deleted. Its display name
  can change.

### Moving a DAG

Teams reorganize, so DAGs move between projects. In v2 a label change moved
a DAG and kept its history; a move between projects keeps it too. The move
ships after v3.0. Until then, a DAG file moved by hand starts over in its
new project, and its runs stay in the old one.

- An admin, or a user with the `manager` role in both projects, moves a DAG
  in the UI or API, or with `dagu move`. Its file, runs, logs, artifacts,
  DAG state, suspend flag, settings, and per-DAG webhooks move with it, and
  its webhook URLs keep working. Runs in which it was a child stay with
  their parent runs.
- The target project's users then see the DAG's past runs and logs, which
  can hold data the source project never shared. The move asks to confirm
  that before it starts.
- Before anything moves, the move lists the secrets, profiles, and
  notification routes the DAG uses that the target project lacks, so what
  the DAG runs with never changes silently.
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
  digits, such as `天気予報`, the admin types the name.
- The access editor, the base config page, and the pages for secrets,
  profiles, routes, and policies work per project.
- The projects page shows admins missing projects and suggested renames.

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
- Queues defined in `config.yaml` are shared across units through one limiter,
  as they are shared across workspaces in v2. Other queues are per project.
- One file watcher covers `projects/`, `paths.projects_dags_dir` when set,
  and every existing folder a project uses, and routes events by path. A
  watcher per project would reach the Linux inotify instance limit (128 by
  default) on servers with many projects.

## Distributed execution

- Dispatch tasks, leases, and status reports carry the project ID. The
  coordinator records attempts in that project's run store.
- Tasks already carry base config content and the profile name. Both now come
  from the task's project. Profile and secret values resolve from the task's
  project, then the server layer.
- `base_config_workspace` is removed from the task.
- Workers stay shared and do not read project directories. A `dag.run` inside
  a dispatched run fetches its child from the coordinator, which resolves it
  in the task's project, then the library. `GetDAG` gains a project ID field
  for this.
- A worker's own `dags_dir` and `alt_dags_dir` serve only default-project
  tasks, so a project run never picks up a default-project DAG with the same
  name.
- Coordinators and workers older than v3.0 cannot place a task in a
  project. A v3.0 coordinator gives them no tasks, and a v3.0 worker does
  not connect to an older coordinator; each says why in its log and on the
  workers page. Upgrade the coordinator and server first, then the workers.
  Runs wait in their queues until an upgraded worker connects.

## Dagu Cloud

Workflows never depend on Dagu Cloud
([RFC 002](002-dagu-cloud-connector.md)), and projects keep it that way.
[RFC 002](002-dagu-cloud-connector.md#projects) specifies how a connected
server reports its projects and binds them; this RFC fixes what that needs:

- Reports identify a project by its ID, never its name. From v3.0, report
  protocol 3 names the project of each run, so `etl` in two projects is two
  workflows in Dagu Cloud.
- A server shares a project with Dagu Cloud only through an explicit
  [binding](002-dagu-cloud-connector.md#binding), never by matching names.
- The registry stores the binding, and [Lifecycle](#lifecycle) says what
  rename, delete, and changes on disk do to it.

## Migration

`dagu migrate workspaces` prints a plan and changes nothing. With `--apply`,
it carries out the plan. Planning is read-only, so it can run against a live
v2 install before the upgrade. Apply it with every Dagu process for that
`DAGU_HOME` stopped.

| v2 | v3.0 |
| --- | --- |
| Workspace `X` | Project `X` in the registry, with a new ID |
| DAG labelled `workspace=X` | `projects/X/dags/`, or `<projects_dags_dir>/X/` when set, with its runs, logs, artifacts, DAG state, suspend flags, and DAG settings |
| DAG or base file already at its v3 path | Left in place. Its runs, logs, and state still move |
| DAG without a `workspace` label | Stays in the default project |
| DAG under `paths.alt_dags_dir` | Stays in place as a library DAG |
| `<dags>/workspaces/X/base.yaml` | `projects/X/base.yaml`, or `<projects_dags_dir>/X/base.yaml` when set |
| Secrets and profile entries of `X` | Project `X`'s secrets and profiles, still encrypted with the server's key |
| Notification routes and incident policies of `X` | Project `X`'s routes and policies |
| Global secrets, profile defaults, notification routes, and incident policies | The server layer, which every project inherits |
| Per-DAG wiki directory `<wiki>/X/<dag>`, pages under `<wiki>/X/` | Project `X`'s wiki |
| User or API key with roles in `X` | Grants on project `X` |
| User or API key with `workspace_access.all: false` | Also `viewer` on the default project, which keeps v2 read access to unlabelled DAGs |
| `workspace_mappings`, `default_workspace_access` | Read as `project_mappings`, `default_project_access` until v3.1 |
| `workspace` label on a moved DAG | Kept as an ordinary label; the file is not rewritten |

`--apply` moves a file only while it is still at its v2 path. Operators who
keep DAG files in git can move them first, and `--apply` then moves only runs,
state, secrets, routes, policies, and grants. The plan finds labelled DAG
files at either path.

The plan lists these conflicts, and `--apply` refuses while any remain:

- A `dag.run` target that would land in another project.
- A DAG or base file present at both its v2 and v3 paths.
- A workspace name that breaks the [name rules](#names), including
  `default` and two names that differ only in case. `--rename OLD=NEW` gives
  such a workspace a project name.
- A queue not defined in `config.yaml` that DAGs in different projects share.
  Defining it in `config.yaml` keeps it shared.

The plan also lists these items without refusing:

- Each library DAG and its static callers. In v2, a child in its own file
  loads the base config of its own `workspace` label, so a library DAG called
  from workspace `X` did not load `X`'s base config. In v3, it gets the
  caller's project base config.
- Each `dag.run` target that contains a variable, such as
  `${JOBS_ROOT}/facilities/${ITEM}`, since the plan cannot resolve it. At run
  time, a target found in neither the caller's project nor the library fails
  the step with a load error that names both.

### Existing clients

v2 DAG names are unique on a server, so API clients, CLI scripts, MCP
clients, and webhook URLs name a DAG without its workspace. Migration must
not break them.

- Migration records each DAG it moves: its v2 name and its project. A
  request without `project` that names a DAG the default project lacks
  resolves through that record, and only through it, when the caller can
  see the recorded project. The response carries a deprecation warning.
- DAGs created after migration never enter the record, so a new DAG in
  another project never changes where an old client's call goes.
- Otherwise the request fails, and the error names the projects the caller
  can see that have a DAG of that name. Projects the caller cannot see are
  never named.
- CLI commands without `--project` and MCP tools without `project` resolve
  the same way and print the warning.
- Per-DAG webhook URLs keep their paths and tokens, and reach the DAG in its
  new project.
- The admin page lists the users and API keys that still rely on this
  fallback. A later major release removes it.

### Before migration

While anything the migration would move remains, v3.0 starts in a
migration-required mode instead of exiting. An image upgrade then leaves a
server that explains itself, not one that restarts in a loop.

- The server serves the migration plan and the command that applies it. The
  UI shows them on every page; other API calls fail with `503` and the
  message below.
- The scheduler schedules nothing, the coordinator dispatches nothing, and
  CLI commands that start runs refuse with the message.
- The health endpoint stays up and reports the mode, so orchestrators do not
  restart the process.
- After `--apply`, which still runs with every Dagu process stopped, the next
  start runs normally.

```text
dagu: workspaces became projects in v3.0, and these items are not migrated:
  workflows        12 DAGs labelled workspace=billing
  base config      dags/workspaces/billing/base.yaml
  secret           DB_PASSWORD (workspace billing)
  incident policy  workspace billing
Run `dagu migrate workspaces` to review the plan, then apply it with --apply.
```

The log, the UI, and refused commands show this message. It lists every
item, not the first one found.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.22 | Warnings and an admin notice that list workspace settings in use. |
| Next v2 release | The warning and notice say that v3.0 moves workspaces into projects, and stop asking operators to move settings by hand. |
| v3.0 | Projects with IDs, names, and display names; the server layer; store scopes and relative run paths; create, on the default directory or an existing folder, rename, and delete, with an admin confirming each rename made on disk; `dagu migrate workspaces`, the migration-required mode, and the fallback for existing clients; report protocol 3 (RFC 002). |
| v3.1 | Drops the `workspace_mappings` and `default_workspace_access` aliases. |
| After v3.0 | Moving DAGs between projects; the All projects view; renames made on disk that finish without an admin when the DAG files match; Dagu Cloud bindings (RFC 002); checkouts, with spec 001. |

v2 gets no new workspace features.

## Verification and acceptance criteria

Criteria 24, 25, 26, and 28, and the part of 17 that finishes a rename
without an admin, apply from the release that ships the feature.

1. A v2 data directory with no workspaces starts on v3.0 unchanged, as the
   default project.
2. After migration, each DAG outside the library runs with the same base
   config, secrets, profile, notification routes, and incident policies as on
   v2. The plan lists every library DAG whose base config changes.
3. After migration, each user and API key can see and do what it could on v2,
   and no more.
4. A user without a role in a project gets `404` for its DAGs, runs, logs, and
   wiki pages, and does not see the project in lists or search.
5. `dag.run` to a DAG in another project fails at load.
6. One scheduler process schedules every project. A new project directory
   starts scheduling without a restart.
7. A distributed run of a project DAG records its status in that project.
8. While workspace items remain, v3.0 runs no workflows, serves the plan,
   keeps its health endpoint up, and its message lists every item.
9. Planning a migration changes nothing on disk.
10. A library DAG called from project X runs with X's base config, secrets,
    and profiles, and only users with a role in X see its run.
11. Library DAGs are not listed among a project's DAGs or scheduled.
12. A `dag.run` inside a distributed run resolves only in the task's project
    and the library.
13. With `paths.projects_dags_dir` set, DAG files and project base config
    load from it, and runtime state stays under `paths.projects_dir`.
14. `--apply` leaves DAG files already at their v3 path in place and still
    moves their runs and state.
15. A project name that breaks the name rules is refused at creation and
    skipped at discovery, and the server logs it.
16. Renaming a project keeps its ID, grants, Dagu Cloud binding, and run
    history. Rename and delete are refused while the project has runs
    queued, running, or waiting, start no move until every process has let
    go of the project, and are refused for the default project.
17. A project directory renamed outside Dagu with its DAG files unchanged
    keeps its ID, grants, binding, run history, and schedules without an
    admin. Any other new directory that appears as a project goes missing
    is offered to an admin and not scheduled until the admin answers.
18. Every project's secrets decrypt with the server's key, and no project
    directory holds a key of its own.
19. A project DAG uses the server layer's secrets, profile defaults,
    notification routes, and incident policies unless its project overrides
    them, as a v2 workspace DAG uses global entries.
20. A display name takes any text up to 200 characters, and changing it
    moves no directory.
21. A user who can see one project sees no project selector.
22. Every user can read library DAGs on the Library page, and only admins
    change them.
23. After migration, an API call, CLI command, MCP call, or webhook URL that
    named a v2 DAG without its workspace still reaches the DAG, with a
    deprecation warning, unless the caller cannot see its project. A DAG
    created after migration never changes where such a call goes.
24. Moving a DAG to another project keeps its runs, logs, state, and webhook
    URLs. It lists what the target project lacks before it moves, and is
    refused while a `dag.run` caller remains in the source project.
25. All projects shows the runs and failures of every project the user can
    see, and of no other.
26. A `dagu` command run anywhere inside a checkout of a project's DAG
    directory, without `--project` or `DAGU_PROJECT`, addresses that
    project.
27. After migration, a rename, or a move, the logs, step output, and
    artifacts of every moved run open as before.
28. A `.dagu.yaml` path that leads out of its project's DAG directory is a
    load error.
29. A v3.0 coordinator gives no task to an older worker, and both say why.
30. An admin can create a project on an existing folder under
    `paths.allowed_project_folders`. Dagu reads its DAGs there, keeps the
    project's runtime state under `projects/<name>/`, and never moves or
    deletes the folder.
31. A folder outside `paths.allowed_project_folders`, already used by a
    project, or inside another project's or Dagu's own directories is
    refused.

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
- **Migrate automatically at startup.** Moving DAG files and run history
  without review is hard to undo.
- **Refuse to start until migrated.** An image upgrade would leave the
  server restarting in a loop, with the reason only in its log.
- **Copy shared child DAGs into each project.** Copies drift apart, and tests
  no longer run the files that production runs.
- **Merge projects that share child DAGs.** Brings back the coarse access
  that projects replace.
- **Settings per project with no server layer.** A credential or alert route
  that every team uses would be copied into each project and rotated in
  each, and migration would have to make those copies.
- **The name as the project's identity.** A rename would orphan grants,
  audit entries, and tasks in flight.
- **An ID file in each project directory.** Survives renames made on disk,
  but every DAG checkout then carries a generated file, and a copied
  directory claims the same ID.
- **Relink any new directory without asking.** A guess on the name alone
  could hand one project's grants, secrets, and history to unrelated DAGs.
  Matching DAG file names first removes that risk.
- **Ask an admin about every rename.** Safe, but a rename merged in git
  overnight pauses the project's schedules until someone answers.
- **Directories named by project ID.** Makes a rename a registry update, but
  the layout and git checkouts become unreadable, and adding a directory no
  longer creates a project.
- **Keep spec 001's project separate.** A checkout and the server project
  it becomes would use one word for two things, as workspaces did.
- **Matching projects across servers by name.** A guess: two servers may
  use one name for unrelated work, and macOS and Windows ignore the case
  that Linux keeps.
- **An encryption key per project.** Keeps a copied directory's secrets
  unreadable elsewhere, but migration must re-encrypt every secret, and each
  key is one more file to back up.

## Consequences

- Multi-team installs keep roles per group, visibility limits, and base
  config, with membership fixed by location.
- Settings every project shares stay in one place, in the server layer.
- Upgrading an install with workspaces takes one reviewed command.
- Project-scoped API paths, MCP tools, and CLI commands gain a project
  parameter. Clients that omit it address the default project, or, until a
  later major release, the one project that has the DAG they name.
- `dag.run` cannot cross projects. Shared child DAGs live in the library and
  run in the caller's project.
- A DAG moves between projects with its history, as a v2 label change kept
  it.
- The scheduler gains a supervisor, and project units reuse the current
  scheduling logic.
- "Workspace" means only the Dagu Cloud account, and "project" means one
  thing in a checkout, on the server, and in Dagu Cloud.
- Each project has an ID, so a rename keeps its grants and history. Project
  names are valid directory names on macOS, Windows, and Linux, and display
  names can be any text.
- Every store is either per project or server-wide, and stores added later
  follow the same rule.
- Dagu Cloud tells projects apart by binding, and report protocol 3 names
  each run's project.

## Open questions

1. **Git sync per project.** With `paths.projects_dags_dir`, one repository
   holds every project as `<name>/` directories. That keeps a v2 install's
   setup and shows the migration as a single change to publish. A multi-team
   install that cannot limit paths with code owners needs one repository per
   project ([Access](#access)). Open: whether git sync supports that in v3.0.
2. **A server-wide cap on concurrent runs** across projects, beyond queues
   defined in `config.yaml`.
3. **An index for reads across projects.** Search and the All projects view
   read every visible project's stores. Open: whether servers with many
   projects need a server-wide index.
