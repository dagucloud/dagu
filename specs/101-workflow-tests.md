# Spec 101: Workflow Tests

## Status

Implemented.

## Scope

This spec covers:

- the `dagu test` command: its arguments, flags, report, and exit status
- where test files live, and which DAG a test file tests
- the test file and case schema, and the JSON Schema that describes it
- mocks: result fields, step fields, results per execution, and the default
  mock
- the actions that run for real under test
- the fail-closed rule, which makes a case invalid before any step runs, and
  the fix an invalid case reports
- preconditions, repeat conditions, continuous steps, and steps that wait
  for a person, under test
- mocks of sub-DAG, `parallel`, `foreach`, and node-targeted steps
- skipped waits, and retries of the run by a DAG-level `retry_policy`
- the isolation of each case: its case directory, files, live runs,
  run-level side effects, secrets, and the values it takes from the host
- the trigger type and schedule slot of a case
- assertions on run status and attempts, step status, attempts, outputs,
  streams, and what a step was called with, and the notifications a run
  would send
- the `--init` and `--update` authoring flags
- test files being skipped by DAG discovery

This spec does not define:

- scheduler, queue, coordinator, or worker behavior. A test runs the DAG
  locally, in the `dagu test` process, also when the DAG sets `queue` or
  `worker_selector`.
- the restart loop of a service DAG (Spec 080). A case is one run of the
  DAG, with the retries its DAG-level `retry_policy` makes.
- tests of `type: agent` DAGs (Spec 032)
- path outputs of `type: build` DAGs (Spec 033). A test of such a DAG is
  invalid.
- mocks of individual `foreach` body steps, per-item results of `foreach`
  and `parallel`, and mocks for the steps of a child run that a real step
  starts
- push-back of a waiting step
- seeding `state` values (Spec 068), and assertions on the samples and
  events of a continuous step (Spec 083)
- HTTP or service virtualization, and timing or performance assertions
- REST API, Web UI, and MCP surfaces
- the behavior a test exercises, such as dependencies, retries, routing,
  `continue_on`, handlers, and status propagation. Their owning specs define
  it, and a test observes it unchanged.

## Goal

A workflow author tests how a DAG behaves through the same runner as
`dagu start`, without editing the DAG and without running anything with side
effects that the test does not name. A missing mock makes the case invalid
instead of reaching a real system.

## Related Specs

- Value resolution: [Spec 003](003-value-resolution.md)
- Step output references: [Spec 007](007-value-resolution-steps.md)
- Step identity: [Spec 009](009-step-reference.md)
- Dynamic evaluation: [Spec 011](011-dynamic-evaluation.md)
- Step outputs: [Spec 012](012-step-outputs.md)
- Built-in run context: [Spec 017](017-built-in-run-context.md)
- Parallel and foreach: [Spec 018](018-parallel-and-foreach.md)
- Preconditions: [Spec 023](023-preconditions.md)
- Human tasks: [Spec 031](031-human-task.md)
- Build workflows: [Spec 033](033-build-workflows.md)
- File dependencies: [Spec 035](035-file-dependencies.md)
- Dry-run step checks: [Spec 064](064-dry-run-step-checks.md)
- Secrets providers: [Spec 069](069-secrets-providers.md)
- Repeat policy: [Spec 075](075-repeat-policy.md)
- DAG restart policy: [Spec 080](080-restart-policy.md)
- Continuous runs: [Spec 083](083-continuous-runs.md)

## Terms

A test file is a YAML file whose name ends in `.test.yaml` or `.test.yml`.

The tested DAG is the DAG file that a test file tests.

A case is one entry of a test file's `cases`. Each case is one run of the
tested DAG.

A step key names a step or a lifecycle handler of the tested DAG in `mocks`,
`real`, and `expect.steps`.

A mock is the value of a step key under `mocks`, or the default mock where
it applies.

The default mock is the case's `default_mock`, or the file's when the case
sets none. See [The Default Mock](#the-default-mock).

An execution is one run of a step's action: the first attempt, a retry, or a
repeat iteration.

A result scripts what one execution returns.

A real step is a step whose key the case lists in `real`.

A replaced step is a step whose action its mock replaces. See
[Replaced Actions](#replaced-actions).

A side-effect-free action is an action that
[Side-Effect-Free Actions](#side-effect-free-actions) lists.

A waiting step is a `human.task` step (Spec 031) or a step with `approval`.
Its wait needs values when completing it with no values would fail: a
`human.task` whose form has a `required` property without a `default`, or
an `approval` gate with `required` inputs.

A continuous step is a step whose `repeat_policy` has a `repeat` mode and no
`limit` (Spec 083).

A substituting `eval` is a `params[].eval` or a precondition `eval` that
holds command substitution in backtick or `$()` form (Spec 011). Any other
`eval` runs no command.

The case directory is the temporary directory a case runs in.

An invalid test file runs none of its cases. An invalid case runs none of its
steps.

## Behavior

### Running Tests

```sh
dagu test [flags] [<test file, DAG file, DAG name, or directory>...]
```

Each case runs the tested DAG once, through the same runner as `dagu start`,
in its own case directory. Dependencies, preconditions, routing, retries,
repeats, `continue_on`, handlers, and status propagation behave as in a real
run. Only the replaced actions, the decided conditions, the settled waits
for a person, the skipped waits, the run retries that `dagu test` starts
itself, and the isolation this spec defines differ.

Rules:

- Without `--init`, a file argument must be a test file or a DAG file. A
  DAG file is a file whose name ends in `.yaml` or `.yml` and is not a test
  file.
- A DAG file argument selects the test files beside it whose names are the
  DAG file's name without `.yaml` or `.yml`, followed by `.test.yaml` or
  `.test.yml`, whichever exist: `release.yaml`, like `release.yml`,
  selects `release.test.yaml` and `release.test.yml`. The selection goes by
  name, and each selected test file tests the DAG file that
  [Where Test Files Live](#where-test-files-live) gives it.
- An argument that is not an existing path is a DAG name. It resolves to a
  DAG file as `dagu start` resolves a DAG name: the file of that name in
  the DAGs directory of the loaded configuration, with `.yaml` appended
  unless the name ends in `.yaml` or `.yml`. `release` resolves to
  `release.yaml` there, and `etl/daily` to `etl/daily.yaml`. The DAG name
  selects the test files beside that DAG file by the rule above. An
  existing path always wins over a DAG name.
- Every flag applies to the test files that a DAG file or DAG name selects
  as it applies to a test file argument.
- A directory argument selects every test file under it, at any depth.
  Directories whose names start with `.` are skipped, and symbolic links are
  not followed.
- With no argument, `dagu test` selects every test file under the DAGs
  directory of the loaded configuration, by the same directory rule.
- A test file that more than one argument selects runs once.
- Test files run one at a time, in lexicographic order of their report paths
  (see [Report](#report)).
- The cases of a test file run one at a time, in file order.
- An invalid test file does not stop the other test files. An invalid case
  does not stop the other cases of its file.
- Selecting no test file, or no case through `--run`, is not an error. The
  report counts zero cases, and the exit status is 0.

Flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--run <regexp>` | none | Run only the cases whose `name` the regular expression matches. The expression uses RE2 syntax and may match anywhere in the name |
| `--timeout <duration>` | `5m` | Longest a case may run, as a duration such as `30s` or `5m`. It must be greater than zero |
| `--format <format>` | `text` | `text`, `json`, or `junit` |
| `--keep` | off | Keep each case directory and report its path |
| `--init` | off | Write a test file for the DAG file given as the one argument. See [Authoring Flags](#authoring-flags) |
| `--update` | off | Rewrite each case's `expect` from its run. See [Authoring Flags](#authoring-flags) |

A case that `--run` does not select is neither validated nor reported. Its
test file is still decoded, so a test file error is still reported.

### Where Test Files Live

A test file sits beside the DAG it tests:

```text
workflows/
  release.yaml
  release.test.yaml
  etl/
    daily.yaml
    daily.test.yaml
```

Rules:

- Without `dag`, a test file tests the DAG file beside it whose name is the
  test file's name without `.test.yaml` or `.test.yml`, followed by `.yaml`,
  or by `.yml` when that `.yaml` file does not exist. `release.test.yaml` and
  `release.test.yml` both test `release.yaml`, or `release.yml` when there is
  no `release.yaml`.
- `dag` names the tested DAG file. A relative path resolves against the
  directory that holds the test file.
- The test file is invalid when the tested DAG file does not exist, and when
  `dag` names a test file.
- Moving or renaming a DAG together with its test file keeps the test
  working.
- One test file per DAG is the convention. Its scenarios are its `cases`.

### Test Files Are Not DAGs

A test file is never a DAG:

- DAG discovery in the DAGs directory, the watcher on that directory, and
  DAG name lookup skip every file whose name ends in `.test.yaml` or
  `.test.yml`.
- The DAG list does not show a test file, the scheduler does not schedule
  one, and a change to one reloads nothing.
- `dagu start release.test` fails, and its message names `dagu test`.
- When `dagu scheduler`, `dagu server`, or `dagu start-all` starts, it reads
  once each file that DAG discovery skips for its name. For each one whose
  first document has top-level `steps` and no `cases`, it logs one warning
  that names the file, says that Dagu treats it as a test file and does not
  run it as a DAG, and suggests a rename, such as `smoke.test.yaml` to
  `smoke-test.yaml`. A file that is not valid YAML is not reported. The
  check runs only at startup: no request, watcher event, or periodic rescan
  runs it.
- `dagu test` reports such a file as an invalid test file with the same
  hint. See [Invalid Test Files](#invalid-test-files).

### The Test File

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `dag` | string | no | Path of the tested DAG file. Defaults to the DAG beside the test file |
| `params` | map | no | Run parameters for every case |
| `env` | map | no | DAG-level `env` entries for every case |
| `secrets` | map | no | Values for DAG `secrets` entries, by `name`, and for SSH credential refs. Needed only in a case with a real step. See [Secrets](#secrets) |
| `mocks` | map | no | Mocks for every case, by step key |
| `default_mock` | map | no | A mock for every step and handler that needs one and that `mocks` and `real` do not name. See [The Default Mock](#the-default-mock) |
| `real` | list | no | Step keys of the steps that run their real action in every case |
| `cases` | list | yes | The cases. It must not be empty |

Case fields:

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | Name of the case. It is not empty and is unique inside the file |
| `params` | map | no | Merged over the file's `params`, by name |
| `env` | map | no | Merged over the file's `env`, by name |
| `scheduled_at` | string | no | Schedule slot of the run. See [Trigger and Schedule Slot](#trigger-and-schedule-slot) |
| `trigger` | string | no | `context.trigger.type` of the run. Defaults to `manual` |
| `precondition` | `met` or `not_met` | no | Outcome of the DAG-level preconditions that run a command. See [Preconditions](#preconditions) |
| `mocks` | map | no | Replaces the file's mock of each step it names |
| `default_mock` | map | no | Replaces the file's `default_mock` |
| `real` | list | no | Added to the file's `real` |
| `expect` | map | yes | Assertions. See [Assertions](#assertions) |

Rules:

- A test file accepts only the fields this spec defines, at every level. Any
  other field makes the test file invalid.
- The keys of `params`, `env`, `secrets`, `mocks`, `outputs`,
  `expect.steps`, and `review.values` are names, not fields, so no such key
  makes the test file invalid. The section of each map says which names the
  case accepts: step keys in [Step Keys](#step-keys), `params` and `env` in
  [Loading the DAG](#loading-the-dag), `secrets` in [Secrets](#secrets),
  `outputs` in [Named Outputs](#named-outputs) and
  [Assertions](#assertions), and `review.values` in
  [Steps That Wait for a Person](#steps-that-wait-for-a-person). An `env`
  name that the DAG does not declare adds an entry.
- A case's mock of a step replaces the file's mock of that step as a whole,
  and a case's `default_mock` replaces the file's as a whole. Fields of the
  two mocks do not merge.
- A file key and a case key that name the same step, one by `name` and one by
  `id`, name one step.
- The case's `real` is the union of the file's `real` and the case's.
- `params` and `env` values are YAML scalars.

### Loading the DAG

The tested DAG loads as `dagu start` loads it: with the base config,
`defaults`, and dotenv files. A DAG that `dagu start` would fail to load
makes the test file invalid.

Rules:

- Each `params` entry is one `name=value` assignment, as
  `dagu start -- name=value` takes it. Its value is the scalar's text as
  written. A case whose params `dagu start` would reject is invalid.
- A param that `params` gives a value does not run its `params[].eval`
  (Spec 003).
- Each `env` entry sets a DAG-level `env` entry. Its value is the scalar's
  text, used literally without value resolution. It replaces the DAG's entry
  of the same name in place, or follows the DAG's last entry when the DAG
  declares no entry of that name.
- Each `secrets` entry gives the value of the DAG `secrets` entry of the same
  `name`, or of the SSH credential ref of the same text. See
  [Secrets](#secrets).

### Step Keys

A step key is one of:

- the `name` or `id` of a step of the tested DAG's entrypoint document
  (Spec 009);
- `handler_on.<event>`, which names the lifecycle handler of that event:
  `init`, `success`, `failure`, `abort`, `exit`, or `wait`. That includes a
  handler that the base config adds.

Rules:

- A key that starts with `handler_on.` always names a handler.
- A key that names no step or handler of the tested DAG makes the case
  invalid. Steps of other documents and `foreach` body steps have no key.
- Two keys of one map or list that name the same step make the case invalid.

### Mocks

A mock has result fields and step fields. Result fields script what the
action returns, once per execution. Step fields script what happens around
the action, once for the step.

Result fields:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `exit_code` | integer | `0` | Exit code of the execution. A non-zero code fails the attempt |
| `stdout` | string | empty | Written to the step's stdout |
| `stderr` | string | empty | Written to the step's stderr |
| `outputs` | map | none | Named outputs the execution publishes |
| `condition` | `met` or `not_met` | none | Outcome of the step's command-check `repeat_policy.condition` after this execution |
| `results` | list | none | One result per execution. Each item takes the result fields above, except `results` |

Step fields:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `precondition` | `met` or `not_met` | none | Outcome of the step's preconditions that run a command. See [Preconditions](#preconditions) |
| `review` | map | none | Decision for each wait of the step. See [Steps That Wait for a Person](#steps-that-wait-for-a-person) |

Step fields sit beside `results` or the inline result fields, never inside a
`results` item:

```yaml
mocks:
  deploy:
    precondition: met
    review:
      decision: approve
    results:
      - exit_code: 1
      - exit_code: 0
```

On a step with `retry_policy.limit: 1` and an `approval` gate, the
preconditions are decided once, the first attempt fails, the retry succeeds,
and the gate is approved when the step reaches it.

#### Results Per Execution

Rules:

- A mock with inline result fields is a one-item `results`.
- `results` must not be empty. A mock with `results` sets no inline result
  field.
- Each execution takes the next result. The first attempt takes the first
  result, and each retry and each repeat iteration takes the one after.
- After the last result, each later execution takes the last result again,
  except on a continuous step. See [Continuous Steps](#continuous-steps).
- A field that a result leaves out takes its default.

#### Replaced Actions

Rules:

- A mock replaces the step's action when it sets a result field.
- A mock also replaces the action of a step that is not a real step and
  whose action is not side-effect free, when the mock sets no result field.
  The step then takes one result with the defaults: exit code 0 and no
  output.
- A mock that sets only step fields keeps the real action of a step whose
  action is side-effect free, and of a real step.
- `{}` is a valid mock. On a step whose action is not side-effect free, it
  replaces the action with exit code 0 and no output. On a step whose action
  is side-effect free, it changes nothing, and the real action runs.
- A mock that sets a result field on a real step makes the case invalid.
- A `human.task` step has no action to replace. A result field in its mock
  makes the case invalid.

#### How a Result Reaches the Run

A replaced step runs through the same runner as a real one. Only its action
differs.

Rules:

- `exit_code` is the exit code of the execution. Retries, `continue_on`,
  and status propagation read it as they read a real exit code.
- `stdout` and `stderr` are written to the step's stdout and stderr exactly
  as given. Dagu adds no newline. String-form `output: VAR` captures `stdout`
  as in a real run.
- `outputs` supplies every named output the step publishes, whatever its
  source as Spec 007 lists them: the output file (Spec 012),
  `output_schema`, object-form `output`, `stdout.outputs`, or the action
  itself. Dagu does not parse `stdout` or read a file to publish them, and it
  does not apply the checks the real action makes on its outputs, such as
  `output_schema` validation.
- Only a result with exit code 0 publishes outputs. A result with a non-zero
  exit code publishes none, whatever its `outputs`.
- The step's `env`, `with`, and `run` fields resolve as in a real run of the
  step, by the command, script, and path rules of the step's own action. A
  reference to an unavailable value is preserved as text, as Spec 003
  defines, and does not fail the step.
- A replaced step starts no process, opens no connection, and creates no
  container. Checks that the real action makes before it runs, such as
  reading an SSH key or reaching a container runtime, do not run.
- A replaced step reads no file: not its `stdin`, and not a file that an
  object-form `output` entry with `from: file` names.

#### Named Outputs

A step has known outputs when its output names are known before the run: the
names its Spec 012 `outputs`, `output_schema`, object-form `output`,
`stdout.outputs`, and action declare. A step with none of them has no known
outputs. A step whose output names are known only during a run, such as a
sub-DAG step or an action step resolved from a manifest (Spec 007), has
runtime outputs.

Rules:

- `outputs` on a step without `id` makes the case invalid. A mock of such a
  step sets no `outputs`, so the rule below that a result sets every known
  output applies only to a step with `id`.
- On a step without runtime outputs, a result of a mock under `mocks` with
  exit code 0 sets every known output and no other name. When the step has
  no known outputs, the result sets no `outputs`.
- The default mock sets no `outputs`. Its result with exit code 0 publishes
  each known output of the step as an empty string, or as `null` for an
  output that Spec 012 declares with `type: json`. On a step with runtime
  outputs, it publishes none.
- On a step with runtime outputs, a result may set any name that matches
  `^[A-Za-z][A-Za-z0-9_]*$`.
- An output that Spec 012 declares with `type: string`, the default, takes a
  YAML string.
- An output that Spec 012 declares with `type: json` takes any YAML value,
  published as its JSON text. A YAML string is published as a JSON string.
- Any other output takes any YAML value. A YAML string is published as
  written, and any other value as its JSON text.

A real run would fail a step that publishes outputs other than the ones it
declares, or publish nothing for it. Rejecting the mock at load reports the
authoring mistake directly.

#### Invalid Mocks

A mock under `mocks` makes the case invalid when:

- its key names no step or handler of the tested DAG;
- it sets `results` together with an inline result field, or sets an empty
  `results`;
- it sets a result field on a real step or on a `human.task` step;
- it sets `outputs` that [Named Outputs](#named-outputs) rejects;
- it sets `precondition` on a step that has no precondition with a command
  check or a substituting `eval`;
- a result sets `condition` on a step whose `repeat_policy.condition` is not
  a command check;
- it sets `review` on a step that does not wait for a person, or sets a
  `review` that [Steps That Wait for a Person](#steps-that-wait-for-a-person)
  rejects.

#### The Default Mock

`default_mock` is a mock with the fields of a mock. It counts as the mock of
every step and handler that needs handling under
[the fail-closed rule](#the-fail-closed-rule) and that `mocks` and `real` do
not name. It never runs anything real, so a test keeps the guarantee of the
rule without a line per step:

```yaml
default_mock: {}
mocks:
  build:
    exit_code: 1
cases:
  - name: a failed build stops the deploy
    expect:
      steps:
        deploy: aborted
```

Rules:

- A step or handler needs handling when it has a source of the fail-closed
  table on it. The default mock applies to such a step or handler, including
  a handler that the base config adds.
- A step or handler with no source, such as a step with a side-effect-free
  action and no other source, takes no default mock and runs as it would
  without a mock.
- The default mock does not handle the sources that are not on a step: the
  DAG-level preconditions, `params[].eval`, secrets, and the child DAG of a
  real step.
- Its fields apply to a step only where they handle one of the step's
  sources:
  - On a step whose action is not side-effect free, it replaces the action
    as `{}` does, with its result fields when it sets any.
  - On a step with a side-effect-free action, it replaces the action only
    when it sets a result field and the step has a source that a replaced
    action handles: a continuous step, an object-form `output` entry with
    `from: file`, or a command-check repeat condition that its results
    script with `condition` (see [Repeat Conditions](#repeat-conditions)).
    Otherwise the real action runs, as under `{}`.
  - `precondition` applies to a step with a command-check or substituting
    `eval` precondition. `condition` in its results applies to a step with a
    command-check repeat condition. `review` applies to a waiting step whose
    wait needs values.
- A source that none of its fields handles stays unhandled, and the case is
  invalid.
- A field that handles no source of a step does not apply to that step and
  makes nothing invalid.
- Each step it applies to counts its own executions through its `results`.
- A `default_mock` that sets `outputs`, inline or in a result, makes the case
  invalid. See [Named Outputs](#named-outputs) for what its results publish.
- The report lists every step and handler it applied to, so an author sees
  which steps the test never scripted.

### Side-Effect-Free Actions

A side-effect-free action reads only its resolved `with` fields and the
run's values, and writes only its stdout, stderr, and outputs. The
side-effect-free actions are:

- `router.route`, including a router step written with the `value` and
  `routes` step fields
- `outputs.write`
- `log.write`
- `noop`

Every other action is not side-effect free. That includes command steps,
`decision.evaluate`, which calls a model provider, and `wait.duration`.

Two kinds of steps have no action of their own:

- A `foreach` step runs its body steps. It is side-effect free when every
  body step has a side-effect-free action and no other source of
  [the fail-closed rule](#the-fail-closed-rule).
- A `human.task` step starts no process (Spec 031). Its wait is approved
  automatically or settled by its `review`. See
  [Steps That Wait for a Person](#steps-that-wait-for-a-person).

A step with a side-effect-free action that its mock does not replace runs
its real action, so a test exercises the routing and outputs the DAG
defines.

### The Fail-Closed Rule

A case is invalid, and none of its steps runs, when the tested DAG has a
source in this table that the case does not handle as the table says:

| Source | Handled by |
| --- | --- |
| A step action that is not side-effect free | A mock that replaces the action, including `{}` and the default mock, or the step in `real` |
| A step precondition with a command check or a substituting `eval` (Spec 023) | `precondition` in the step's mock, or the step in `real` |
| A command-check `repeat_policy.condition` (Spec 075) | `condition` in every result of the step's mock, or the step in `real` |
| An object-form `output` entry with `from: file`, on a step whose action is not replaced | A mock that replaces the action, including the default mock, or the step in `real` |
| A continuous step with a side-effect-free action that is not replaced | A mock that sets a result field, or the step in `real` |
| A `foreach` body step with a source in this table, including an action that is not side-effect free | A mock of the `foreach` step |
| A waiting step whose wait needs values | `review` in the step's mock, also for a step in `real` |
| A DAG `secrets` entry (Spec 069) or an SSH credential given as `{ secret: <ref> }` (Spec 042), in a case with a real step | A value in `secrets` |
| A DAG-level precondition with a command check or a substituting `eval` | The case's `precondition` |
| A substituting `params[].eval` (Spec 011) | A value for that param in `params` |
| A source in this table in the child DAG of a real step | Nothing. The case is invalid |

Rules:

- The rule covers every step and handler, reached or not, so the outcome
  does not depend on the branch a case takes. Handlers include the ones the
  base config adds.
- Spec 003 allows command substitution only in `params[].eval` and in
  precondition `eval`, and command checks run only in preconditions and
  repeat conditions. The table covers every command Dagu runs outside a step
  action.
- An `eval` that is not substituting runs no command. It evaluates as in a
  real run and needs nothing from the test.
- The default mock counts as the mock of every step and handler it applies
  to. See [The Default Mock](#the-default-mock).
- No person answers in a test. A wait that needs no values is approved
  automatically, and a wait that needs values takes `review`, even when the
  step's action is real. See
  [Steps That Wait for a Person](#steps-that-wait-for-a-person).
- In an invalid case, no step, handler, `params[].eval`, or precondition
  runs, and no secret resolves.
- Only a line in `real` runs an action that is not side-effect free.

The error of an invalid case lists every unhandled source at once, one line
per source, as `<label>: <message>`:

| Source | Label |
| --- | --- |
| A step, or a source on it | `step <step>` |
| A handler, or a source on it | `handler_on.<event>` |
| A `foreach` body step | `step <step>, body step <body step>` |
| A source in the child DAG of a real step | `step <step>, child <DAG name>, ` followed by the source's label in the child DAG |
| A DAG-level precondition | `precondition` |
| A substituting `params[].eval` | `param <name>` |
| A DAG `secrets` entry | `secret <name>` |
| An SSH credential ref | `secret <ref>` |

Rules:

- `<step>` and `<body step>` are the step's `id`, or its `name` when it has
  no `id`.
- The message names what would run and how the test handles it. It
  contains `mock` or the test field that handles the source: `real`,
  `precondition`, `condition`, `review`, `params`, or `secrets`. For a step
  action, it names the action.
- Sources are listed in this order: params in declaration order, secrets in
  declaration order, SSH credential refs in lexicographic order, DAG-level
  preconditions, steps in the order the DAG declares them, then handlers in
  the order `init`, `success`, `failure`, `abort`, `exit`, `wait`. Several
  sources of one step are listed in the order of the table.

```text
INVALID workflows/release.test.yaml: a failed build stops the deploy
  param build_date: eval runs a command; give it a value under params
  step deploy: action ssh.run has side effects; mock it or list it under real
  handler_on.failure: action mail.send has side effects; mock it or list it under real
  fix, ready to paste:
    params:
      build_date: ""
    mocks:
      deploy: {}
      handler_on.failure: {}
```

A test cannot reach a real system by forgetting a mock. Running a real action
takes a line in `real`.

#### The Fix

Each invalid case with an unhandled source ends with the YAML that handles
every listed source, ready to paste.

Rules:

- The line `fix, ready to paste:` follows the error lines, and the fix
  follows it, indented by four spaces. Its fields are case fields, in the
  order `params`, `precondition`, `mocks`, and the entries of each field
  follow the order of the error lines.
- A `secrets` entry, which only the test file takes, follows instead under
  the line `fix for the test file, ready to paste:`, indented the same way.
- Pasted into the case, merged into the fields the case already has, the fix
  makes every listed source handled. A source in the child DAG of a real
  step has no entry, since no field handles it.
- A mock in the fix holds the step's current mock, if any, with the fields
  the step's sources need added.
- Each source adds this entry:

| Source | Entry |
| --- | --- |
| A step action that is not side-effect free, or a `foreach` body step | The mock `{}` on the step, or on its `foreach` step. On a step with known outputs, the mock sets `outputs` with each known output as `""`, or `null` for a `type: json` output |
| An object-form `output` with `from: file`, or a continuous step, on a step with a side-effect-free action | The mock `exit_code: 0`, with `outputs` as above |
| A step precondition | `precondition: met` in the step's mock |
| A command-check repeat condition | A `condition` that stops the step, `not_met` for `while` and `met` for `until`, in each result that has none. A `condition` that a result already sets stays. An inline mock becomes a one-item `results`, with its inline result fields moved into that result. A mock with no result field gains one result |
| A wait that needs values | `review` with `decision: approve` and a value for each required name. See below |
| A secret or an SSH credential ref | Its name under `secrets`, with the value `""` |
| A DAG-level precondition | The case's `precondition: met` |
| A substituting `params[].eval` | The param under `params`, with its `default`, else its first `enum` value, else `""` |

- A value for a required `human.task` property is its first `enum` or
  `oneOf` value, else `""`, `0`, or `false` by its type. A value that
  another constraint of the form rejects, such as `pattern`, is written with
  the comment `# replace with a value the form accepts`, and the case stays
  invalid until it is replaced.
- A value for a required `approval` input is the text `TODO`, since an
  empty value counts as left out.
- When the step's mock has `results`, the `outputs` of the fix go into each
  result.

### Preconditions

Rules:

- A value-match precondition without a substituting `eval` (Spec 023)
  evaluates as in a real run, against mocked outputs and the case's params
  and env. A test exercises output-driven branches this way.
- A mock's `precondition` decides every command-check condition and every
  substituting `eval` condition of the step, once per step start, as
  Spec 023 checks step preconditions. The value is the outcome of each such
  condition after `negate` applies: `met` passes it, and `not_met` fails it,
  which skips the step as Spec 023 defines.
- The step's other conditions still evaluate, so a step with
  `precondition: met` is still skipped when one of its value-match conditions
  fails.
- The case's `precondition` decides the DAG-level command-check and
  substituting `eval` conditions in the same way. `not_met` aborts the run,
  as Spec 023 defines, and no step starts.
- No command of a decided condition runs, and no `eval` of one is evaluated.
- A mock's `precondition` also decides the conditions of a real step.
  Without it, a real step's conditions run as in a real run.

### Repeat Conditions

Rules:

- A value-match `repeat_policy.condition` evaluates as in a real run.
- A command-check `repeat_policy.condition` of a replaced step takes the
  `condition` of the result that the execution used, with the meaning
  Spec 075 gives `met` and `not_met` for `while` and `until`. Its command
  does not run.
- The final check that `on_limit: fail` makes at `limit` takes the
  `condition` of the last execution's result.
- The command-check repeat condition of a real step runs as in a real run.
- A mock whose results set `condition` replaces the step's action, also on
  a step with a side-effect-free action and under the default mock, since
  each execution takes its `condition` from the result it used.

### Continuous Steps

With waits skipped, a continuous step whose last result repeats would run
without pause until `--timeout`. Under test:

- A replaced continuous step runs one execution per result and then stops
  repeating, unless its own repeat decision (Spec 075) stops it first. It
  ends with the status of its last execution, and `attempts` counts its
  executions.
- A continuous step with a side-effect-free action that its mock does not
  replace makes the case invalid, because nothing bounds it.
- A continuous step in `real` keeps its retry and repeat intervals, as every
  real step does (see [Time](#time)). Only its own repeat decision,
  `timeout_sec`, or `--timeout` ends it, also when it became continuous at
  run time.
- A `limit` that comes from a reference, such as `${params.n}`, is decided
  when it resolves during the run. At load, the step is not continuous. A
  positive number bounds the step as written. A value that leaves the step
  without a limit, such as `0`, makes it continuous: a replaced step's
  results bound it as above, a real step follows the rule above, and a step
  that is not real, with a side-effect-free action that its mock does not
  replace, fails the case at run time with that reason. See
  [Timeout, Limits, and Interrupt](#timeout-limits-and-interrupt).

### Steps That Wait for a Person

The run reaches the waiting checkpoint as a real run does (Spec 031).
Independent branches keep running. Once no other step is ready or running,
the run's status becomes `waiting`, `handler_on.wait` runs, and the wait
notification is recorded. The test then settles each waiting step through
the same completion path that a person uses, and the run resumes under the
same run ID. Nothing waits for a person.

A wait that needs no values, of a `human.task` whose form requires no
property without a `default` or of an `approval` gate with no `required`
inputs, is approved automatically with no values when the step's mock sets
no `review`. The report lists each wait approved this way. A wait that needs
values, or one that the test rejects, takes `review`:

```yaml
mocks:
  approve_release:
    review:
      decision: approve
      values:
        reason: looks good
```

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `decision` | `approve` or `reject` | yes | How each wait of the step is settled |
| `values` | map | no | Completion input of a `human.task`, or inputs of an `approval` gate |

Rules:

- `review` settles every wait of the step with the same decision.
- The default mock's `review` applies only to a wait that needs values. A
  wait that needs none, under the default mock, is approved automatically.
- For a `human.task` step, `approve` completes the task with `values` as its
  completion input, typed as `--inputs-json` types it (Spec 031). The step
  ends `succeeded`, and Spec 031 generates its outputs from that input.
  `values` that the task's form rejects make the case invalid, and so does
  `reject`.
- For a step with `approval`, the step's action runs first: for real when the
  step is real, and as its mock replaces it otherwise. When the step reaches
  its gate, `approve` approves the gate with `values` as its inputs, and
  `reject` rejects it as a person does: the step and the run end `rejected`.
  Each value is a scalar, given as its text. A value that is not a scalar,
  and `values` that leave out a name that `approval.required` lists, make
  the case invalid. An empty or blank value counts as left out, as it does
  for a person.
- A real waiting step takes its `review` like any other waiting step.

### Sub-DAG, Parallel, Foreach, and Node Steps

Rules:

- A mock that replaces a `dag.run` or `dag.enqueue` step replaces its child
  run. A mock that replaces a `parallel` step, or a step with `nodes`
  (Spec 089), replaces every child. No child run starts, is queued, or is
  recorded.
- The mock's result is the step's result, and its `outputs` are the step's
  outputs. `results` counts executions of the step, not children.
- On a command step with `nodes`, `retry_policy` applies to each node's child
  run (Spec 089), so a mock that replaces the whole step takes one result per
  step start.
- A `parallel` step and a step with `nodes` have runtime outputs: a real run
  publishes their children's outputs as a list, not by name, so a mock's
  `outputs` publish as given, as the step's named outputs.
- A real `dag.run` step, or a real `parallel` step with `dag.run`, starts its
  children in the same case. A child gets no mocks and no `real`, so a source
  of the fail-closed rule in the child DAG makes the case invalid. So does a
  child DAG that cannot be resolved before the case starts, such as one
  named by a step output.
- A real `dag.enqueue` step, or a real `parallel` step with `dag.enqueue`,
  queues its children in the case directory, where nothing runs them.
- A mock on a `foreach` step that is not side-effect free, and a mock that
  sets a result field on any `foreach` step, replace the whole construct:
  item expansion, the body, `collect`, and the aggregate status. String-form
  `output: VAR` captures the mock's `stdout` in place of the aggregate. The
  step ends `succeeded` or `failed`.
- A side-effect-free `foreach` step that its mock does not replace runs for
  real, over items taken from the run's values, such as mocked upstream
  outputs.
- `real` takes no `foreach` step. A `foreach` step in `real` makes the case
  invalid.

### Build DAGs

A test of a `type: build` DAG (Spec 033) makes the test file invalid, and
the error names Spec 033. A build step publishes a path output that Dagu
verifies and commits under the DAG's working directory, which a mock cannot
produce inside the case directory.

### Time

Rules:

- Under test, Dagu does not wait for retry intervals and backoff, repeat
  intervals and backoff, or `delay_sec`. The order and count of executions
  do not change.
- A real step keeps its retry and repeat intervals and backoff. Its action
  reaches a real system, and its intervals protect that system.
- A wait action such as `wait.duration` is not side-effect free, so a test
  mocks it like any other action.
- `timeout_sec` applies as in a real run. A replaced action returns at once,
  so only a real step can reach a timeout.

### Run Retries

A DAG-level `retry_policy` retries a failed run. A real run's retries are
started by the scheduler. A test has no scheduler, so `dagu test` starts them
itself.

Rules:

- Each retry runs in the same case directory, without waiting for the
  policy's interval, up to the policy's limit.
- Each retry runs as the scheduler's retry of the run would, including its
  trigger type `retry` (Spec 017).
- Mock results keep counting across run attempts: a step's executions in
  every attempt take its results in order.
- Handlers and notifications follow the rule a real run follows while a
  retry remains.
- Run and step status, outputs, and streams are those of the last attempt.
  A step's `attempts` counts its executions in every run attempt, and the
  run-level `attempts` counts the run attempts.
- The notifications that the report lists and `notifications` asserts are
  those of every attempt.
- `--timeout` bounds the case, every attempt included.

### Isolation

#### Case Directory

Rules:

- Each case runs in a new temporary case directory. The case's data
  directory is inside it and holds the case's run history, logs, artifacts,
  state (Spec 068), materialization records, live-process records, and
  locks, also when the DAG or the configuration sets `log_dir` or
  `artifacts.dir`.
- A case reads nothing from and writes nothing to the configured data
  directory. `state` starts empty, and no queue, scheduler, or server sees
  the run.
- The case directory is removed when the case ends, unless `--keep` is set.
- An invalid case creates no case directory.

#### Live Runs

A case runs next to the live runs of the same DAG, and neither sees the
other:

- A test of a service DAG (Spec 080) is not refused while a run of that DAG
  is active, and the active run is not stopped.
- The test's run is not in the DAG's run history, and no restart follows it.

#### Files

Rules:

- Without a real step, the DAG's working directory is neither created nor
  changed. Only real steps write files outside the case directory, apart
  from the run's control socket.
- The run's control socket is created where a real run creates it, under a
  name unique to the run, and removed when the run ends. It is the one file
  outside the case directory that every case creates.
- The `stdout` and `stderr` redirect files of every step that is not real
  are written inside the case directory, not at their configured paths. A
  redirect into the run's artifact directory writes to the case's artifact
  directory.
- A replaced step reads no file. See
  [How a Result Reaches the Run](#how-a-result-reaches-the-run).
- The run reads its inputs as `dagu start` does: the DAG file, the base
  config, dotenv files, and declared file dependencies (Spec 035). The
  snapshot of the file dependencies is materialized inside the case
  directory.

#### Run-Level Side Effects

The run does more than run steps. Under test:

| Behavior | Under test |
| --- | --- |
| `mail_on` notifications and step `mail_on_error` | Not sent. The report lists each notification the run would have sent |
| OpenTelemetry trace export (`otel`) | Off |
| An explicit `working_dir` that does not exist | Not created, unless the case has a real step |
| DAG-level `container` | Not created, unless the case has a real step, which runs in it |
| DAG-level `ssh` client, which reads keys and known hosts | Not created, unless the case has a real step |
| Declared `tools` | Not installed, unless the case has a real step |
| Native resource limits (`resources.limits` without a DAG-level `container`) | Not applied, unless the case has a real step |

A real step is the explicit opt-in to side effects, so the working
directory, container, SSH client, tools, and resource limits it needs are
set up as in a real run. A case without a real step runs on a host that has
none of them. Tools install into the configured tools directory, the one
write outside the case directory that only a real step causes.

#### Secrets

Secrets never resolve through their providers under test. No secret
provider or secret registry is contacted, and no file that a provider would
read is read, whatever the provider, the `env` provider included.

Rules:

- A case without a real step needs no secret value, because a replaced step
  never uses one. Each DAG `secrets` entry resolves to the placeholder
  `<secret:NAME>`, where `NAME` is the entry's `name`, unless `secrets` gives
  it a value. An SSH credential given as `{ secret: <ref> }` (Spec 042) is
  not resolved, since no SSH client exists.
- A case with a real step needs a value in `secrets` for every DAG `secrets`
  entry and every SSH credential ref of the DAG, because Dagu cannot tell
  which of them the real step reads. A missing one is an unhandled source of
  [the fail-closed rule](#the-fail-closed-rule). An SSH credential ref is
  keyed by its ref, such as `ssh/deploy-key`.
- A `secrets` key that is neither the `name` of a DAG `secrets` entry nor
  the ref of an SSH credential of the DAG makes the case invalid.
- Values and placeholders are masked in logs, in stored run status, and in
  the report, as secret values are in a real run (Spec 069).
- No connection record (Spec 107) is read, and a run under test skips
  Spec 107's admission checks.

Committed test files therefore hold no secret values, and a case's secrets
do not depend on the host it runs on. Other values can: a DAG `env` entry
that reads a host variable, such as `TOKEN: ${HOST_TOKEN}`, reads it under
test too, unless the case sets that entry in `env`. See
[Host Values](#host-values).

#### Host Values

A case's run reads the host environment as a run of `dagu start` does. A
host value is the value of a variable that the run takes from it:

- a variable that resolving the DAG and its steps reads from the host, such
  as `HOST_TOKEN` in a DAG `env` entry `TOKEN: ${HOST_TOKEN}`, in
  `${env.HOST_TOKEN}`, or in a JSON path such as `${HOST_JSON.token}`.
- a variable that Dagu passes to steps because `env_passthrough` or
  `env_passthrough_prefixes` in the configuration names it.

When the value is JSON, each string and number in it is a host value too,
since a JSON path or the listed keys of an assertion can take a part of it,
also through a DAG `env` entry. A number counts in each text it takes, as
written and as resolved, so `7.36491e5` also counts as `736491`. A host
value or a part shorter than four characters, such as `us` or `482`, is a
short part: it counts only where a value, or a string or number inside one,
equals it, not
in text that merely contains it, such as `us-east-1`, since text that short
cannot be told apart from the text around it. A boolean holds one bit and
is no host value. The variables that Dagu passes to every step whatever the
configuration have no host value: its fixed allowlist, such as `PATH` and
`HOME`, and the names that start with `DAGU_`, `DAG_`, `LC_`, or
`KUBERNETES_`. An empty value is no host value either.

Rules:

- A value holds a host value when its text contains one, as written or
  inside a JSON string, as output masking matches a secret (Spec 069), or
  when it, or a string or number inside it, equals a short part. The match
  is by value and best effort: a value derived from a host value in
  another form, such as a part that a shell-style operator takes
  (`${HOST_TOKEN#Bearer }`) or a re-encoding, is not matched. A DAG
  `secrets` entry, and the case's `env` for any other value, are the way to
  keep a credential out of a test.
- The text and the JSON report mask host values in the values of a failed
  output or `called_with` assertion, as they mask secrets. Notification
  recipients are written as the run resolved them.
- `--update` writes no value that holds a host value, and no `called_with`
  variable whose value the step takes from the host. See
  [Authoring Flags](#authoring-flags).
- A case that sets an entry in `env` takes no host value for it. A DAG
  `env` entry built from another entry, such as `AUTH: Bearer ${TOKEN}`,
  keeps the value it took while the DAG loaded, so the case sets each entry
  that holds the host value.

### Trigger and Schedule Slot

Rules:

- `trigger` sets `context.trigger.type` (Spec 017). It takes `manual`,
  `scheduler`, `webhook`, `retry`, `catchup`, `restart`, `backfill`, or
  `file` (Spec 082). `unknown`, `subdag`, and any other value make the case
  invalid. Without `trigger`, the type is `manual`.
- `trigger` changes nothing else. `restart` tests the branch a service DAG
  takes on its restarted runs. `retry` retries no run, `backfill` starts no
  backfill, and no trigger carries a payload.
- A retry that `dagu test` starts has the trigger type `retry`, whatever
  `trigger` sets. See [Run Retries](#run-retries).
- `scheduled_at` takes the forms that `dagu dry --scheduled-at` takes
  (Spec 017), including a slot later than now. An unquoted YAML date or time
  is read as written.
- With `scheduled_at`, the run gets the schedule time and window of that
  slot, as Spec 017 defines for a manual run started with `--scheduled-at`.
  Its trigger type stays the one `trigger` sets.

### Assertions

```yaml
expect:
  status: failed
  attempts: 1
  steps:
    plan:
      outputs:
        risk: low
    implement:
      status: succeeded
      attempts: 3
    test:
      status: failed
      stderr:
        contains: FAIL ./uploader
    route: aborted
    handler_on.failure: succeeded
  notifications:
    - kind: mail_on.failure
```

| Field | Meaning |
| --- | --- |
| `status` | Run status: `succeeded`, `failed`, `aborted`, `partially_succeeded`, or `rejected` |
| `attempts` | Run attempts, counting the retries of a DAG-level `retry_policy`. See [Run Retries](#run-retries) |
| `steps.<key>` | A step status token, or a map with one or more of `status`, `attempts`, `outputs`, `stdout`, `stderr`, and `called_with` |
| `steps.<key>.status` | Node status: `succeeded`, `failed`, `aborted`, `skipped`, `partially_succeeded`, `rejected`, or `not_started` |
| `steps.<key>.attempts` | Executions of the step's action, counting retries and repeat iterations |
| `steps.<key>.outputs` | Expected named outputs, by name |
| `steps.<key>.stdout`, `steps.<key>.stderr` | A map with `contains`, `matches`, or both |
| `steps.<key>.called_with` | What the step's last execution was called with. See [Called With](#called-with) |
| `notifications` | The notifications the run would have sent |

Rules:

- A step or handler that never started is `not_started`.
- A step whose action never ran, such as a skipped step, has 0 attempts. A
  `human.task` step has no action, so it has 0 attempts.
- A step that has no assertion is not checked, and the notifications are
  not checked without `notifications`.
- `expect` holds at least one assertion: `status`, `attempts`, a
  `steps.<key>` entry, or `notifications`. An empty `steps.<key>` map makes
  the case invalid.
- An unknown step key, an unknown status token, or a negative `attempts`
  makes the case invalid.
- A case passes when the run reaches a terminal status and every assertion
  holds. Otherwise it fails.
- The report lists every failed assertion of a case, not only the first.

Outputs:

- An output that Spec 012 declares with `type: json` matches by the JSON
  rule below.
- Any other output matches a YAML string when its text equals the string
  exactly. It matches any other YAML value by the JSON rule, against its
  text read as JSON. Text that is not JSON does not match.
- The JSON rule: an object matches when every key that the expected object
  lists has a matching value, recursively, and keys it does not list are
  ignored. An array or a scalar matches only an equal value. Numbers
  compare by value.
- An output that the step did not publish does not match.
- On a step with known outputs (see [Named Outputs](#named-outputs)), an
  assertion on another name makes the case invalid. A step with runtime
  outputs takes any name.
- An output assertion on a replaced step checks only what the test
  supplied. It is allowed, and is most useful on side-effect-free steps,
  which run for real.

Streams:

- `contains` holds when the stream contains its text. `matches` holds when
  its RE2 regular expression matches somewhere in the stream. When both are
  set, both must hold.
- The stream is the step's stdout or stderr of its last execution, as the
  step's log holds it, with secret values and placeholders masked.
- A step with no execution fails a stream assertion.
- A `matches` value that is not a valid RE2 expression makes the case
  invalid.

Notifications:

- Each item has `kind`: `mail_on.failure`, `mail_on.success`,
  `mail_on.wait`, or `mail_on_error`. It may have `step`, a step key, and
  `to`, a list of recipients. `step` is valid only with `mail_on_error`.
- The list is compared without regard to order. It holds when the run
  would have sent as many notifications as it lists, and each item pairs
  with a different notification of its `kind` and, when the item sets them,
  of its `step` and its `to`, compared as a set of recipients.
- `notifications: []` asserts that the run would have sent none.

#### Called With

`called_with` checks the call of a step's last execution: the values Dagu
resolved for its action from params, upstream outputs, env, and secrets,
before the action ran or its mock replaced it. It holds for a replaced step
and a real step alike, so a test checks the data that flows into a step
whose action never runs.

```yaml
expect:
  steps:
    deploy:
      called_with:
        run: ./deploy.sh "v1.2.3"
        env:
          IMAGE_TAG: v1.2.3
    notify:
      called_with:
        with:
          channel: "#releases"
```

| Field | Meaning |
| --- | --- |
| `run` | The resolved command or script text of a step written with `run`. A string matches it exactly. A map takes `contains`, `matches`, or both, as a stream assertion does |
| `with` | The resolved `with` inputs of a step written with `action`, by name |
| `env` | The step's resolved environment, by name |

Rules:

- The last execution is the latest execution of the step's action in every
  run attempt, as for streams. A step with no execution, such as a skipped
  step or a handler that never ran, fails `called_with` with one failure,
  whatever it lists.
- `run` is the script of a multi-line `run`, the command of a one-line
  `run` without leading and trailing whitespace, or the commands of a
  list-form `run`, one per line, each resolved as in a real run. Dagu-owned
  references, such as `${steps.<id>.outputs.<name>}`, `${params.<name>}`,
  and `${env.<name>}`, take their values. `$NAME` and `${NAME}` stay as
  written for the shell, except where Spec 006 has Dagu resolve them, such
  as inside a single-quoted span.
- `with` holds the inputs as the step writes them under `with`, each
  resolved as the action's `with` resolves. For a custom action, they are
  the inputs the step passes to it, not the fields of its definition.
- Only the listed `with` inputs are checked. A string matches an input
  whose text equals it exactly, and any other value matches by the JSON
  rule of outputs, against the input's text read as JSON: listed keys only,
  recursively. An input that the step does not set does not match.
- `env` holds every variable the step's action receives: the host
  variables that Dagu passes to steps, which are its fixed allowlist and
  the names that `env_passthrough` and `env_passthrough_prefixes` add, the
  DAG's and the step's `env`, and secrets. A host variable that Dagu does
  not pass, such as an `AWS_SECRET_ACCESS_KEY` that `env_passthrough` does
  not name, is not set. Only the listed names are checked, each against its
  exact text. A variable that is not set does not match.
- A secret compares as the step saw it: its placeholder `<secret:NAME>` in
  a case without a real step, and its value under `secrets` in a case with
  one. The report masks it.
- `run` on a step written with `action`, or on a step with no command or
  script, makes the case invalid. So does `with` on a step that is not
  written with `action`, such as a step written with `run`, whose `with`
  selects only its shell.
- `called_with` on a `human.task` step, a `foreach` step, or a step with
  `nodes` makes the case invalid: the first has no action, the second calls
  only its body steps, and the third resolves its command in the child run
  of each node.
- `called_with` without a field, an empty `with` or `env`, a `run` map
  without `contains` and `matches`, and a `matches` value that is not a
  valid RE2 expression make the case invalid.

### Report

The report goes to stdout. Its `<file>` is the test file's path relative to
the current directory when the file is under it, and its absolute path
otherwise, with `/` as the separator on every platform.

#### Text Report

```text
PASS workflows/release.test.yaml: deploys the built image (0.08s)
FAIL workflows/release.test.yaml: a failed build stops the deploy (0.06s)
  step deploy: expected skipped, got aborted (upstream step build failed)
2 cases: 1 passed, 1 failed
```

Rules:

- One line starts each case, in run order:
  - `PASS <file>: <case name> (<duration>)` for a case that passed
  - `FAIL <file>: <case name> (<duration>)` for a case that failed
  - `INVALID <file>: <case name>` for an invalid case
- An invalid test file has one line, `INVALID <file>`, and none of its cases
  appears.
- `<duration>` is the case's wall-clock time in seconds, with two decimal
  places, followed by `s`.
- Lines indented by two spaces follow the line of their case or file. A case
  that ran has, in this order:
  - one line per failed assertion;
  - `default mock: <steps>`, when the default mock applied to a step or
    handler;
  - `approved automatically: <steps>`, when a wait was approved
    automatically;
  - one line per notification the run would have sent;
  - `case directory <path>`, with `--keep`.
- With `--update`, the last line of each case says what happened to its
  `expect` (see [Authoring Flags](#authoring-flags)): `expect updated`,
  `expect unchanged`, or `expect kept: <reason>`. Before it comes one line
  per assertion that stays as written because the run's value holds a host
  value, in the order of the failures.
- A kept host value reads
  `<label>: kept, it holds the value of host variable <names>; set <variables> under env`.
  `<label>` is the start of the assertion's failure line:
  `step <key> output <name>`, `step <key> called_with.run`,
  `step <key> called_with.with.<name>`, `step <key> called_with.env.<name>`,
  or `notifications`. `<names>` lists the host variables whose values the
  run's value holds, sorted and separated by `, `, and the line reads
  `the values of host variables` when there are more than one.
  `<variables>` lists the variables of the step's call that hold those
  values, sorted and separated by `, `. A variable that Dagu passes to
  every step, such as `PATH`, is listed only when its own value is one of
  those values, or when it is the kept variable itself. `; set <variables>
  under env` is left out when no variable of the call holds them, and
  always for `notifications`.
- `<steps>` lists step labels in the order the DAG declares the steps, then
  handlers, separated by `, `. A step label is the step's `id`, or its
  `name` when it has no `id`, and a handler's label is
  `handler_on.<event>`.
- An invalid case has one line per error, then its fix (see
  [The Fix](#the-fix)). An invalid file has one line per error.
- A failed run-status assertion reads
  `status: expected <expected>, got <actual>`, and a failed run-level
  attempts assertion reads `attempts: expected <expected>, got <actual>`.
- A failed step-status assertion reads
  `step <key>: expected <expected>, got <actual>`.
- A failed attempts assertion reads
  `step <key> attempts: expected <expected>, got <actual>`.
- A failed output assertion reads
  `step <key> output <name>: expected <expected>, got <actual>`. Both values
  are compact JSON with host values masked: the expected YAML value, and
  the published value as a JSON string for a text output or as its JSON
  text for a `type: json` output. `<actual>` is `nothing` when the step
  published no such output.
- A failed stream assertion reads `step <key> stdout: does not contain
  <text>` or `step <key> stdout: does not match <regexp>`, with `stderr` for
  the stderr stream. `<text>` and `<regexp>` are JSON strings.
- A failed `called_with` assertion reads
  `step <key> called_with.run: expected <expected>, got <actual>`, with
  `called_with.with.<name>` for a `with` input and `called_with.env.<name>`
  for a variable. A failed `contains` or `matches` reads
  `step <key> called_with.run: expected to contain <text>, got <actual>` or
  `step <key> called_with.run: expected to match <regexp>, got <actual>`.
  Both values are compact JSON with secrets and host values masked, and
  `<actual>` is `nothing` when the call has no such input or variable. A
  step with no execution reads
  `step <key> called_with: expected a call, got none`.
- A failed notifications assertion reads
  `notifications: expected <list>, got <list>`. Each list writes each
  notification as its `kind`, followed by ` step <step>` and
  ` to <recipients>` when they are set, separated by `; `, or `none` when it
  is empty.
- `<key>` is the step key as `expect.steps` writes it. A failure line may
  end with a reason in parentheses, such as the upstream step that ended the
  step. The reason is informational, and its text is not part of this
  contract.
- A notification line reads `notification <kind>: <recipients>`, or
  `notification mail_on_error, step <step>: <recipients>` for a step's
  `mail_on_error`. `<kind>` is `mail_on.failure`, `mail_on.success`, or
  `mail_on.wait`. Recipients are separated by `, `.
- An error line reads `<label>: <message>`, or `<message>` when the error
  has no label. A fail-closed source has the label
  [the fail-closed rule](#the-fail-closed-rule) gives it. Another case error
  has the path of the field it concerns in the merged case, such as
  `mocks.build.outputs` or `expect.steps.deploy`, with `[<index>]` for a list
  item, counted from 0. A test file error has the path of the field in the
  file, or no label.
- In an invalid case, field errors come first, sorted by label, then the
  unhandled sources in their order.
- The last line is the summary,
  `<n> cases: <passed> passed, <failed> failed`. `<n>` counts the cases that
  passed, failed, or were invalid. `, <invalid> invalid` follows when a case
  is invalid, and `, <files> invalid files` follows when a test file is
  invalid. `cases` and `files` are singular when their count is 1.

#### JSON Report

`--format json` writes exactly one JSON object to stdout, and nothing else,
once every case has finished:

```json
{"cases": [{"file": "workflows/release.test.yaml", "name": "a failed build stops the deploy", "result": "fail", "duration_ms": 61, "failures": [{"step": "deploy", "field": "status", "expected": "skipped", "actual": "aborted"}]}]}
```

Top-level fields:

| Field | Present | Meaning |
| --- | --- | --- |
| `cases` | always | One object per case that ran or was invalid, in run order |
| `invalid_files` | when a test file is invalid | One object per invalid test file, in run order |

Case object:

| Field | Present | Meaning |
| --- | --- | --- |
| `file` | always | `<file>` of the test file |
| `name` | always | Case name |
| `result` | always | `pass`, `fail`, or `invalid` |
| `duration_ms` | when the case ran | Wall-clock time of the case, in whole milliseconds |
| `failures` | when `result` is `fail` | Failure objects. The run status and run attempts come first, then steps in the order `expect.steps` lists them, then `notifications`. Within a step: status, attempts, outputs in the order listed, stdout, stderr, with `contains` before `matches`, then `called_with`: its run text, `contains`, and `matches`, then its `with` inputs and its variables in the order listed |
| `errors` | when `result` is `invalid` | Error objects, in the order of the text report |
| `fix` | when an invalid case has a fix | The fix for the case, as YAML text |
| `file_fix` | when an invalid case's fix has `secrets` | The fix for the test file, as YAML text |
| `default_mocked` | when the default mock applied | Step labels the default mock applied to, in the order of the text report |
| `auto_approved` | when a wait was approved automatically | Step labels of the waits approved automatically, in the order of the text report |
| `notifications` | when the run would have sent one | Notification objects, in the order the run would have sent them |
| `case_dir` | with `--keep`, when the case ran | Absolute path of the kept case directory |
| `update` | with `--update` | What happened to the case's `expect`: `updated`, `unchanged`, or `kept` (see [Authoring Flags](#authoring-flags)) |
| `update_kept` | with `--update`, when an assertion stays as written because the run's value holds a host value | Kept objects, in the order of the text report |

Failure object:

| Field | Present | Meaning |
| --- | --- | --- |
| `step` | for a step assertion, and for `limit` | Step key as `expect.steps` writes it. For `limit`, the step label |
| `field` | always | `status`, `attempts`, `outputs.<name>`, `stdout.contains`, `stdout.matches`, `stderr.contains`, `stderr.matches`, `called_with`, `called_with.run`, `called_with.run.contains`, `called_with.run.matches`, `called_with.with.<name>`, `called_with.env.<name>`, `notifications`, `timeout`, `limit`, or `error` |
| `expected` | except for `called_with`, `limit`, and `error` | For `status` and `attempts`, the expected token or number. For `outputs.<name>`, the expected value, with host values masked. For a stream, the text or expression. For a `called_with` part, the expected text, expression, or value, with secrets and host values masked. For `notifications`, the expected list of notification objects. For `timeout`, the `--timeout` limit in milliseconds |
| `actual` | for `status`, `attempts`, `notifications`, `limit`, `error`, a published output, and a `called_with` part that the call has | The actual token or number, the published value (a string for a text output, the JSON value for a `type: json` output) with host values masked, the resolved run text, `with` input, or variable, with secrets and host values masked, the actual list of notification objects, for `limit`, the resolved limit as a string, or, for `error`, the reason a run could not complete (see [Timeout, Limits, and Interrupt](#timeout-limits-and-interrupt)) |

Kept object: `step`, the step key as `expect.steps` writes it, present for
a step assertion; `field`, the assertion's field as a failure object names
it, `outputs.<name>`, `called_with.run`, `called_with.with.<name>`,
`called_with.env.<name>`, or `notifications`;
`variables`, the host variables whose values the run's value holds, sorted;
and `env`, present when any, the variables of the step's call that hold
them, sorted. See [Host Values](#host-values).

Error object: `source`, the label, present when the error has one, and
`message`.

Notification object: `kind`, which is `mail_on.failure`, `mail_on.success`,
`mail_on.wait`, or `mail_on_error`; `step`, present for `mail_on_error`; and
`to`, the list of recipients.

Invalid file object: `file`, and `errors`, a list of error objects.

#### JUnit Report

`--format junit` writes exactly one JUnit XML document to stdout, and
nothing else, once every case has finished, so CI systems such as GitHub
Actions and GitLab show each case as a test result:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="1" failures="1" errors="0" time="0.061">
  <testsuite name="workflows/release.test.yaml" tests="1" failures="1" errors="0" time="0.061">
    <testcase name="a failed build stops the deploy" classname="workflows/release.test.yaml" time="0.061">
      <failure message="step deploy: expected skipped, got aborted">  step deploy: expected skipped, got aborted&#xA;</failure>
    </testcase>
  </testsuite>
</testsuites>
```

Rules:

- One `testsuite` per test file that reports a case or is invalid, in
  run order, named `<file>`. Its `testcase` elements are its cases, in run
  order, each named after the case, with `<file>` as `classname`. A file
  whose cases `--run` all leaves out has no suite, as it has no case in
  the JSON report.
- `time` is the case's wall-clock time in seconds, with three decimal
  places, and `0.000` for an invalid case. A suite's `time` adds up its
  cases, and the `testsuites` element's adds up the suites.
- `tests`, `failures`, and `errors` count the cases of the suite, or of
  every suite on `testsuites`.
- A failed case has one `failure` element, and an invalid case one `error`
  element. Its `message` is the first failure or error line, and its text is
  every line that follows the case's line in the text report, indented as
  there, such as the fix of an invalid case. A case that passed holds those
  lines, when it has any, in `system-out`.
- An invalid test file is a suite with one `testcase` named `<file>`, whose
  `error` holds the file's error lines.

### JSON Schema

Dagu publishes a JSON Schema for test files beside the DAG schema, so an
editor validates and completes `*.test.yaml` and `*.test.yml` files.

`dagu schema test` prints the schema, which an editor takes as a schema
file. `dagu schema test <path>` prints the part that a dot-separated path
names, as `dagu schema dag` does for the DAG schema.

Rules:

- The schema describes every field of this spec, with its type and allowed
  values, and allows no other field.
- A test file that the schema rejects for a field this spec does not define,
  or for a field of the wrong type, is a test file that `dagu test` reports
  invalid. A value that makes only one case invalid, such as an unknown
  status token, leaves the file valid and its other cases running, whether
  or not the schema rejects it.
- `dagu test` stays the authority. A test file that the schema accepts can
  still be invalid, such as one with a mock for a step the DAG lacks.

### Authoring Flags

`--init` and `--update` ship after the first release of `dagu test`.

`--init` writes an `expect` from a run in this shape, and `--update` writes
a step it adds in the same shape:

- `status`: the run status.
- `attempts`: the run attempts, when there are more than one.
- `steps`: every step of the DAG in the order it declares them, then every
  handler in the order `init`, `success`, `failure`, `abort`, `exit`,
  `wait`, each keyed by its label. Each is its status token, or a map with
  `status` and `attempts` when its attempts are more than one.
- `notifications`: the notifications the run would have sent, each with
  `kind`, `step` for `mail_on_error` as the step's label, and `to`, when
  there is any.

`dagu test --init <dag file>`:

- Takes exactly one argument, a DAG file, and writes its test file beside
  it: `<name>.test.yaml` for `<name>.yaml`, and `<name>.test.yml` for
  `<name>.yml`. When `<name>.yaml` sits beside `<name>.yml`, the test file
  of `<name>.yml` sets `dag: <name>.yml`, since without `dag` it would test
  `<name>.yaml`.
- The test file holds `params` and `mocks` with an entry for every source of
  the fail-closed rule that the DAG has, as [The Fix](#the-fix) writes them,
  and a `params` entry for every required param without a default, with its
  first `enum` value or `""`. It holds one case named `initial`, with the
  case's `precondition: met` when the DAG has a DAG-level precondition that
  needs it, and an `expect` written from a run of that case. `--timeout`
  bounds that run.
- It prints the path of the file it wrote and exits with status 0. When the
  case it wrote is still invalid, such as with a value marked for
  replacement, the case's `expect` is `status: succeeded`, the case's
  report as `dagu test` writes it follows the path, and it exits with
  status 2 after writing the file. A run of the case that does not finish,
  such as one that times out or has a repeat limit that leaves a step
  without a limit, does the same with exit status 1, and its report ends
  with the line
  `the run did not finish, so the written expect is status: succeeded, not the run's result`.
- An interrupt before the file is written writes nothing.
- It writes nothing and exits with status 2 when a test file for the DAG
  already exists beside it, under either suffix, or when the argument is
  not one DAG file that loads.

`dagu test --update [<test file, DAG file, DAG name, or directory>...]`:

- Selects test files and cases as `dagu test` does without it, `--run`
  included. `--timeout` bounds each run, and `--keep` and `--format json`
  apply as they do without it.
- Runs each selected case and rewrites its `expect` from its run, so a
  change in behavior shows as a diff to review. It never removes an
  assertion the author wrote. Every other part of the file, comments
  included, stays as it was.
- The exact facts that the case asserts take the run's values: the run
  `status` and `attempts`, each step's and handler's `status` and
  `attempts`, and `notifications`, each notification written as the shape
  above writes it. An assertion that already holds stays as written.
- A step or handler with no assertion gains `status`, and `attempts` when
  its attempts are more than one, as the shape above writes them, after
  the case's last step entry and in the shape's order. No other assertion
  is added.
- A step entry keeps its form: a status token stays a token, and a map keeps
  its fields.
- The outputs that the case asserts take their published values, and no
  output assertion is added. A string asserted on an output without
  `type: json` takes the published text, and any other value takes the
  text read as JSON. For an output asserted with an object, each key it
  lists takes its published value, recursively, and no key is added. An
  asserted output that the step did not publish, a listed key that the
  published object lacks, an expected object whose published value at that
  place, at any depth, is not an object, the value of a `type: json`
  output whose text is not JSON, and a value in place of a published number
  that YAML cannot write exactly, such as an integer above
  18446744073709551615 or a decimal with more digits than a 64-bit float
  holds, keep their expected values, and the case reports them as
  failures. Any other value whose text is not JSON takes
  the text.
- `stdout` and `stderr` assertions, `contains` and `matches`, stay as
  written.
- A `called_with` value that does not hold takes the step's call: a `run`
  string takes the resolved text, each listed `with` input takes its
  resolved value as an asserted output takes its published value, the
  listed keys only of an object, and each listed variable takes its text.
  A secret is written as the step saw it. A `run` given as `contains` or
  `matches`, an input or variable that the call lacks, and the
  `called_with` of a step with no execution stay as written, and the case
  reports them as failures. No `called_with` is added.
- No value from the host is written. An output, a `called_with` `run`, a
  `with` input, or a variable whose run value holds a host value (see
  [Host Values](#host-values)) stays as written, and so do `notifications`
  when a recipient of the sent ones holds one. So does a `called_with`
  variable whose value the step takes from the host, which Dagu passes to
  steps with no DAG, step, or case `env` entry in its place, a variable of
  the fixed allowlist such as `HOME` included. For a value asserted with an
  object, each listed key decides on its own, so the other changed keys of
  that object are still written. The case reports each kept value as a
  failure, masked as [Host Values](#host-values) says, and a line names the
  host variables and the variables to set under the case's `env`, such as
  `step deploy called_with.env.TOKEN: kept, it holds the value of host variable HOST_TOKEN; set TOKEN under env`.
  Once the case sets those variables, its run takes their values from the
  case, and `--update` writes the values that hold no host value.
- A case that is invalid, or whose run does not finish, such as one that
  times out or has a repeat limit that leaves a step without a limit,
  keeps its `expect`.
- A case written in flow style or with a YAML anchor or merge key, and a
  case whose `expect` holds an anchor, alias, or merge key, keep their
  `expect`, since rewriting it could change another part of the file.
- Only the text of what changes is rewritten. A value keeps its comment,
  and a string keeps its quotes. A flow-style map or list that gains an
  entry, or one of whose values changes other than in place on its line,
  is rewritten in block style.
- Each rewrite is read back before it is kept. A case whose rewritten text
  would not read as its rewritten `expect`, with the case otherwise as valid
  as before and the rest of the file as before, keeps its `expect`, so the
  file never gets invalid YAML or a value other than the run's.
- The report and the exit status are those of a run of the rewritten cases.
  A rewritten case passes unless an assertion that stays as written fails,
  such as a stream assertion, an output that the step did not publish, or a
  value from the host, and then it reports that failure.
- The last line of each case in the text report, and `update` in the JSON
  report, say what happened to its `expect`, whether or not the case
  passes: `expect updated` (`updated`) when its text changed,
  `expect unchanged` (`unchanged`) when the run left its text as it was, and
  `expect kept: <reason>` (`kept`) when it stays as written whatever the run
  did. `<reason>` is `the case is invalid`, `the run did not finish`,
  `the case is written in flow style or uses a YAML anchor, alias, or merge key`,
  or `the expect could not be rewritten in place`.
- A test file is written once its selected cases have run, and only when an
  `expect` changed, through a temporary file in its directory renamed over
  it, keeping its mode. A test file that changed while its cases ran is not
  written. `dagu test` then writes an error that names it to stderr, goes on
  with the other test files, ends the report, and exits with status 1, or 2
  when that status applies. An interrupt leaves the test file whose cases
  were running as it was.

### Exit Status

| Status | Meaning |
| --- | --- |
| 0 | Every selected case passed, or no case was selected |
| 1 | At least one case failed |
| 2 | At least one test file or case is invalid, or the arguments are invalid |

The highest applicable status wins.

## Errors

### Invalid Arguments

`dagu test` exits with status 2, runs no case, and writes the error to
stderr when:

- an argument is not an existing path, and does not resolve to an
  existing DAG file as a DAG name;
- without `--init`, a file argument is neither a test file nor a DAG file;
- no test file sits beside the DAG file that a DAG file or DAG name argument
  selects. The error names the command that writes one:
  `release.yaml has no test file; write one with: dagu test --init release.yaml`.
  For a DAG name, the command names the resolved DAG file;
- `--init` has other than one argument, or is set together with `--update`,
  `--run`, `--keep`, `--format json`, or `--format junit`;
- `--run` is not a valid regular expression;
- `--format` is not `text`, `json`, or `junit`;
- `--timeout` is not a duration greater than zero;
- a flag is unknown.

### Invalid Test Files

A test file is invalid, and none of its cases runs, when:

- it is not valid YAML, or its top level is not a map;
- it has a field this spec does not define, at any level, or a field of the
  wrong type;
- it leaves out `cases`, or `cases` is empty;
- a case leaves out `name` or `expect`, or two cases have the same `name`;
- its first document has top-level `steps` and no `cases`, even when more
  documents follow. The error says that the file reads as a DAG, that Dagu
  treats it as a test file and does not run it as a DAG, and suggests a
  rename, as the startup warning in
  [Test Files Are Not DAGs](#test-files-are-not-dags) does;
- the tested DAG file does not exist, `dag` names a test file, or the tested
  DAG fails to load;
- the tested DAG is a `type: build` DAG. The error names Spec 033.

The other test files still run.

### Invalid Cases

A case is invalid, and none of its steps runs, when, after the file's fields
merge into it:

- a step key names no step or handler, or two keys of one map or list name
  the same step;
- a mock is invalid. See [Invalid Mocks](#invalid-mocks);
- the default mock sets `outputs`;
- a source of [the fail-closed rule](#the-fail-closed-rule) is unhandled,
  including a secret or an SSH credential ref without a value in a case with
  a real step;
- `real` lists a `foreach` step;
- `dagu start` would reject its params, or a `secrets` key names neither a
  DAG `secrets` entry nor an SSH credential ref of the DAG;
- `trigger` is not an accepted type, or `scheduled_at` is not an accepted
  form;
- a `precondition` or `condition` value is neither `met` nor `not_met`, or a
  `decision` is neither `approve` nor `reject`;
- the case sets `precondition` and the DAG has no DAG-level precondition
  with a command check or a substituting `eval`;
- `expect` holds no assertion, names an unknown step, uses an unknown status
  token, or sets a negative `attempts`;
- an output assertion names an output that a step with known outputs does
  not declare;
- a `matches` value is not a valid RE2 expression;
- a `called_with` assertion is one that [Called With](#called-with) rejects,
  such as `run` on a step written with `action` or `with` on a step written
  with `run`. The error names the field;
- a `notifications` item has an unknown `kind`, or sets `step` with a `kind`
  other than `mail_on_error`.

The other cases of the file still run.

### Run Failures

A failed step or run is an outcome, not an error. The case compares its
status with the assertions like any other status.

### Timeout, Limits, and Interrupt

- A case that runs longer than `--timeout` is stopped as `dagu stop` stops a
  run. It fails with one `timeout` failure, and its assertions are not
  evaluated. Its text line reads `timeout: did not finish within <timeout>`,
  with the limit as a duration such as `5m0s`.
- A repeat `limit` that resolves during the run to a value that leaves a
  side-effect-free step without a limit, when the step is not real and its
  mock does not replace its action, stops the run as `--timeout` does. A
  real step that becomes continuous this way runs on, as
  [Continuous Steps](#continuous-steps) defines. The case fails with one
  `limit` failure for that step, and its assertions are not evaluated. Its
  text line reads
  `step <step>: repeat limit <value> leaves the step continuous; mock it with results`,
  where `<step>` is the step label and `<value>` the resolved limit.
- A run that cannot reach a terminal status, such as one with a step that
  waits for a child run's person, which a test cannot settle, fails with
  one `error` failure, and its assertions are not evaluated. Its text line
  is the reason. The reason's text is not part of this contract.
- An interrupted `dagu test` stops the running case, removes its case
  directory unless `--keep` is set, starts no further case, and exits with a
  non-zero status.

### Cleanup

- A case directory is removed when its case passes, fails, or times out,
  unless `--keep` is set.
- A case directory that cannot be removed is reported on stderr and does not
  change the exit status.

## Examples

The files of each example sit in one `workflows/` directory, and each
command runs in its parent directory. No example starts a process for a
replaced step.

### A Passing Test

```yaml
# workflows/release.yaml
steps:
  - id: build
    run: ./build.sh
    outputs:
      - name: image_tag
  - id: deploy
    depends: build
    run: ./deploy.sh "${steps.build.outputs.image_tag}"
```

```yaml
# workflows/release.test.yaml
mocks:
  build:
    outputs:
      image_tag: v1.2.3
  deploy: {}
cases:
  - name: deploys the built image
    expect:
      status: succeeded
  - name: a failed build stops the deploy
    mocks:
      build:
        exit_code: 1
    expect:
      status: failed
      steps:
        build: failed
        deploy: aborted
```

`dagu test workflows/` exits with status 0. Stdout contains
`PASS workflows/release.test.yaml: deploys the built image`,
`PASS workflows/release.test.yaml: a failed build stops the deploy`, and
`2 cases: 2 passed, 0 failed`.

### A Failed Assertion

With `workflows/release.yaml` above:

```yaml
# workflows/release.test.yaml
mocks:
  build:
    exit_code: 1
  deploy: {}
cases:
  - name: a failed build skips the deploy
    expect:
      status: failed
      steps:
        deploy: skipped
```

`dagu test workflows/release.test.yaml` exits with status 1. Stdout contains
`FAIL workflows/release.test.yaml: a failed build skips the deploy`,
`step deploy: expected skipped, got aborted`, and
`1 case: 0 passed, 1 failed`.

`dagu test --format json workflows/release.test.yaml` exits with status 1.
Stdout is one JSON object. Its only case has `result` `fail` and the failure
`{"step": "deploy", "field": "status", "expected": "skipped", "actual": "aborted"}`.

### Unhandled Sources

```yaml
# workflows/ship.yaml
params:
  - name: env
    default: prod
  - name: service
    eval: ${params.env}-api
  - name: build_date
    eval: $(date +%F)
steps:
  - id: build
    run: ./build.sh
  - id: deploy
    depends: build
    action: ssh.run
    with:
      host: prod.example.invalid
      user: deploy
      command: ./deploy.sh
handler_on:
  failure:
    action: mail.send
    with:
      from: dagu@example.com
      to: oncall@example.com
      subject: ship failed
      message: ship failed
```

```yaml
# workflows/ship.test.yaml
mocks:
  build: {}
cases:
  - name: ships
    expect:
      status: succeeded
```

`dagu test workflows/ship.test.yaml` exits with status 2, and no command
runs. Stdout contains `INVALID workflows/ship.test.yaml: ships` and, in this
order, lines that start with `param build_date: `, `step deploy: `, and
`handler_on.failure: `. The `step deploy` line names `ssh.run`, and the
`handler_on.failure` line names `mail.send`. No line names `service`: its
`eval` has no command substitution, so it evaluates as in a real run. The
error lines end with this fix:

```text
  fix, ready to paste:
    params:
      build_date: ""
    mocks:
      deploy: {}
      handler_on.failure: {}
```

With the fix pasted into the case, `dagu test workflows/ship.test.yaml`
exits with status 0.

### The Default Mock

With `workflows/release.yaml` from [A Passing Test](#a-passing-test):

```yaml
# workflows/release.test.yaml
default_mock: {}
cases:
  - name: every step succeeds by default
    expect:
      status: succeeded
  - name: a failed build stops the deploy
    mocks:
      build:
        exit_code: 1
    expect:
      steps:
        build: failed
        deploy: aborted
```

`dagu test workflows/release.test.yaml` exits with status 0, and no process
starts. Under the first case, stdout contains `default mock: build, deploy`,
and `build` publishes `image_tag` as an empty string. Under the second, it
contains `default mock: deploy`.

### Routing on a Mocked Output

```yaml
# workflows/change.yaml
steps:
  - id: plan
    run: agent plan
    outputs:
      - name: risk
  - id: route
    depends: plan
    action: router.route
    with:
      value: ${steps.plan.outputs.risk}
      routes:
        high: [review]
        low: [merge]
  - id: review
    run: agent review
  - id: merge
    run: gh pr merge --auto
```

```yaml
# workflows/change.test.yaml
mocks:
  plan:
    outputs:
      risk: low
  review: {}
  merge: {}
cases:
  - name: low risk merges without review
    expect:
      status: succeeded
      steps:
        route: succeeded
        merge: succeeded
        review: skipped
  - name: high risk goes to review
    mocks:
      plan:
        outputs:
          risk: high
    expect:
      steps:
        review: succeeded
        merge: skipped
```

`dagu test workflows/change.test.yaml` exits with status 0. `route` runs its
real `router.route` action in both cases.

### Retries Without Waiting

```yaml
# workflows/implement.yaml
steps:
  - id: implement
    run: agent implement
    retry_policy:
      limit: 2
      interval_sec: 60
```

```yaml
# workflows/implement.test.yaml
cases:
  - name: a flaky step passes on the third attempt
    mocks:
      implement:
        results:
          - exit_code: 1
          - exit_code: 1
          - exit_code: 0
    expect:
      status: succeeded
      steps:
        implement:
          status: succeeded
          attempts: 3
```

`dagu test workflows/implement.test.yaml` exits with status 0 without
waiting for either 60-second retry interval.

### Deciding Preconditions

```yaml
# workflows/migrate.yaml
preconditions:
  - condition: test -f /etc/release.lock
    negate: true
steps:
  - id: migrate
    preconditions:
      - condition: test -f migrations/pending
    run: ./migrate.sh
  - id: notify
    depends: migrate
    run: ./notify.sh
```

```yaml
# workflows/migrate.test.yaml
mocks:
  migrate:
    precondition: not_met
  notify: {}
cases:
  - name: nothing pending skips the migration
    precondition: met
    expect:
      status: succeeded
      steps:
        migrate: skipped
        notify: skipped
  - name: a release lock aborts the run
    precondition: not_met
    expect:
      status: aborted
      steps:
        migrate: not_started
  - name: an unscripted DAG precondition is invalid
    expect:
      status: succeeded
```

`dagu test workflows/migrate.test.yaml` exits with status 2. The first two
cases pass, and no `test` command runs. `precondition: met` passes the
negated DAG-level condition. The third case is invalid, and its error line
starts with `precondition: `.

### Scripting a Repeat Condition

```yaml
# workflows/poll.yaml
steps:
  - id: poll
    run: ./poll.sh
    repeat_policy:
      repeat: while
      condition: test -f pending.flag
      interval_sec: 30
      limit: 10
```

```yaml
# workflows/poll.test.yaml
cases:
  - name: polls until nothing is pending
    mocks:
      poll:
        results:
          - condition: met
          - condition: met
          - condition: not_met
    expect:
      steps:
        poll:
          status: succeeded
          attempts: 3
  - name: a result without condition is invalid
    mocks:
      poll:
        results:
          - condition: met
          - exit_code: 0
    expect:
      status: succeeded
```

`dagu test workflows/poll.test.yaml` exits with status 2. The first case
passes without waiting for a repeat interval. The second case is invalid,
and its error line starts with `step poll: `.

### Bounding a Continuous Step

```yaml
# workflows/heartbeat.yaml
restart_policy: always
restart_wait_sec: 5
steps:
  - id: beat
    run: ./check.sh
    repeat_policy:
      repeat: while
      interval_sec: 1
  - id: tick
    action: log.write
    with:
      message: tick
    repeat_policy:
      repeat: while
```

```yaml
# workflows/heartbeat.test.yaml
mocks:
  tick:
    exit_code: 0
cases:
  - name: three healthy beats
    mocks:
      beat:
        results:
          - exit_code: 0
          - exit_code: 0
          - exit_code: 0
    expect:
      status: succeeded
      steps:
        beat:
          status: succeeded
          attempts: 3
        tick:
          attempts: 1
  - name: an unbounded side-effect-free step is invalid
    mocks:
      beat: {}
      tick: {}
    expect:
      status: succeeded
```

`dagu test workflows/heartbeat.test.yaml` exits with status 2. The first
case passes well before `--timeout`, also while a live run of the DAG runs
on the same host, and the live run is not stopped. The second case is
invalid, and its error line starts with `step tick: `.

### Human Tasks

```yaml
# workflows/window.yaml
steps:
  - id: choose_window
    action: human.task
    with:
      prompt: Choose a deployment window
      form:
        type: object
        properties:
          window:
            type: string
            enum: [morning, evening]
        required: [window]
  - id: deploy_morning
    depends: choose_window
    preconditions:
      - condition: ${steps.choose_window.outputs.window}
        expected: morning
    run: ./deploy.sh
```

```yaml
# workflows/window.test.yaml
mocks:
  choose_window:
    review:
      decision: approve
      values:
        window: morning
  deploy_morning: {}
cases:
  - name: the morning window deploys
    expect:
      status: succeeded
      steps:
        choose_window: succeeded
        deploy_morning: succeeded
  - name: the evening window skips the deploy
    mocks:
      choose_window:
        review:
          decision: approve
          values:
            window: evening
    expect:
      steps:
        deploy_morning: skipped
  - name: an unscripted human task is invalid
    mocks:
      choose_window: {}
    expect:
      status: succeeded
```

`dagu test workflows/window.test.yaml` exits with status 2. The first two
cases pass without waiting for a person. The third case is invalid, and its
error line starts with `step choose_window: ` and names `review`.

### Approval Gates

```yaml
# workflows/gate.yaml
steps:
  - id: release
    run: ./release.sh
    approval:
      prompt: Ship it?
  - id: announce
    depends: release
    run: ./announce.sh
    approval:
      prompt: Announce it?
      input: [channel]
      required: [channel]
handler_on:
  wait:
    action: log.write
    with:
      message: waiting
```

```yaml
# workflows/gate.test.yaml
mocks:
  release: {}
  announce:
    review:
      decision: approve
      values:
        channel: releases
cases:
  - name: an approved release is announced
    expect:
      status: succeeded
      steps:
        release: succeeded
        announce: succeeded
        handler_on.wait: succeeded
  - name: a rejected release ends the run rejected
    mocks:
      release:
        review:
          decision: reject
    expect:
      status: rejected
      steps:
        release: rejected
        announce: not_started
  - name: a required input without review is invalid
    mocks:
      announce: {}
    expect:
      status: succeeded
```

`dagu test workflows/gate.test.yaml` exits with status 2. In the first case,
the run reaches `waiting` and runs `handler_on.wait` before each gate is
settled. The gate of `release` needs no values, so it is approved
automatically, and stdout contains `approved automatically: release`. The
second case passes. The third case is invalid, and its error line starts
with `step announce: ` and names `review`.

### Foreach

```yaml
# workflows/episodes.yaml
steps:
  - id: list
    run: ./list-episodes.sh
    outputs:
      - name: episodes
        type: json
  - id: summarize
    depends: list
    foreach:
      items: ${steps.list.outputs.episodes}
      steps:
        - id: one
          run: ./summarize.sh ${foreach.item}
    output: SUMMARIES
```

```yaml
# workflows/episodes.test.yaml
mocks:
  list:
    outputs:
      episodes: [a, b]
cases:
  - name: a mocked foreach succeeds without expanding items
    mocks:
      summarize:
        stdout: '{"summary": {"total": 2}}'
    expect:
      steps:
        summarize: succeeded
  - name: a foreach with a command body is invalid
    expect:
      status: succeeded
```

`dagu test workflows/episodes.test.yaml` exits with status 2. In the first
case, no item body runs. The second case is invalid, and its error line
starts with `step summarize, body step one: `.

### Side Effects That Stay Off

```yaml
# workflows/uploader.yaml
mail_on:
  failure: true
error_mail:
  from: dagu@example.com
  to: oncall@example.com
otel:
  enabled: true
  endpoint: localhost:4317
container:
  image: alpine:3.20
steps:
  - id: test
    run: make test
    stdout: result.txt
```

```yaml
# workflows/uploader.test.yaml
cases:
  - name: failing tests send mail
    mocks:
      test:
        exit_code: 2
        stdout: FAIL
    expect:
      status: failed
```

`dagu test workflows/uploader.test.yaml` exits with status 0. Stdout
contains `PASS workflows/uploader.test.yaml: failing tests send mail` and
`notification mail_on.failure: oncall@example.com`. No mail is sent, no span
is exported, no container is created, and `workflows/result.txt` does not
exist. With `--keep`, the reported case directory holds a redirect file that
contains `FAIL`.

### Setting the Trigger

```yaml
# workflows/service.yaml
restart_policy: always
restart_wait_sec: 5
steps:
  - id: recover
    preconditions:
      - condition: ${context.trigger.type}
        expected: restart
    run: ./recover.sh
```

```yaml
# workflows/service.test.yaml
mocks:
  recover: {}
cases:
  - name: a restarted run recovers
    trigger: restart
    expect:
      steps:
        recover: succeeded
  - name: a manual run skips recovery
    expect:
      steps:
        recover: skipped
```

`dagu test workflows/service.test.yaml` exits with status 0.

### Secrets Without Values

```yaml
# workflows/publish.yaml
secrets:
  - name: API_TOKEN
    provider: env
    key: PUBLISH_TOKEN
steps:
  - id: show
    action: log.write
    with:
      message: token ${API_TOKEN}
  - id: publish
    depends: show
    run: ./publish.sh
```

```yaml
# workflows/publish.test.yaml
mocks:
  publish: {}
cases:
  - name: a case without a real step needs no secret
    expect:
      status: succeeded
      steps:
        show:
          stdout:
            contains: token *******
  - name: a real step needs every secret
    real: [publish]
    expect:
      status: succeeded
```

`dagu test workflows/publish.test.yaml` exits with status 2, with
`PUBLISH_TOKEN` unset. The first case passes: `API_TOKEN` resolves to its
placeholder, which the log masks. The second case is invalid, and no command
runs. Its error line starts with `secret API_TOKEN: `, and its fix is under
`fix for the test file, ready to paste:`.

### Run Retries

```yaml
# workflows/nightly.yaml
retry_policy:
  limit: 2
  interval_sec: 300
steps:
  - id: sync
    run: ./sync.sh
```

```yaml
# workflows/nightly.test.yaml
cases:
  - name: the second run attempt succeeds
    mocks:
      sync:
        results:
          - exit_code: 1
          - exit_code: 0
    expect:
      status: succeeded
      attempts: 2
      steps:
        sync:
          status: succeeded
          attempts: 2
```

`dagu test workflows/nightly.test.yaml` exits with status 0 without waiting
for the 300-second retry interval.

### Outputs, Streams, and Notifications

```yaml
# workflows/report.yaml
mail_on:
  failure: true
error_mail:
  from: dagu@example.com
  to: oncall@example.com
steps:
  - id: count
    run: ./count.sh
    outputs:
      - name: files
      - name: stats
        type: json
  - id: summary
    depends: count
    action: log.write
    with:
      message: ${steps.count.outputs.files} files changed
  - id: check
    depends: summary
    run: ./check.sh
```

```yaml
# workflows/report.test.yaml
mocks:
  count:
    outputs:
      files: "3"
      stats:
        files: 3
        lines: 40
  check: {}
cases:
  - name: the summary reports the count
    expect:
      status: succeeded
      steps:
        count:
          outputs:
            files: "3"
            stats:
              files: 3
        summary:
          stdout:
            contains: 3 files changed
            matches: '^\d+ files changed'
      notifications: []
  - name: a failed check sends one mail
    mocks:
      check:
        exit_code: 1
    expect:
      status: failed
      notifications:
        - kind: mail_on.failure
          to: [oncall@example.com]
```

`dagu test workflows/report.test.yaml` exits with status 0. The `stats`
assertion lists one of two keys and matches. With `notifications: []` in the
second case instead, the case fails, and stdout contains
`notifications: expected none, got mail_on.failure to oncall@example.com`.

### What a Step Was Called With

```yaml
# workflows/called.yaml
secrets:
  - name: API_TOKEN
    provider: env
    key: NOTIFY_TOKEN
steps:
  - id: build
    run: ./build.sh
    outputs:
      - name: image_tag
  - id: deploy
    depends: build
    run: ./deploy.sh "${steps.build.outputs.image_tag}"
    env:
      - IMAGE_TAG: ${steps.build.outputs.image_tag}
  - id: notify
    depends: deploy
    action: http.request
    with:
      method: POST
      url: https://hooks.example.com/releases
      headers:
        Authorization: Bearer ${API_TOKEN}
      body: deployed ${steps.build.outputs.image_tag}
```

```yaml
# workflows/called.test.yaml
mocks:
  build:
    outputs:
      image_tag: v1.2.3
  deploy: {}
  notify: {}
cases:
  - name: deploys the built image
    expect:
      steps:
        deploy:
          called_with:
            run: ./deploy.sh "v1.2.3"
            env:
              IMAGE_TAG: v1.2.3
        notify:
          called_with:
            with:
              headers:
                Authorization: Bearer <secret:API_TOKEN>
              body: deployed v1.2.3
  - name: a wrong tag reaches the deploy
    mocks:
      build:
        outputs:
          image_tag: v1.2.4
    expect:
      steps:
        deploy:
          called_with:
            run:
              contains: v1.2.3
  - name: an action step takes no run
    expect:
      steps:
        notify:
          called_with:
            run: ./notify.sh
```

`dagu test workflows/called.test.yaml` exits with status 2 with
`NOTIFY_TOKEN` unset, and no process starts. The first case passes: the
`with` assertion lists two of four inputs, and `API_TOKEN` compares as its
placeholder. The second case fails, and stdout contains
`step deploy called_with.run: expected to contain "v1.2.3", got "./deploy.sh \"v1.2.4\""`.
The third case is invalid, and its error line reads
`expect.steps.notify.called_with.run: the step runs action http.request, which takes no run; assert with`.

### A Value From the Host

```yaml
# workflows/sync.yaml
env:
  - TOKEN: ${SYNC_TOKEN}
steps:
  - id: sync
    run: ./sync.sh
```

```yaml
# workflows/sync.test.yaml
mocks:
  sync: {}
cases:
  - name: the host token reaches the sync
    expect:
      steps:
        sync:
          called_with:
            env:
              TOKEN: test-token
  - name: the case sets the token
    env:
      TOKEN: test-token
    expect:
      steps:
        sync:
          called_with:
            env:
              TOKEN: old-token
```

With `SYNC_TOKEN` set on the host, `dagu test --update workflows/sync.test.yaml`
exits with status 1. The first case keeps its `TOKEN`, and its lines read
`step sync called_with.env.TOKEN: expected "test-token", got "*******"`,
`step sync called_with.env.TOKEN: kept, it holds the value of host variable SYNC_TOKEN; set TOKEN under env`,
and `expect unchanged`. The second case sets `TOKEN` under `env`, so its run
takes no value from the host: its `TOKEN` is rewritten to `test-token`, and
it passes.

### A Repeat Limit From a Param

```yaml
# workflows/poll_n.yaml
params:
  - name: n
    default: "3"
steps:
  - id: poll
    run: ./poll.sh
    repeat_policy:
      repeat: while
      limit: ${params.n}
  - id: tick
    action: log.write
    with:
      message: tick
    repeat_policy:
      repeat: while
      limit: ${params.n}
```

```yaml
# workflows/poll_n.test.yaml
mocks:
  poll: {}
cases:
  - name: the limit bounds both steps
    expect:
      steps:
        poll:
          attempts: 3
        tick:
          attempts: 3
  - name: without a limit the results bound the mocked steps
    params:
      n: "0"
    mocks:
      poll:
        results:
          - exit_code: 0
          - exit_code: 0
      tick:
        exit_code: 0
    expect:
      steps:
        poll:
          attempts: 2
        tick:
          attempts: 1
  - name: without a limit an unmocked step fails the case
    params:
      n: "0"
    expect:
      status: succeeded
```

`dagu test workflows/poll_n.test.yaml` exits with status 1. The first two
cases pass. The third case fails, and stdout contains
`step tick: repeat limit 0 leaves the step continuous; mock it with results`.

### Writing a Test File

With `workflows/release.yaml` from [A Passing Test](#a-passing-test) and no
test file beside it, `dagu test --init workflows/release.yaml` exits with
status 0, prints `workflows/release.test.yaml`, and writes:

```yaml
mocks:
  build:
    outputs:
      image_tag: ""
  deploy: {}
cases:
  - name: initial
    expect:
      status: succeeded
      steps:
        build: succeeded
        deploy: succeeded
```

`dagu test workflows/release.test.yaml` then exits with status 0. A second
`dagu test --init workflows/release.yaml` exits with status 2 and writes
nothing.

After the file's `build` mock gets `exit_code: 1`,
`dagu test --update workflows/release.test.yaml` exits with status 0, its
report follows the case's `PASS` line with `expect updated`, and it rewrites
only the case's `expect`, to `status: failed` with `build: failed` and
`deploy: aborted`.

### A Test File That Reads as a DAG

```yaml
# workflows/smoke.test.yaml
steps:
  - run: ./smoke.sh
```

`dagu test workflows/smoke.test.yaml` exits with status 2. Stdout contains
`INVALID workflows/smoke.test.yaml` and an error line that contains
`rename`.

### A Build DAG

```yaml
# workflows/compile.yaml
type: build
steps:
  - id: compile
    inputs:
      - name: source
        path: source.txt
    outputs:
      - name: artifact
        path: artifact.bin
    run: compiler "${inputs.source}" -o "${outputs.artifact}"
```

```yaml
# workflows/compile.test.yaml
mocks:
  compile: {}
cases:
  - name: compiles
    expect:
      status: succeeded
```

`dagu test workflows/compile.test.yaml` exits with status 2. Stdout contains
`INVALID workflows/compile.test.yaml` and an error line that contains
`Spec 033`.
