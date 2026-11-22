# Spec 101: Workflow Tests

## Status

Not implemented. This spec describes target conformance behavior for
`dagu test` and must not be treated as product behavior until implementation
catches up.

This version defines no assertion on step outputs, stdout, stderr, or
notifications. The report still lists the notifications a run would have
sent. Every DAG `secrets` entry needs a value in the test, whatever its
provider.

## Scope

This spec covers:

- the `dagu test` command: its arguments, flags, report, and exit status
- where test files live, and which DAG a test file tests
- the test file and case schema
- mocks: result fields, step fields, and results per execution
- the actions that run for real under test
- the fail-closed rule, which makes a case invalid before any step runs
- preconditions, repeat conditions, continuous steps, and steps that wait
  for a person, under test
- mocks of sub-DAG, `parallel`, `foreach`, and node-targeted steps
- skipped waits
- the isolation of each case: its case directory, files, live runs,
  run-level side effects, and secrets
- the trigger type and schedule slot of a case
- assertions on run status, step status, and attempts
- test files being skipped by DAG discovery

This spec does not define:

- scheduler, queue, coordinator, or worker behavior. A test runs the DAG
  locally, in the `dagu test` process, also when the DAG sets `queue` or
  `worker_selector`.
- the restart loop of a service DAG (Spec 080), or a retry of the whole run
  by a DAG-level `retry_policy`. A case is one run of the DAG.
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

- Projects and workflow discovery: [Spec 001](001-project.md)
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

A mock is the value of a step key under `mocks`.

An execution is one run of a step's action: the first attempt, a retry, or a
repeat iteration.

A result scripts what one execution returns.

A real step is a step whose key the case lists in `real`.

A replaced step is a step whose action its mock replaces. See
[Replaced Actions](#replaced-actions).

A side-effect-free action is an action that
[Side-Effect-Free Actions](#side-effect-free-actions) lists.

A waiting step is a `human.task` step (Spec 031) or a step with `approval`.

A continuous step is a step whose `repeat_policy` has a `repeat` mode and no
`limit` (Spec 083).

The case directory is the temporary directory a case runs in.

An invalid test file runs none of its cases. An invalid case runs none of its
steps.

## Behavior

### Running Tests

```sh
dagu test [flags] [<test file or directory>...]
```

Rules:

- A file argument must be a test file.
- A directory argument selects every test file under it, at any depth.
  Directories whose names start with `.` are skipped, and symbolic links are
  not followed.
- With no argument, `dagu test` selects every test file under the DAGs
  directory of the loaded configuration, or under the workflow directory in
  a project (Spec 001), by the same directory rule.
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
| `--format <format>` | `text` | `text` or `json` |
| `--keep` | off | Keep each case directory and report its path |

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

- DAG discovery in the DAGs directory and in a project's workflow directory
  (Spec 001), the watcher on those directories, and DAG name lookup skip
  every file whose name ends in `.test.yaml` or `.test.yml`.
- The DAG list does not show a test file, the scheduler does not schedule
  one, and a change to one reloads nothing.
- `dagu start release.test` fails, and its message names `dagu test`.

### The Test File

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `dag` | string | no | Path of the tested DAG file. Defaults to the DAG beside the test file |
| `params` | map | no | Run parameters for every case |
| `env` | map | no | DAG-level `env` entries for every case |
| `secrets` | map | no | A value for each DAG `secrets` entry, by its `name` |
| `mocks` | map | no | Mocks for every case, by step key |
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
| `real` | list | no | Added to the file's `real` |
| `expect` | map | yes | Assertions. See [Assertions](#assertions) |

Rules:

- A test file accepts only the fields this spec defines, at every level. Any
  other field makes the test file invalid.
- A case's mock of a step replaces the file's mock of that step as a whole.
  Fields of the two mocks do not merge.
- A file key and a case key that name the same step, one by `name` and one by
  `id`, name one step.
- The case's `real` is the union of the file's `real` and the case's.
- `params` and `env` values are YAML scalars.

### Loading the DAG

The tested DAG loads as `dagu start` loads it: with the base config,
`defaults`, and dotenv files, and in a project with the project's base config
(Spec 001). A DAG that `dagu start` would fail to load makes the test file
invalid.

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
  `name`. See [Secrets](#secrets).

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

- `outputs` on a step without `id` makes the case invalid.
- On a step without runtime outputs, a result with exit code 0 sets every
  known output and no other name. When the step has no known outputs, the
  result sets no `outputs`.
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

A mock makes the case invalid when:

- its key names no step or handler of the tested DAG;
- it sets `results` together with an inline result field, or sets an empty
  `results`;
- it sets a result field on a real step or on a `human.task` step;
- it sets `outputs` that [Named Outputs](#named-outputs) rejects;
- it sets `precondition` on a step that has no precondition with a command
  check or `eval`;
- a result sets `condition` on a step whose `repeat_policy.condition` is not
  a command check;
- it sets `review` on a step that does not wait for a person, or sets a
  `review` that [Steps That Wait for a Person](#steps-that-wait-for-a-person)
  rejects.

### Side-Effect-Free Actions

A side-effect-free action reads only its resolved `with` fields and the
run's values, and writes only its stdout, stderr, and outputs. The
side-effect-free actions are:

- `router.route`, including a router step written with the `value` and
  `routes` step fields
- `outputs.write`
- `log.write`
- `noop`

A `foreach` step is side-effect free when every body step has a
side-effect-free action and no other source of
[the fail-closed rule](#the-fail-closed-rule).

Every other action is not side-effect free. That includes command steps,
`decision.evaluate`, which calls a model provider, and `wait.duration`.

A step with a side-effect-free action that its mock does not replace runs
its real action, so a test exercises the routing and outputs the DAG
defines. A mock on such a step replaces the action only when it sets a
result field.

### The Fail-Closed Rule

A case is invalid, and none of its steps runs, when the tested DAG has a
source in this table that the case does not handle as the table says:

| Source | Handled by |
| --- | --- |
| A step action that is not side-effect free | A mock that replaces the action, including `{}`, or the step in `real` |
| A step precondition with a command check or `eval` (Spec 023) | `precondition` in the step's mock, or the step in `real` |
| A command-check `repeat_policy.condition` (Spec 075) | `condition` in every result of the step's mock, or the step in `real` |
| An object-form `output` entry with `from: file`, on a step whose action is not replaced | A mock that replaces the action, or the step in `real` |
| A continuous step with a side-effect-free action that is not replaced | A mock that sets a result field, or the step in `real` |
| A `foreach` body step with a source in this table, including an action that is not side-effect free | A mock of the `foreach` step |
| A waiting step | `review` in the step's mock, also for a step in `real` |
| A DAG-level precondition with a command check or `eval` | The case's `precondition` |
| A `params[].eval` (Spec 011) | A value for that param in `params` |
| A DAG `secrets` entry (Spec 069) | A value for that entry in `secrets` |
| A source in this table in the child DAG of a real step | Nothing. The case is invalid |

Rules:

- The rule covers every step and handler, reached or not, so the outcome
  does not depend on the branch a case takes. Handlers include the ones the
  base config adds.
- Spec 003 allows command substitution only in `params[].eval` and in
  precondition `eval`, and command checks run only in preconditions and
  repeat conditions. The table covers every command Dagu runs outside a step
  action.
- No person answers in a test, so a waiting step needs `review` even when
  its action is real.
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
| A `params[].eval` | `param <name>` |
| A DAG `secrets` entry | `secret <name>` |

Rules:

- `<step>` and `<body step>` are the step's `id`, or its `name` when it has
  no `id`.
- The message names what would run and the test field that handles it:
  `mocks`, `real`, `precondition`, `condition`, `review`, `params`, or
  `secrets`. For a step action, it names the action.
- Sources are listed in this order: params in declaration order, secrets in
  declaration order, DAG-level preconditions, steps in the order the DAG
  declares them, then handlers in the order `init`, `success`, `failure`,
  `abort`, `exit`, `wait`. Several sources of one step are listed in the
  order of the table.

```text
INVALID workflows/release.test.yaml: a failed build stops the deploy
  param build_date: eval runs a command; give it a value under params
  step deploy: action ssh.run has side effects; mock it or list it under real
  handler_on.failure: action mail.send has side effects; mock it or list it under real
```

A test cannot reach a real system by forgetting a mock. Running a real action
takes a line in `real`.

### Preconditions

Rules:

- A value-match precondition without `eval` (Spec 023) evaluates as in a
  real run, against mocked outputs and the case's params and env. A test
  exercises output-driven branches this way.
- A mock's `precondition` decides every command-check condition and every
  `eval` condition of the step, once per step start, as Spec 023 checks step
  preconditions. The value is the outcome of each such condition after
  `negate` applies: `met` passes it, and `not_met` fails it, which skips the
  step as Spec 023 defines.
- The step's other conditions still evaluate, so a step with
  `precondition: met` is still skipped when one of its value-match conditions
  fails.
- The case's `precondition` decides the DAG-level command-check and `eval`
  conditions in the same way. `not_met` aborts the run, as Spec 023 defines,
  and no step starts.
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

### Continuous Steps

With waits skipped, a continuous step whose last result repeats would run
without pause until `--timeout`. Under test:

- A replaced continuous step runs one execution per result and then stops
  repeating, unless its own repeat decision (Spec 075) stops it first. It
  ends with the status of its last execution, and `attempts` counts its
  executions.
- A continuous step with a side-effect-free action that its mock does not
  replace makes the case invalid, because nothing bounds it.
- A continuous step in `real` keeps its real retry and repeat intervals. Only
  its own repeat decision, `timeout_sec`, or `--timeout` ends it.

### Steps That Wait for a Person

`review` settles the waits of a waiting step:

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

- The run reaches its waiting checkpoint as a real run does (Spec 031).
  Independent branches keep running, and `handler_on.wait` runs once no
  other step is ready or running.
- At the checkpoint, the test settles each waiting step by its `review`,
  through the same completion path a person uses, and the run resumes under
  the same run ID. Nothing waits for a person.
- `review` settles every wait of the step with the same decision.
- For a `human.task` step, `approve` completes the task with `values` as its
  completion input, typed as `--inputs-json` types it (Spec 031). The step
  ends `succeeded`, and Spec 031 generates its outputs from that input.
  `values` that the task's form rejects make the case invalid, and so does
  `reject`.
- For a step with `approval`, the step's action runs first: for real when the
  step is real, and as its mock replaces it otherwise. When the step reaches
  its gate, `approve` approves the gate with `values` as its inputs, and
  `reject` rejects it as a person does: the step and the run end `rejected`.
  Each value is a scalar, given as its text. `values` that leave out a name
  that `approval.required` lists make the case invalid.
- A real waiting step takes its `review` like any other waiting step.

### Sub-DAG, Parallel, Foreach, and Node Steps

Rules:

- A mock that replaces a `dag.run` or `dag.enqueue` step replaces its child
  run. A mock that replaces a `parallel` step, or a step with `nodes`
  (Spec 089), replaces every child. No child run starts, is queued, or is
  recorded.
- The mock's result is the step's result, and its `outputs` are the step's
  outputs. `results` counts executions of the step, not children.
- A real `dag.run` step, or a real `parallel` step with `dag.run`, starts its
  children in the same case. A child gets no mocks and no `real`, so a source
  of the fail-closed rule in the child DAG makes the case invalid. So does a
  child DAG that cannot be known before the case starts, such as one named
  by a step output.
- A real `dag.enqueue` step, or a real `parallel` step with `dag.enqueue`,
  queues its children in the case directory, where no scheduler runs them.
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
