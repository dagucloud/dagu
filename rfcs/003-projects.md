# RFC 003: Replace Workspaces with Projects

- **Status:** Accepted
- **Date:** 2026-10-09, revised 2026-10-24, 2026-11-14, and 2026-11-23
- **Affected areas:** `workspace`, `auth`, `spec` (base config), `runtime`
  (`dag.run` resolution), `secret`, `profile`, notification routes, incident
  policies, wiki, `audit`, `gitsync`, `persis`, `cmn/crypto`, `cloudreport`,
  `service/scheduler`, `service/coordinator`, `service/mcp`, `api/v1`,
  `proto`, `ui`, and `cmn/config`

## Summary

Replace workspaces with projects in v3.0. A project is a directory of DAG
files with its own runs, logs, base config, secrets, profiles, notification
routes, incident policies, and wiki. A project inherits none of these from
the server: every setting a workflow runs with belongs to one project, the
default project or another. Users, API keys, authentication, and workers
stay server-wide. A project's DAG files live under `paths.projects_dir`, and
its state lives in the server's data directory. One server process serves
every project. Child DAGs that several projects call live in a library at
`paths.alt_dags_dir` and run in the project that calls them.

A project's name never changes; its display name can. Each project also has
an ID, so a project created again under a deleted one's name is a new
project. Dagu Cloud tells projects apart by an explicit binding, never by
name.

v3.0 makes each v2 workspace a project on its first start, and v2's
global settings become the default project's. It moves state and no DAG
file. `dagu migrate workspaces` previews that start and changes nothing. Installs
without workspaces run as the default project; the same first start moves
its state into `<data_dir>/projects/default/`, like every project's.

v3.0 ships the core, with git sync per project. Moving DAGs between
projects, the All projects view, and Dagu Cloud bindings follow, as
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
8. Give every setting a workflow runs with, other than business calendars,
   one owner, a project, with no server-wide layer that projects inherit.

Goals 4 and 5 have four exceptions, and the preview lists each before the
upgrade. An install whose paths overlap ([Layout](#layout)) sets
`paths.projects_dir` or moves a path. A DAG with an unsettled `workspace`
label ([Migration](#migration)) loads once its label is removed or its
file moves into a project. A DAG that v2 found through a symbolic link out
of `paths.dags_dir`, and a library DAG that v2 scheduled because
`alt_dags_dir` lay inside `dags_dir`, are scheduled again once their files
move into a DAG directory.

## Non-goals

- A process per project.
- Users or authentication configured per project.
- `dag.run` across projects.
- Spreading projects across several scheduler processes.
- Projects found from a `dagu.json` file. The registry is the only list
  of projects, and a `dagu.json` in a DAG directory changes nothing.
- Renaming `internal/runtime/workspacebundle`, which packs directories for
  distributed runs and is unrelated.

## Projects

### Layout

```text
$DAGU_HOME/
  config.yaml            server-wide settings
  base.yaml              the default project's base config
  dags/                  the default project's DAG files
  logs/admin/            admin and audit logs
  data/                  paths.data_dir
    projects.json        the project registry
    server/              queues defined in config.yaml
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
  `dags_dir/wiki` by default. Wiki pages then stay in git with the DAGs
  they describe.
- `paths.projects_dir` defaults to `projects` in the Dagu home that holds
  the default `dags` directory: the directory given by `--dagu-home`, else
  `$DAGU_HOME`, else `~/.dagu` when it exists. Without a Dagu home, it
  defaults to `projects` beside `dags` in the XDG config directory. A
  project's [git sync](#git-sync), or a git checkout at
  `paths.projects_dir`, keeps its DAG files and base config in git.
- When `paths.projects_dir` and `paths.dags_dir` are on different file
  systems, the server warns at startup and on the projects page. A
  container that mounts only `dags` would lose every project's DAG files
  when it is recreated.
- The state directory is `<data_dir>/projects/<name>/`. It holds
  everything else the project has: `runs/`, `logs/`, `artifacts/`,
  `settings/` for secrets, profiles, notification routes, and incident
  policies, and `state/` for the rest of [Scope](#scope). State never
  lives in a DAG directory, so a checkout stays clean.
- When `paths.log_dir`, `paths.artifact_dir`, `paths.dag_runs_dir`, or
  `paths.dag_run_work_dir` is set, by `config.yaml`, the environment, or a
  flag, a project's logs, artifacts, runs, or run work directories live
  there instead, in `.projects/<name>/`. A volume set aside for any of them
  then holds every project's, and migration moves them within that volume.
- Outside `projects/`, Dagu keeps only server-wide state in
  `paths.data_dir`: the registry in `projects.json`, the queues defined in
  `config.yaml` in `server/`, and the rest of the Server row of
  [Scope](#scope). Every project's state, the default project's included,
  is under `projects/`. A DAG root may still lie inside `paths.data_dir` in
  a directory that holds no state, as `paths.dags_dir: /data/dags` does in
  the Helm chart.
- `dag_discovery` settings in `config.yaml` apply to every project's DAG
  directory, as they do to the default project. Discovery never follows a
  symbolic link out of a project's DAG directory, whatever
  `dag_discovery.symlinks` says, so one project never reads another's DAGs.
- When one of `paths.dags_dir`, `paths.projects_dir`, and
  `paths.alt_dags_dir` lies inside another, discovery in the outer one
  skips the inner one, so each DAG file belongs to one project or the
  library.
- A DAG directory never shares a directory with state. The DAG roots are
  `paths.dags_dir`, `paths.projects_dir`, and `paths.alt_dags_dir`; the
  state roots are `<data_dir>/projects/`, each path set apart above, and
  the directory of each server-wide store. Every server, scheduler,
  coordinator, and CLI process refuses to start, and names the paths, when
  a DAG root is or contains `paths.data_dir`, when it is, contains, or
  lies inside a state root, or when two DAG roots are the same directory.
  `dagu migrate workspaces` lists each overlap instead of refusing. An
  install whose `paths.data_dir` is its Dagu home, as in the Helm chart,
  sets `paths.projects_dir`, since the default would be
  `<data_dir>/projects/`. The v3.0 Helm chart sets it.
- The default project's DAG directory is `paths.dags_dir`, and its base
  config is the file at `paths.base_config`, which v2 applied to every DAG
  as the global base config. Its wiki stays where v2 kept it:
  `paths.wiki_dir` when set, else the deprecated `paths.docs_dir` when
  set, else `dags_dir/docs` when that exists and `dags_dir/wiki` does not,
  else `dags_dir/wiki`. Its DAG files, names, and URLs do not change; its
  state lives in `<data_dir>/projects/default/` like every project's.
- Settings that place another per-project store, `paths.queue_dir`,
  `paths.suspend_flags_dir`, `paths.proc_dir`, `paths.dag_state_dir`,
  `paths.webhooks_dir`, `paths.views_dir`, and `paths.event_store_dir`,
  only tell migration where v2 kept that data. v3.0 does not use them
  afterwards, and warns at startup when `paths.queue_dir`,
  `paths.suspend_flags_dir`, or `paths.proc_dir` is set.
- Project names follow [Names](#names).
- The server knows its projects only from the registry in
  `paths.data_dir` ([Identity and references](#identity-and-references)).
  A directory under `paths.projects_dir` is a project's DAG directory only
  when a project of that name is registered, so a checkout's `scripts/`,
  `docs/`, or `.git/` never becomes one.
- A DAG file belongs to the project whose DAG directory holds it. The one
  exception is a v2 workspace's file, which runs in place as its
  project's until the operator moves it ([Migration](#migration)).

### Names

A project name is a directory name, so it must work as one on macOS,
Windows, and Linux.

- It matches `^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`. Run files nest several
  levels below the project directory, and many Windows tools stop at 260
  characters in a path. A name never starts with `-`, so `--project` never
  reads it as a flag, or with `_`, which API paths keep for their own
  segments.
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
| Server | `config.yaml`, library DAG files (`paths.alt_dags_dir`), business calendars, the project registry, v2 webhook URLs that migration kept, users and their grants, API keys, OIDC and proxy authentication, the encryption key, license, upgrade check, Dagu Cloud connection with its report and remote access state, remote nodes, coordinator and worker state (dispatch tasks, leases, active distributed runs, worker heartbeats), service registry, the scheduler lock, pause flag, and restart backoff, audit log, admin logs, notification channels and notification settings, incident providers, queues defined in `config.yaml` and the runs admitted to them |
| Project | DAG files, base config, runs, logs, artifacts, run work directories, git sync settings and clone, process heartbeats, run events, DAG state, suspend flags, DAG settings, pins, views, scheduler state and checkpoints, watch state, slot index, backfills, materializations, agent session cleanups, secrets, profiles, notification routes, incident policies and incidents, wiki, per-DAG webhooks and their public URLs, node inventories ([RFC 012](012-node-inventory.md)), other queues |

A store added later is per project unless it holds identity, authentication,
licensing, or the state of the processes and workers that serve every
project. A setting a workflow runs with is always per project, except
business calendars: holidays are the same for every team, so the server
keeps one set.

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
  output, and artifacts relative to the project's log or artifact
  directory, which may lie outside its state directory when
  `paths.log_dir` or `paths.artifact_dir` is set, so moving a DAG leaves
  them valid. Records written before v3.0 hold
  absolute paths, and migration does not rewrite them: a project maps a
  path under its v2 directories to the new place when it reads the record.
  A later move of the DAG rewrites them. A path a DAG sets outside the
  project's directories stays absolute and does not move.
- **Reads across projects.** Most pages show one project. Search and the
  All projects view read the index of each project the user can see, a few
  at a time, and merge the results. They run only when a user asks, never
  on a tick, so their cost grows with the projects a user can see, not with
  time. No server-wide index copies the projects' indexes.

### Base config

A DAG's base config is its project's `base.yaml`, then the DAG file. No
server-wide base config applies. The default project's base config is the
file at `paths.base_config`, which v2 applied to every DAG as the global
base config. A migrated project reads v2's global, workspace, DAG order
until it has a `base.yaml` of its own ([Migration](#migration)), so its
DAGs keep the base config they ran with. A child started with `dag.run` runs in its caller's
project, so it gets that project's base config without carrying provenance.
This holds whether the child is in the caller's file, in its own file, or in
the library.

### Settings

A project owns its base config, secrets, profiles, notification routes, and
incident policies, and inherits none of them from the server or from
another project. v2's global entries become the default project's. A change
to one project's settings never changes what another project's DAGs run
with. The one exception is a migrated project without a `base.yaml` of its
own, which reads the default project's base config first
([Base config](#base-config)).

| Setting | A project's DAG uses |
| --- | --- |
| Secret | The project's secret of that name |
| Profile | The project's defaults, then the selected profile. A profile named by a run resolves in the project |
| Notification routes | The project's routes, which send to server-wide channels |
| Incident policies | The DAG's policy, then the project's, the DAG's inheriting the project's as in v2 |

- A credential that every project uses belongs in a secret provider
  ([spec 069](../specs/069-secrets-providers.md)), which each project's DAGs
  reference. Rotating it there reaches every project at once.
- Other settings that several projects share are set in each of them.
- Notification channels and notification settings, such as SMTP, stay
  server-wide, and only admins change them, since a channel can run a
  command on the server. A project's routes name channels, and its users
  see a channel's name and type, never its URL or secret.
- Incident providers, such as PagerDuty, stay server-wide in the same way.
  A project's incident policies name providers, and its users see a
  provider's name and type, never its credentials.
- Editing a project's settings needs the role in that project that editing
  a workspace's needs in v2. A server-wide role covers the default project,
  so whoever edits global entries on v2 edits the default project's.

### Identity and references

Each project has an ID, a random UUID assigned when the project is created. A
project deleted and created again under the same name
gets a new ID, so its grants, audit trail, and Dagu Cloud history never mix
with the old one's.

- A server-wide registry in `paths.data_dir` holds each project's ID, name,
  display name, creation time,
  [Dagu Cloud binding](002-dagu-cloud-connector.md#binding), and, for a
  migrated project, the v2 workspace name it came from. It is the only
  list of projects: creating, deleting, and migration change it, and
  nothing on disk adds to it. Every write to it holds one lock and checks
  the name rules before it stores. Writes are rare, so one lock costs
  nothing.
- Every server, scheduler, and coordinator process checks the registry for
  changes every 5 seconds, since NFS and other shared storage send no
  notice of another host's write. When it changed, the process starts or
  stops using projects to match it, so a project created through one
  process reaches the others without a restart. Every write reaches every
  process, also when two writes land within one tick of the file system's
  modification time, so the check does not rely on that time alone.
- Each of a project's state directories, one under `paths.log_dir` or
  another path set apart included, records the project's ID and name. A
  state directory whose ID the registry lacks, as after a registry restored
  from an older backup, is listed on the projects page for an admin to
  restore or delete. It is never attached to a project by its name.
- Records that outlive a request refer to a project by ID: grants, audit
  entries, dispatch tasks, leases, and status reports. Names are for people:
  `--project`, the API's `project` parameter, and `project_mappings` take a
  name and resolve it to an ID.
- The default project gets its ID at the first v3.0 start.
- A DAG is identified by its project and name. Names are unique within a
  project.
- `dag.run` resolves its target in the caller's project, then in the
  library. A path target must lie inside the caller's project DAG directory
  or the library once symbolic links resolve, or be a file that runs in
  place as that project's ([Migration](#migration)). A target in another
  project is a load error.
- A CLI command addresses the project named by `--project`, else by
  `DAGU_PROJECT`, else the project whose DAG directory contains the
  current directory, else `default`. A DAG file given by path must be the
  addressed project's, in its DAG directory or running in place as its,
  or be in the library. Any other file is refused, and the error names
  the project it belongs to.
- API paths and MCP tools that address DAGs, runs, secrets, profiles, routes,
  policies, base config, or wiki pages take `project`. The default is
  `default`, so existing clients keep working against the default project.
  A request that names a project the caller cannot see gets `404`, as for
  a project that does not exist. A request for a DAG the project lacks
  gets `404`, and the error names the projects the caller can see that
  have a DAG of that name.
- A DAG in a subdirectory of its project, such as `etl/daily.yaml`, is
  named `daily` when `dag_discovery.recursive` finds it, as v2 names it, so
  migration changes no DAG name.
- A per-DAG webhook URL is `/webhooks/{project}/{name}`. The URL selects
  the DAG, and the webhook's token or signature authenticates the request,
  as in v2.
- `/webhooks/{name}` reaches the DAG that migration placed in a project
  under that name while it exists, so callers outside Dagu need no change,
  and otherwise the default project's DAG, as in v2. A v2 server has one
  DAG of each name, so each such URL reaches one DAG. The v2 URL of a
  library DAG stops working.
- `/projects` lists and creates projects. The `/workspaces*`,
  `/settings/workspaces/*`, `/notification-routes/workspaces/*`,
  `/incident-policies/workspaces/*`, `/profiles/_workspaces/*`,
  `/notification-routes/global`, `/incident-policies/global`, and
  `/profiles/_global/*` paths and the `?workspace=` parameter are removed.
  A client that wrote v2's global entries there gets an error instead of
  writing only the default project's.
- Local runs start their child process with `--project`. `DAGU_HOME` stays the
  server root, so the child still reads `config.yaml`.
- Every step that runs on the server runs with `DAGU_PROJECT` set to its
  run's project, so a `dagu` command in a step addresses that project
  whatever its working directory. A step on a worker does not get it: its
  `dagu` commands read the worker's own Dagu home, as in v2, which has no
  projects.
- `dagu test` with no argument selects the test files under the addressed
  project's DAG directory and beside its files that run in place. A test
  file given by path runs in the project of the DAG it tests.
- Audit entries record the project's ID and its name at the time. Old
  entries keep `workspace`.

### Git sync

`git_sync` in `config.yaml` syncs `paths.dags_dir`, the default project's
DAG directory, as in v2. Every other project may have a git sync of its
own, with the same pull, push, auto sync, and conflict handling.

- An admin sets a project's repository, branch, path in the repository,
  and credentials, a token or an SSH key, in the UI or API. They are kept
  with the project's settings, and the credentials are encrypted with the
  server's key. The repository decides what code runs with the project's
  secrets, so only an admin sets it.
- Users who may edit the project's DAGs pull and push its files, as they
  do with `git_sync`. API paths for git sync take `project`.
- A project's git sync keeps its clone in the project's state directory and
  copies files to and from `<projects_dir>/<name>/`, as `git_sync` keeps
  its clone in `paths.data_dir` and copies files to and from
  `paths.dags_dir`. The DAG directory never holds `.git`.
- Several projects may sync one repository, each from its own path, so one
  repository can hold every team's directory. Each keeps its own clone.
- Each file has at most one sync. When a project syncs a path of the
  repository that `git_sync` syncs, `git_sync` leaves that path out, and
  when `paths.projects_dir` lies inside `paths.dags_dir`, `git_sync` leaves
  `paths.projects_dir` out.

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
  the library DAG's history. API paths, URLs, and CLI commands address a
  library DAG in a project with `library` beside `project`, so its runs
  stay reachable after a project DAG takes its name.
- A project DAG shadows a library DAG of the same name. `dag.run` finds the
  project's DAG, and an explicit start of the library DAG in that project
  is refused, since the name there addresses the project's own DAG.
- A library DAG that sets no `working_dir` has its file's directory as its
  DAG working directory ([specs 013](../specs/013-step-run.md) and
  [035](../specs/035-file-dependencies.md)), as in v2, whichever project
  calls it.
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
  its own repository with [git sync](#git-sync), or limits each project's
  directory to its team with code owners, and limits `paths.alt_dags_dir`
  to admins.
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
  becomes a project only when an admin creates a project of its name. A new
  project starts with no secrets, profiles, routes, or policies, and with
  the base config its directory holds, if any. Create is refused while a
  state directory of that name records another ID, until an admin
  restores or deletes it on the projects page.
  - Create is also refused when the DAG directory would be, or contain, a
    protected path: a directory or file that another `paths` setting
    names, whether set or defaulted, the default project's wiki
    directory, `paths.base_config`, or `config.yaml`. Deleting a project
    then never removes another project's, the library's, or the server's
    files.
- **No rename.** A project's name never changes; see [Names](#names). An
  admin changes the display name in the UI or API at any time, and only
  the registry changes.
- **Delete.** An admin deletes a project in the UI or API, or with `dagu
  project delete`. Delete marks the project as deleting in the registry.
  Each server, scheduler, and coordinator process that sees the mark
  stops starting, admitting, or dispatching runs of the project, closes
  its stores, and records that in its service registry entry, since
  Windows cannot remove a directory with open files. Every server,
  scheduler, and coordinator process keeps an entry in the service
  registry while it runs. Once every live process has recorded that,
  delete checks the project's runs. While any
  is queued, in its own queues or a server-wide one, running, or
  waiting, delete is refused and the mark removed. Otherwise it removes
  the DAG directory, the state directories, grants, each API key left
  with no role, and Dagu Cloud binding, and last the registry entry. A
  process that starts while a project is marked deleting finishes the
  delete.
  - While a git checkout tracks files in the DAG directory, delete is
    refused: the checkout owns those files, so they are removed there
    first. A checkout that tracks nothing there, such as a dotfiles
    repository in the home directory, does not block delete.
  - Delete is also refused while the registry records a file that runs in
    place as the project's ([Migration](#migration)), whether or not the
    file exists now. Left behind without its record, the file would run in
    the default project.
  - Delete is also refused while the DAG directory is or contains a
    protected path.
- **Changed on disk.** A project whose DAG directory disappears outside
  Dagu is listed as missing and is not scheduled. It keeps its registry
  entry, grants, binding, and state until the directory returns or an
  admin deletes it. A directory renamed outside Dagu is not a project until
  an admin creates one of its new name, since names never change. A
  project with files that run in place is not missing while those files
  exist, and keeps running them.
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
- A unit runs a periodic task only while its project has work for it: the
  restart scanner while a DAG has a restart policy, the backfill
  dispatcher while a backfill is open, and the folder watcher while a DAG
  watches a folder. The tick planner saves its watermarks only when one
  changed. A periodic task added later follows the same rule, so an idle
  project costs little ([Performance](#performance)).
- The supervisor owns each queue defined in `config.yaml`. Items from every
  project wait in one server-wide store in `<data_dir>/server/`, and the
  supervisor admits them oldest first under the queue's limit, as v2's
  single queue did, then hands each run to its project's unit. It keeps
  the runs it admitted to each queue in the same store, and a unit
  releases one when its run ends, so admission reads one store and no
  project's process heartbeats. After a restart, the supervisor checks
  each admitted run against its project once. Other queues are per
  project, and their unit processes them.
- A queue page lists the runs of the projects the user can see, and counts
  the others without naming them, since they share the queue's limit.
- No cap limits concurrent runs across every project, as none does in v2.
  A queue defined in `config.yaml` limits the DAGs of every project that
  use it, and each worker's `max_active_runs` limits what it runs.
- One file watcher covers `paths.projects_dir` and routes events by path.
  It routes a file that runs in place ([Migration](#migration)) to the
  project the registry records for it.
  A watcher per project would reach the Linux inotify instance limit (128
  by default) on servers with many projects.

## Distributed execution

- Dispatch tasks, leases, and status reports carry the project ID. The
  coordinator records attempts in that project's run store.
- Tasks already carry base config content and the profile name. Both now come
  from the task's project. Profile and secret values resolve from the task's
  project.
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
  DAG's working directory.
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

v3.0 makes each workspace a project on its first start, before it
schedules or serves anything. `dagu migrate workspaces` prints what that
start would do, including each item that needs attention, and changes
nothing, so it can run against a live v2 install,
or a copy of one, before the upgrade.

- Stop every v2 server, scheduler, and coordinator before the first v3.0
  start. Workers may keep running, as
  [Distributed execution](#distributed-execution) says. The first v3.0
  process to start for a `DAGU_HOME` migrates under a lock in
  `paths.data_dir`, and other v3.0 processes wait for it.
- Downgrading after migration is not supported. Back up `paths.data_dir`
  and the DAG directories before the upgrade, and restore them to go back
  to v2.
- Migration waits while a v2 scheduler or coordinator is in the service
  registry, and logs what it waits for. A v2 server has no service
  registry entry, so migration cannot detect one; stopping it is the
  operator's step. Migration then cancels every run whose
  process is still live, as `dagu stop` does, and moves a run's files
  only after its process exits. A run on a worker is marked cancelled,
  and the coordinator stops it when the worker next connects.
- Runs waiting in a queue keep their order. Those in a queue defined in
  `config.yaml` move to `<data_dir>/server/`, and the others to their
  DAG's project.
- A process killed during migration resumes it on its next start. While
  migration waits or runs, the health endpoint of every v3.0 process,
  including one that waits for another to migrate, answers 200 and reports
  progress, and other requests get 503. An orchestrator then neither
  restarts a large install midway nor holds a rolling update, in which the
  v2 process stops only once its v3.0 replacement reports healthy.
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
| DAG labelled `workspace=X` | Project `X`'s DAG. Its file stays where it is and runs in place; its runs, logs, artifacts, DAG state, suspend flags, and DAG settings move to `<data_dir>/projects/X/` |
| DAG without a `workspace` label | Stays in the default project. Its file stays in `paths.dags_dir`; its runs, logs, and state move to `<data_dir>/projects/default/` |
| DAG under `paths.alt_dags_dir` | Stays in place as a library DAG |
| Global base config (`paths.base_config`) | The default project's base config, at the same path |
| `<dags>/workspaces/X/base.yaml` | Stays where it is. Project `X` reads the global base config, then this file, as v2 did, until `<projects_dir>/X/base.yaml` exists |
| Secrets and profile entries of `X` | Project `X`'s secrets and profiles, still encrypted with the server's key |
| Notification routes and incident policies of `X` | Project `X`'s routes and policies |
| Global secrets, profiles, profile defaults, notification routes, and incident policies | The default project's. Project `X` also gets a copy of each one it inherited on v2: every global secret, profile, and profile default, the global routes when `X`'s route set has `inheritGlobal`, and the global policies when `X`'s policy set has `inheritParent`. An entry of `X`'s own with the same name stays as it is. Both flags are then dropped |
| Notification channels, notification settings, and incident providers | Stay server-wide, as on v2 |
| Per-DAG wiki directory `<wiki>/X/<dag>`, pages under `<wiki>/X/` | Project `X`'s wiki, read and written where it is |
| User or API key with roles in `X` | Grants on project `X` |
| User or API key with `workspace_access.all: false` | No server-wide role, and `viewer` on the default project, which keeps v2 read access to unlabelled DAGs |
| `workspace_mappings`, `default_workspace_access` | Read as `project_mappings`, `default_project_access` until v3.1. A workspace name that migration changed reads as its project's name |
| `workspace` label | Kept as an ordinary label, apart from two rules below |

- Migration moves no DAG file, base file, or wiki page, and edits none.
  It writes the registry and moves state. A DAG outside the library that
  ran on v2 runs on v3.0 from the same file, in the same working
  directory, with the same base config. The exceptions, which the report
  lists, are a DAG that v2 found through a symbolic link out of
  `paths.dags_dir`, a DAG with an unsettled `workspace` label, a later
  document of a file with several documents, and a workspace DAG whose
  name a DAG in `<projects_dir>/X/` already has.
- A schedule slot gets the same run ID on v3.0 as on v2, so a slot that
  ran before the upgrade does not run again after it.
- Each workspace DAG file runs in place. The registry records its path as
  project `X`'s, and `X` lists, schedules, and runs it under the same
  name, with `X`'s settings, runs, and state. No other project reads it.
  Migration records only the files it found: a file added later belongs
  to the directory that holds it.
- The operator moves a file to `<projects_dir>/X/` at any time, by hand
  or in git. It is then `X`'s by its directory, keeps its name and
  history, and its record ends. While both files exist, as when two git
  syncs bring the move one after the other, the file in
  `<projects_dir>/X/` wins and the record ends. This also holds when the
  file in `<projects_dir>/X/` exists before migration runs, and the
  preview and the report list the pair.
  A record stays while its file is absent, as during a `git pull` or an
  editor's save that replaces the file. The DAG is then not listed or
  scheduled, and runs in place again when the file returns. A record ends
  only when `<projects_dir>/X/` gets a DAG of the same name, or when an
  admin ends it on the projects page, which lists each record whose file
  is absent.
  A DAG that sets no `working_dir` has its file's directory as its DAG
  working directory ([specs 013](../specs/013-step-run.md) and
  [035](../specs/035-file-dependencies.md)), so moving it changes where
  it runs; the projects page says so beside each file that runs in place.
- After migration, a `workspace` label places nothing, and apart from two
  rules it is an ordinary label. A default-project DAG whose `workspace`
  label names a migrated project, by its name or by the v2 workspace name
  it came from, does not load, and the error names the project. A
  recorded file renamed in git, or a file added by v2 habit, would
  otherwise run with the default project's settings and readers. Moving
  the file to `<projects_dir>/X/` or removing the label fixes it. The
  other rule is the record of an unsettled label below. Both rules read
  only the first document of a file; a later document's `workspace` label
  is ordinary.
- A `workspace` label is unsettled when its value is not a valid v2
  workspace name, when it has two different values, or when it names
  neither a v2 workspace nor a project that migration makes, as after a
  typo. On v2, only users with access to every workspace saw such a DAG.
  On an install that had workspaces, migration records in the registry,
  in a server-wide list, each file in `paths.dags_dir` whose first
  document has an unsettled label. A recorded file does not load, and the
  error names the label: in the default project, its DAG would be visible
  to every user with a role there. Its past runs move to the default
  project. The record stays while the file is absent, and ends when the
  file's first document has no `workspace` label, when the file moves into
  a project's DAG directory, or when an admin ends it on the projects
  page. Installs without workspaces have no such records, and a file
  added or labelled later is an ordinary default-project DAG unless its
  label names a migrated project.
- In a file with several documents, the first document decides the file's
  project: a migrated project when it carries that project's label, else
  the default project. A later document runs, as a child, in its caller's
  project with that project's base config, like any child in its caller's
  file. On v2 it loaded with its own label's base config.
- While project `X` has no `<projects_dir>/X/base.yaml`, it reads its
  base config in v2's order: the file at `paths.base_config`, then `X`'s
  v2 base file, then the DAG file. A change to the default project's base
  config then still reaches `X`. Once `<projects_dir>/X/base.yaml`
  exists, it alone is `X`'s base config, and the projects page lists the
  global settings to add to it.
- Project `X`'s wiki is its v2 directory until the operator moves it to
  `<projects_dir>/X/wiki/`, and the default project's wiki leaves that
  directory out. Once `<projects_dir>/X/wiki/` exists, `X` uses it alone.
- `git_sync` keeps syncing `paths.dags_dir`, the files that run in place
  included, as on v2. To move such a file, an admin gives project `X` a
  git sync on a path of its own, and the file moves there in the
  repository.
- Per-DAG webhook URLs keep their paths and tokens, and reach the DAG in its
  project, as [Identity and references](#identity-and-references)
  describes.
- Before it copies a state directory to another file system, migration
  checks free space there. When there is not enough, it moves nothing and
  waits: the health endpoint names the path and the space it needs, and
  migration starts once there is room. The process never exits into a
  restart loop.

The report lists these items, which migration cannot settle alone:

- Each run that migration cancelled, so the operator can retry it. The
  preview lists the runs that are live when it runs.
- Each project's files that run in place, and whether it reads its base
  config in v2's order.
- Each global entry copied into a workspace's project. Later changes to the
  default project's entry, including by a script that wrote v2's global
  entries, no longer reach the copies. A credential that every project uses
  can move to a secret provider instead.
- A workspace name that breaks the [name rules](#names), including
  `default` and two names that differ only in case. The project gets a
  valid name made from it, as the UI suggests one, and keeps the workspace
  name as its display name. When that name is taken, a number is added to
  it. The preview shows these names before the upgrade, and
  `workspace_mappings` that name the workspace keep granting the same
  access until v3.1.
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
- A DAG that v2 found through a symbolic link out of `paths.dags_dir`.
  v3.0 does not follow the link, so the DAG is no longer listed or
  scheduled; moving its file into a DAG directory runs it again.
- A library DAG with a `workspace` label. On v2 only that workspace's
  users could read it; on v3 every user can read library DAGs. Moving its
  file into the project keeps it private when no other project calls it.
- Each `dag.run` target that contains a variable, such as
  `${JOBS_ROOT}/facilities/${ITEM}`, since migration cannot resolve it. At
  run time, a target found in neither the caller's project nor the library
  fails the step with a load error that names both.
- Each file recorded for an unsettled `workspace` label, which does not
  load until its label is removed or it moves into a project. A user with
  a role in the default project can read its past runs.
- Each later document of a file whose `workspace` label differs from the
  first document's, since its base config changes.
- Each workspace DAG whose name a DAG in `<projects_dir>/X/` already has,
  so the file in `<projects_dir>/X/` runs in its place.

```text
dagu: made 2 workspaces into projects
  billing        12 DAGs, base config, 3 secrets, 1 incident policy
  ops            4 DAGs; workspace "-ops", which is not a valid name
needs attention:
  dag.run        billing/report calls sim/score in another project
  global copy    secret DB_PASS copied into billing and ops
  cancelled      2 runs that were still running
```

## Performance

Cost grows with the number of projects, so an idle project adds little.

- **Periodic tasks.** Each unit runs the current scheduler's periodic
  tasks on its own timers ([Scheduler](#scheduler)). A waiting timer costs
  nothing; the cost is each tick's reads of the project's stores. The
  restart scanner lists the run event directories every second, and the
  backfill dispatcher reads open backfills every 10 seconds, so a unit runs
  them only while its project uses them. An idle project's queue processor
  and retry scanner then read its stores every 30 seconds each, and its
  zombie detector every 45 seconds: 100 idle projects add about nine reads
  a second. Each project with a restart policy adds two directory reads a
  second. The folder watcher reads only the folders a DAG watches, and
  the tick planner writes its watermarks only when one changed, so
  neither costs an idle project a read.
- **Server-wide queues.** Admitting a run reads the queue's store in
  `<data_dir>/server/` ([Scheduler](#scheduler)), so its cost does not
  grow with the number of projects.
- **Dagu Cloud reports.** A connected server's reporter reads each
  project's new run events once per report
  ([RFC 002](002-dagu-cloud-connector.md#cadence)): every 60 seconds, and
  at most every 5 seconds while runs change. 100 projects add about two
  reads a second, and at most 20. A server that is not connected reads
  nothing.
- **Size.** The project ID adds 36 bytes to each dispatch task, lease,
  status report, and audit entry. A run record gains nothing, since its
  directory places it in its project.
- **Memory.** Each project's caches keyed by DAG or run keep the bounds
  the default project's caches have today, so each project adds at most
  one set of those bounds.
- **SSE.** Topic keys gain the project ID, so `etl` in two projects never
  shares a topic, and payloads keep their size. A page subscribes only to
  the topics of the project it shows.
- **File watching.** One watcher covers every project
  ([Scheduler](#scheduler)).
- **Git sync.** A project without git sync costs nothing. Each project with
  auto sync fetches its repository once per interval, 300 seconds by
  default, so 100 such projects fetch about once every three seconds. Each
  clone takes the disk its repository takes, also when several projects
  sync one repository.
- **Registry.** Each server, scheduler, and coordinator process stats
  `projects.json` every 5 seconds: one stat, whatever the number of
  projects. It reads the file only when it changed.
- **Reads across projects.** Search and the All projects view cost one
  index read per visible project ([Storage](#storage)).
- **Migration.** No DAG file moves. Run, log, and state directories move
  by rename, on every install, since the default project's state moves
  too. A directory on
  another file system, such as one that `paths.queue_dir` placed, is
  copied. No run record is rewritten ([Storage](#storage)), so the first
  start costs one rename per moved directory, not one write per stored
  run.

## Compatibility and rollout

| Release | Ships |
| --- | --- |
| v2.22 | Warnings and an admin notice that list workspace settings in use. |
| v2.28 | The warning and notice say that v3.0 makes workspaces projects on its first start, and stop asking operators to move settings by hand. |
| v3.0 | Projects with IDs, names, and display names; settings owned by each project, with v2's global settings in the default project; store scopes and relative run paths; the default project's state in `projects/default/`; create and delete; migration on first start, with its preview `dagu migrate workspaces`; report protocol 3 (RFC 002); git sync per project; compose files that mount `./projects` beside `./dags`, and a Helm chart that sets `paths.projects_dir` and drops the per-project paths v3.0 no longer reads. |
| v3.1 | Drops the `workspace_mappings` and `default_workspace_access` aliases. |
| After v3.0 | Moving DAGs between projects; the All projects view; Dagu Cloud bindings (RFC 002). |

v2 gets no new workspace features.

These specs define workspace behavior, and v3.0 rewrites them for projects:

- [Spec 020](../specs/020-mcp-server.md) and
  [spec 021](../specs/021-mcp-read-tool.md): the `workspace` parameter
  becomes `project`, and wiki URIs become `dagu://wiki/{project}` and
  `dagu://wiki/{project}/{path}`.
- [Spec 018](../specs/018-parallel-and-foreach.md): the nearest
  `defaults.max_concurrent` is the DAG's, then its project base config's.
- [Spec 034](../specs/034-wiki-page-format.md): a wiki link resolves in the
  page's project.
- [Spec 042](../specs/042-ssh-run.md): a `{ secret: <ref> }` resolves in the
  DAG's project only.
- [Spec 069](../specs/069-secrets-providers.md): provider settings that
  come from workspace configuration come from the project's.
- [Spec 070](../specs/070-runtime-profiles.md): global and workspace profile
  defaults become one set of project defaults, applied to every DAG of the
  project instead of by `workspace` label, and named profiles belong to a
  project.
- [Spec 086](../specs/086-backfill.md): a backfill summary goes to the
  project's routes, and dismissing its notice needs the operator role in
  the DAG's project.
- [Spec 089](../specs/089-node-targeting.md): the rule that a DAG with a
  `workspace` label cannot target nodes is dropped, since each project has
  its own inventories.

## Verification and acceptance criteria

Criteria 23 and 24 apply from the release that ships the feature.

1. A v2 data directory with no workspaces starts on v3.0 as the default
   project, with no manual step outside the four exceptions that follow
   [Goals](#goals). Its DAG files and URLs do not change, and its runs,
   logs, and settings move to `<data_dir>/projects/default/` and open as
   before.
2. After migration, each DAG outside the library runs with the same base
   config, secrets, profile, notification routes, and incident policies, and
   in the same working directory, as on v2. The exceptions are a DAG that
   v2 found through a symbolic link out of `paths.dags_dir`, a DAG with an
   unsettled `workspace` label, a later document of a file with several
   documents, and a workspace DAG whose name a DAG in `<projects_dir>/X/`
   already has. The report lists each of them, and every library DAG
   whose base config changes.
3. After migration, each user and API key can see and do what it could on v2,
   and no more, except that every user can read a library DAG that carried
   a `workspace` label, which the report lists, a user limited to
   workspaces can read default-project DAGs through MCP, as through the UI
   on v2, and a user with a role in the default project can read the past
   runs of a DAG with an unsettled `workspace` label.
4. A user without a role in a project gets `404` for its DAGs, runs, logs, and
   wiki pages, and does not see the project in lists or search.
5. `dag.run` to a DAG in another project fails at load.
6. One scheduler process schedules every project. A newly created project
   starts scheduling without a restart.
7. A distributed run of a project DAG records its status in that project.
8. The first v3.0 start migrates every workspace with no manual step
   outside the four exceptions that follow [Goals](#goals), waits while a
   v2 scheduler or coordinator is live or space is short, cancels every run
   still running, resumes after being killed, answers 200 on its health
   endpoint throughout, and reports every item that needs attention, each
   cancelled run included.
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
    `projects/`, Dagu keeps only server-wide state in `paths.data_dir`; a
    directory there that holds no state may hold a DAG root. With
    `paths.log_dir`, `paths.artifact_dir`, `paths.dag_runs_dir`, or
    `paths.dag_run_work_dir` set, that part of its state lives there, in
    `.projects/<name>/`.
14. Migration moves and edits no DAG file, base file, or wiki page. Each
    workspace DAG runs in place in its project, under its v2 name, in its
    v2 directory, with its v2 base config, and its runs and state move.
15. A project name that breaks the name rules is refused at creation.
16. No UI, API, or CLI path changes a project's name. Delete is refused
    while the project has runs queued, running, or waiting, and for the
    default project.
17. A project directory that disappears outside Dagu is listed as missing
    and not scheduled, and keeps its grants and state until the directory
    returns or an admin deletes it.
18. Every project's secrets decrypt with the server's key, and no project
    directory holds a key of its own.
19. A project DAG uses only its project's base config, secrets, profiles,
    notification routes, and incident policies. Changing one project's
    settings leaves every other project's DAGs as they were. The one
    exception is a migrated project without a `base.yaml` of its own,
    which reads the default project's base config first.
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
25. A `dagu` command run anywhere inside a project's DAG directory,
    without `--project` or `DAGU_PROJECT`, addresses that project.
26. After migration or a move, the logs, step output, and artifacts of every
    moved run open as before.
27. Every step that runs on the server runs with `DAGU_PROJECT` set to its
    run's project, and a `dagu` command in it addresses that project from
    any working directory. A step on a worker does not get the variable.
28. A v3.0 coordinator gives an older worker only default-project tasks,
    and both say why it gets no project task.
29. A default-project DAG whose `workspace` label names a migrated project,
    by its name or by its v2 workspace name, does not load, and the error
    names the project.
30. `/webhooks/{project}/{name}` reaches that project's DAG. Every v2
    webhook URL of a DAG outside the library reaches the same DAG after
    migration, and no v2 DAG name changes.
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
35. A file that runs in place, once moved to `<projects_dir>/X/`, keeps
    its name and run history, and its record ends.
36. A DAG file that runs in place is listed, scheduled, and started only in
    its project, and a user who could not see it on v2 cannot see it on
    v3.0. No DAG stops being scheduled at the upgrade. The exceptions,
    which the report lists, are a DAG that v2 found through a symbolic
    link out of `paths.dags_dir`, a DAG under `paths.alt_dags_dir` that v2
    scheduled because `alt_dags_dir` lay inside `dags_dir`, a DAG with an
    unsettled `workspace` label, a later document of a file with several
    documents, and a workspace DAG whose name a DAG in
    `<projects_dir>/X/` already has.
37. A secret, profile, or DAG state that a worker reads through the
    coordinator comes from the task's project, and the coordinator refuses
    a call without a project for a task of another project.
38. A project DAG added under a library DAG's name starts with no runs,
    even when the library DAG has run in that project, and the library
    DAG's runs in that project stay reachable.
39. Each of a project's state directories, one under `paths.log_dir`
    included, records its project's ID. A state directory whose ID the
    registry lacks is listed on the projects page and is not
    attached to a project by its name.
40. After migration, the default project holds v2's global settings, and
    its base config is the file at `paths.base_config`. Each workspace's
    project holds a copy of every global entry it inherited on v2 and has
    no entry of its own name for, its DAGs run with every base config
    setting they inherited on v2, and the report lists each copied entry.
41. Admitting a run to a queue defined in `config.yaml` reads the queue's
    store in `<data_dir>/server/` and no project's process heartbeats.
42. A project with no restart policy and no open backfill runs neither the
    restart scanner nor the backfill dispatcher.
43. A user who can see a queue defined in `config.yaml` sees only the runs
    of projects the user can see.
44. A project with git sync pulls from and pushes to its own repository
    and path, keeps its clone in its state directory, and leaves no `.git`
    in its DAG directory. Only an admin sets its repository and
    credentials.
45. After migration, `git_sync` syncs the files that run in place as on
    v2.
46. When `paths.projects_dir` and `paths.dags_dir` are on different file
    systems, the server warns at startup and on the projects page.
47. `dagu test` with no argument inside a project's DAG directory runs
    that project's test files and no other project's.
48. Notification channels, notification settings, and incident providers
    are server-wide, and only admins change them. A project's routes name
    channels and its policies name providers, and no project user sees a
    channel's URL or secret or a provider's credentials.
49. A project created or deleted through one process reaches every other
    server, scheduler, and coordinator process without a restart, also
    on NFS. Delete removes nothing while a process still
    uses the project's stores, and no run of the project starts while
    delete is in progress.
50. A CLI command given by path a DAG file that is neither the addressed
    project's nor in the library refuses it and names the file's project.

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
- **Wait for running runs to finish.** An earlier revision of this RFC.
  One long run would keep every project unscheduled and every request
  failing until it ended.
- **Keep resolving moved DAGs for clients that omit `project`.** A second
  resolution path, with warnings and an admin page, for a one-time move. The
  error that names the DAG's projects shows the fix.
- **Copy shared child DAGs into each project.** Copies drift apart, and tests
  no longer run the files that production runs.
- **Merge projects that share child DAGs.** Brings back the coarse access
  that projects replace.
- **A server layer that every project inherits.** Secrets, profiles,
  routes, policies, and base config kept once for every project. A setting
  then has two possible owners: a request without `project` can mean the
  server layer or the default project, a v2 script that wrote global
  entries writes the one it did not mean without an error, and every page
  must show what a project inherits. A secret provider keeps a credential
  that every project uses in one place instead.
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
- **Projects found from `dagu.json` in v3.0.** An earlier revision of
  this RFC. A server project would have two rules for DAG names,
  discovery, and working directories, chosen by a file in its directory,
  and the CLI would keep state for projects that no server registers.
  Spec 001, which defined them, is removed.
- **Move workspace DAG files into `<projects_dir>/X/` at migration.** An
  earlier revision of this RFC. A moved DAG would run in another
  directory unless migration edited its file, a file in a git checkout or
  on another file system could not move and needed a second path anyway,
  and `git_sync` needed a list of paths to leave out. Every file running
  in place is one path, and the operator moves files when ready.
- **Notification channels per project.** A channel can run a command on
  the server, so only an admin can own one. v2 and Dagu Cloud keep
  channels in one place, and migration then has nothing to copy.
- **Matching projects across servers by name.** A guess: two servers may
  use one name for unrelated work, and macOS and Windows ignore the case
  that Linux keeps.
- **An encryption key per project.** Keeps a copied directory's secrets
  unreadable elsewhere, but migration must re-encrypt every secret, and each
  key is one more file to back up.
- **Keep the default project's state at v2's paths.** Installs without
  workspaces would move nothing, but `paths.data_dir` would mix server-wide
  state with the default project's, and every store would need two
  layouts.
- **Move the default project with a command the operator runs.** Until it
  runs, v3.0 would have to serve both layouts, and refusing to start
  without it would leave an upgraded image restarting in a loop.
- **Projects inside `dags_dir` by default, under the one `git_sync`.** One
  repository would hold every project, but the default project's directory
  would contain every other project, and whoever could write it or its
  repository could write them all. Git sync per project gives each team
  its own repository, and one repository can still hold every project by
  path.
- **A server-wide cap on concurrent runs.** v2 has none, and projects add
  no runs. Queues defined in `config.yaml` and each worker's
  `max_active_runs` already bound the load.
- **A server-wide index for reads across projects.** A second copy of every
  project's index, kept consistent through crashes, migration, moves, and
  deletes, for reads that happen only when a user asks.

## Consequences

- Multi-team installs keep roles per group, visibility limits, and base
  config, with membership fixed by location.
- Every setting a workflow runs with belongs to one project. A credential
  that every project uses lives in a secret provider; other shared settings
  are set in each project.
- Upgrading needs no manual step, with or without workspaces, outside the
  cases that [Goals](#goals) lists, and a preview shows each change
  beforehand. Every install's state moves once, so tools that read v2's
  paths need the new ones. Migration moves and edits no
  DAG file, so no workflow changes at the upgrade. Runs still running at
  the upgrade are cancelled.
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
  thing on the server and in Dagu Cloud.
- Project names never change and are valid directory names on macOS,
  Windows, and Linux. Display names can be any text and change at any time.
- Every store is either per project or server-wide, and stores added later
  follow the same rule.
- Dagu Cloud tells projects apart by binding, and report protocol 3 names
  each run's project.
