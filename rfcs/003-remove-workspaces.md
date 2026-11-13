# RFC 003: Replace Workspaces with Projects

- **Status:** Proposed
- **Date:** 2026-10-09, revised 2026-10-24 and 2026-11-13
- **Affected areas:** `workspace`, `auth`, `spec` (base config), `runtime`
  (`dag.run` resolution), `secret`, `profile`, notification routes, incident
  policies, wiki, `audit`, `gitsync`, `persis`, `cmn/crypto`, `cloudreport`,
  `service/scheduler`, `service/coordinator`, `service/mcp`, `api/v1`,
  `proto`, `ui`, and `cmn/config`

## Summary

Replace workspaces with projects in v3.0. A project is a directory with the
same layout as `DAGU_HOME`. It holds its DAGs, runs, logs, base config,
secrets, profiles, notification routes, incident policies, and wiki. Users,
API keys, authentication, and workers stay server-wide. One server process
serves every project. Child DAGs that several projects call live in a library
at `paths.alt_dags_dir` and run in the project that calls them.

Each project has an ID that survives renames. Grants, audit entries, and
distributed tasks refer to the ID; people refer to the name. Dagu Cloud tells
projects apart by an explicit binding, never by name.

`dagu migrate workspaces` moves each v2 workspace into a project. v3.0 refuses
to start while unmigrated workspace items remain. Installs without workspaces
run unchanged as the default project.

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

### Scope

| Scope | Contents |
| --- | --- |
| Server | `config.yaml`, global `base.yaml`, library DAG files (`paths.alt_dags_dir`), business calendars, the project registry, users and their grants, API keys, OIDC and proxy authentication, the encryption key, license, upgrade check, Dagu Cloud connection with its report and remote access state, remote nodes, coordinator and worker state (dispatch tasks, leases, active distributed runs, worker heartbeats), service registry, the scheduler lock, pause flag, and restart backoff, audit log, admin logs, queues defined in `config.yaml` |
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
- **Reads across projects.** Most pages show one project. Search, and any
  page that spans projects, reads the stores of each project the user can
  see, a few at a time, and merges the results. Its cost grows with the
  number of projects.

### Base config

A DAG's base config is the global `base.yaml`, then its project's `base.yaml`,
then the DAG file. The order matches v2's global, workspace, DAG. A child
started with `dag.run` runs in its caller's project, so it gets that project's
base config without carrying provenance. This holds whether the child is in
the caller's file, in its own file, or in the library.

### Identity and references

Each project has an ID, a random UUID assigned when the project is created or
first discovered. The ID never changes; the name can.

- A server-wide registry holds each project's ID, name, creation time, and
  [Dagu Cloud binding](#binding). Every write to it holds one lock and
  checks the name rules before it stores. Writes are rare, so one lock costs
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
  or the library. A target in another project is a load error.
- CLI commands take `--project` (`DAGU_PROJECT`). The default is `default`.
- API paths and MCP tools that address DAGs, runs, secrets, profiles, routes,
  policies, or wiki pages take `project`. The default is `default`, so
  existing clients keep working against the default project.
- `/projects` lists and creates projects. The `/workspaces*`,
  `/settings/workspaces/*`, `/notification-routes/workspaces/*`,
  `/incident-policies/workspaces/*`, and `/profiles/_workspaces/*` paths and
  the `?workspace=` parameter are removed.
- Local runs start their child process with `--project`. `DAGU_HOME` stays the
  server root, so the child still reads `config.yaml` and the global base.
- Audit entries record the project's ID and its name at the time. Old
  entries keep `workspace`.

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

- Library DAGs are not listed or scheduled on their own. Validation warns when
  one has a `schedule`.
- A library DAG runs in the project that starts it: the caller's project for
  `dag.run`, or the project given to an explicit start. Its run is recorded
  there, and it uses that project's base config, secrets, and profiles.
- A `dag.run` inside a library DAG resolves in the same project, then the
  library.
- A library run is visible only to users with a role in the project that ran
  it. Library definitions are readable through every project.
- Every project can call the library. A library call is not a cross-project
  call.

### Access

- A grant is a user or group, a project, and a role below `admin`. A
  server-wide role applies to every project.
- A user's role in a project is the higher of the server-wide role and the
  project grant.
- A user with neither gets `404` for the project's DAGs, runs, logs, and wiki
  pages. The project does not appear in lists or search.
- `admin` is server-wide only.
- OIDC and proxy `project_mappings` and `default_project_access` replace
  `workspace_mappings` and `default_workspace_access` with the same shape.
  v3.0 reads the old keys with a warning. v3.1 drops them.

### Lifecycle

- **Create.** An admin creates a project in the UI or API, or with `dagu
  project create`, or adds a directory that discovery finds. Either way the
  registry assigns its ID, and the scheduler starts its unit without a
  restart.
- **Rename.** An admin renames a project in the UI or API, or with `dagu
  project rename`. The rename holds the registry lock while it moves the
  project's directories and updates the registry. Discovery takes the same
  lock, so no process sees the moved directory as a new project. The ID,
  grants, Dagu Cloud binding, and run history stay. Clients that address the
  project by name use the new name. With `paths.projects_dags_dir` set, a git
  checkout shows the moved DAG directory as a change to commit.
- **Delete.** An admin deletes a project in the UI or API, or with `dagu
  project delete`. It removes the project's directories, registry entry,
  grants, and Dagu Cloud binding. With `paths.projects_dags_dir` set, it is
  refused while the project's DAG directory exists: the checkout owns that
  directory, so it is removed there first.
- Rename and delete are refused while the project has runs queued, running,
  or waiting.
- **Changed on disk.** A directory renamed or removed outside Dagu is not a
  rename. The old registry entry keeps its ID, grants, and binding, and the
  projects page lists it as missing until its directory returns or an admin
  deletes it. A renamed directory registers as a new project, with no grants
  and no binding.

### UI

The workspace selector becomes a project selector that lists the projects the
user can see. The access editor and the base config page work per project.

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
- One file watcher covers `projects/`, and `paths.projects_dags_dir` when
  set, and routes events by path. A watcher per project would reach the Linux
  inotify instance limit (128 by default) on servers with many projects.

## Distributed execution

- Dispatch tasks, leases, and status reports carry the project ID. The
  coordinator records attempts in that project's run store.
- Tasks already carry base config content and the profile name. Both now come
  from the task's project. Profile and secret values resolve from the task's
  project.
- `base_config_workspace` is removed from the task.
- Workers stay shared and do not read project directories. A `dag.run` inside
  a dispatched run fetches its child from the coordinator, which resolves it
  in the task's project, then the library. `GetDAG` gains a project ID field
  for this.
- A worker's own `dags_dir` and `alt_dags_dir` serve only default-project
  tasks, so a project run never picks up a default-project DAG with the same
  name.

## Dagu Cloud

Workflows never depend on Dagu Cloud
([RFC 002](002-dagu-cloud-connector.md)), and projects keep it that way.
Everything below is metadata about a project. Losing the connection changes
no run.

### Binding

- An admin of a connected server binds a project to a project of the Dagu
  Cloud workspace the server is connected to, in the UI or with `dagu project
  bind`. The registry stores the Dagu Cloud project's ID.
- Dagu Cloud never matches projects by name. Two servers share a Dagu Cloud
  project only when an admin of each has bound a local project to it, so
  `weather` on two servers stays two projects until someone binds them.
- A server binds a Dagu Cloud project to at most one of its projects.
  Unbinding, or disconnecting the server, ends what the binding enables and
  leaves the local project as it is.
- In Dagu Cloud, a project belongs to a workspace and is what servers bind
  to. A bundle of DAG files that Dagu Cloud deploys is a version of such a
  project, not a second kind of project.

### Reports

Report protocol 3 extends the
[RFC 002 wire contract](002-dagu-cloud-connector.md#wire-contract):

- Each run, scheduled DAG, and queue that is not defined in `config.yaml`
  carries `project`: the local project's ID and name, and the Dagu Cloud
  project's ID when bound.
- Dagu Cloud keys a server's workflows by project ID and DAG name, so `etl`
  in two projects is two workflows. A protocol 2 report belongs to the
  default project.
- The reporter reads every project's run events and keeps a cursor for each.
  It sends Dagu Cloud one opaque `cursor` for the set, still at most 4096
  bytes, and reports a gap when any project lost events.

### What a binding enables

- **Inventory sync** ([RFC 012](012-node-inventory.md#dagu-cloud-sync))
  covers bound projects only. Servers bound to the same Dagu Cloud project
  share its inventories.
- **Deploys,** in a later RFC, write only into a bound project and never
  create one. A deploy owns the project's DAG files and base config: the
  server does not edit them, and a change on disk shows as drift.

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

### Refusal message

v3.0 refuses to start while anything the migration would move remains.

```text
dagu: workspaces became projects in v3.0, and these items are not migrated:
  workflows        12 DAGs labelled workspace=billing
  base config      dags/workspaces/billing/base.yaml
  secret           DB_PASSWORD (workspace billing)
  incident policy  workspace billing
Run `dagu migrate workspaces` to review the plan, then apply it with --apply.
```

The message lists every item, not the first one found.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.22 | Warnings and an admin notice that list workspace settings in use. |
| Next v2 release | The warning and notice say that v3.0 moves workspaces into projects, and stop asking operators to move settings by hand. |
| v3.0 | Projects, `dagu migrate workspaces`, and the startup refusal. |
| v3.1 | Drops the `workspace_mappings` and `default_workspace_access` aliases. |

v2 gets no new workspace features.

## Verification and acceptance criteria

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
8. v3.0 refuses to start while workspace items remain, and the message lists
   every item.
9. Planning a migration changes nothing on disk.
10. A library DAG called from project X runs with X's base config, secrets,
    and profiles, and only users with a role in X see its run.
11. Library DAGs are not listed or scheduled.
12. A `dag.run` inside a distributed run resolves only in the task's project
    and the library.
13. With `paths.projects_dags_dir` set, DAG files and project base config
    load from it, and runtime state stays under `paths.projects_dir`.
14. `--apply` leaves DAG files already at their v3 path in place and still
    moves their runs and state.
15. A project name that breaks the name rules is refused at creation and
    skipped at discovery, and the server logs it.
16. Renaming a project keeps its ID, grants, Dagu Cloud binding, and run
    history. Rename and
    delete are refused while the project has runs queued, running, or
    waiting.
17. A directory renamed outside Dagu registers as a new project without
    grants, and the old entry is listed as missing.
18. Every project's secrets decrypt with the server's key, and no project
    directory holds a key of its own.
19. A connected server's reports carry each run's project, and two projects
    with the same DAG name are two workflows in Dagu Cloud.
20. Two servers share a Dagu Cloud project only after an admin of each binds
    a local project to it.

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
- **Copy shared child DAGs into each project.** Copies drift apart, and tests
  no longer run the files that production runs.
- **Merge projects that share child DAGs.** Brings back the coarse access
  that projects replace.
- **The name as the project's identity.** A rename would orphan grants,
  audit entries, and tasks in flight.
- **An ID file in each project directory.** Survives renames made on disk,
  but every DAG checkout then carries a generated file, and a copied
  directory claims the same ID.
- **Directories named by project ID.** Makes a rename a registry update, but
  the layout and git checkouts become unreadable, and adding a directory no
  longer creates a project.
- **Matching projects across servers by name.** A guess: two servers may
  use one name for unrelated work, and macOS and Windows ignore the case
  that Linux keeps.
- **An encryption key per project.** Keeps a copied directory's secrets
  unreadable elsewhere, but migration must re-encrypt every secret, and each
  key is one more file to back up.

## Consequences

- Multi-team installs keep roles per group, visibility limits, and base
  config, with membership fixed by location.
- Upgrading an install with workspaces takes one reviewed command.
- Project-scoped API paths, MCP tools, and CLI commands gain a project
  parameter. Clients that omit it address the default project.
- `dag.run` cannot cross projects. Shared child DAGs live in the library and
  run in the caller's project.
- The scheduler gains a supervisor, and project units reuse the current
  scheduling logic.
- "Workspace" means only the Dagu Cloud account.
- Each project has an ID, so a rename keeps its grants and history. Project
  names are valid directory names on macOS, Windows, and Linux.
- Every store is either per project or server-wide, and stores added later
  follow the same rule.
- Dagu Cloud tells projects apart by binding, and report protocol 3 names
  each run's project.

## Open questions

1. **Git sync per project.** With `paths.projects_dags_dir`, one repository
   holds every project as `<name>/` directories. That keeps a v2 install's
   setup and shows the migration as a single change to publish. Open: whether
   git sync also supports one repository per project.
2. **A server-wide cap on concurrent runs** across projects, beyond queues
   defined in `config.yaml`.
3. **An index for reads across projects.** Search reads every visible
   project's stores. Open: whether servers with many projects need a
   server-wide index.
