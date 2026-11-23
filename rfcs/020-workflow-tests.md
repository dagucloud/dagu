# RFC 020: Workflow Tests

- **Status:** Accepted
- **Date:** 2026-11-22
- **Accepted:** 2026-11-23
- **Issue:** https://github.com/dagucloud/dagu/issues/2864
- **Affected areas:** `cmd`, a new `dagtest` package, `executor/registry`,
  `runtime` (mock injection at executor construction, skipped waits),
  `runtime/agent` (notifications, trace export, working directory, DAG
  container, SSH client, and tools under test), `runtime/builtin`
  (side-effect opt-in), `persis/file/dag` (discovery, watcher, name lookup),
  `specs`, `conformance`, and `docs`

## Summary

Test how a DAG behaves without running its commands. A test file sits beside
the DAG it tests, as a Go test file sits beside its package. It replaces the
DAG's step actions with fixed results and states the run and step statuses
each case must end with:

```yaml
# workflows/release.test.yaml, beside workflows/release.yaml
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

```sh
dagu test workflows/
```

Each case runs the DAG through the same runner as `dagu start`, in a
temporary data directory. Only the mocked actions change. Dependencies,
preconditions, routing, retries, `continue_on`, handlers, and status
propagation all work as in a real run. Step actions with side effects, and
the commands Dagu runs outside actions, must be replaced by the test or
listed as real. Otherwise the case is invalid and nothing runs.
Notifications, trace export, and run setup that reaches beyond the case,
such as the DAG-level container and SSH client, are off unless a step is
real.

## Problem statement

Steps call agents, cloud APIs, CI systems, and databases. Running the real
workflow to check its branching costs money and time, and it changes real
systems. The current options:

| Option | What it checks | What it leaves |
| --- | --- | --- |
| `dagu validate` | Schema, references, dependency graph | Behavior |
| `dagu dry` (Spec 064) | Real runner with every action skipped; host checks; `--scheduled-at` | Every step succeeds (`internal/runtime/runner.go:1347`, `:1679`), and a check never changes the exit status. Failure branches, retries, `continue_on`, failure handlers, and conditions on outputs are never exercised |
| `dagu start` against staging | Real behavior | Real side effects and cost, slow, needs credentials |
| A copy of the DAG with fake commands | Behavior of the copy | The copy drifts from the DAG it stands for |
| Env-gated commands inside the DAG | Behavior | The production DAG is edited to be testable |
| Unit tests of each script | The script | Dagu's orchestration |

An author's model of the engine is often wrong in ways only a real run shows.
In the issue's failure scenario, the author expects `review` to be `skipped`
when `test` fails. Dagu marks it `aborted` (`internal/runtime/runner.go:1608`).
A separate simulator would agree with the author and be wrong.

## Prior art

| Tool | Mechanism |
| --- | --- |
| AWS Step Functions Local | A mock config file defines test cases per state machine. Each state maps to mocked responses, chosen by attempt (`"0"`, `"1-2"`), each a `Return` or a `Throw` |
| Temporal test environment | Runs a workflow against mocked activity implementations, with time skipping so timers and retries finish at once |
| Dagster | `execute_in_process` runs a job in one process with its resources replaced |
| Prefect | `prefect_test_harness` runs flows against a temporary database |
| Airflow | `dag.test()` runs every task in one process; mocking is left to Python |

The common design: run the real engine in isolation, replace each external
call with a scripted result, pick results by attempt, and skip waits.

## Goals

1. Test a DAG's orchestration through the same runner as `dagu start`:
   dependencies, preconditions, routing, retries, repeats, `continue_on`,
   handlers, and status propagation.
2. Nothing with side effects runs unless the test names it: no step action,
   no command Dagu runs outside an action, no notification, no container,
   and no SSH connection.
3. A DAG needs no change to be testable.
4. One file holds several scenarios of one DAG.
5. A test reads nothing from and writes nothing to the configured data
   directory, and only real steps write files outside the case directory,
   apart from the run's control socket.
6. Retry intervals, repeat intervals, and start delays are not waited for, so
   a mocked case finishes in well under a second.
7. Runs headless in CI, with an exit status and a report a CI system can read.

## Non-goals

- Testing the commands themselves. Mocks fix their results.
- Scheduler, queue, coordinator, and worker behavior. A test runs locally,
  in the CLI process.
- The restart loop of a service DAG (Spec 080): when the scheduler starts
  the next run, `restart_wait_sec`, and `unless-stopped`. A test runs one run
  of the DAG.
- `type: agent` DAGs, where a model picks the next step.
- `type: build` DAGs (Spec 033) in the first version. See
  [Build workflows](#build-workflows).
- HTTP or service virtualization. A mock replaces a whole action.
- Timing and performance assertions.
- REST API, UI, and MCP surfaces. See [Open questions](#open-questions).

## Design

### Running tests

```sh
dagu test [flags] <test file or directory>...
```

A directory argument runs every test file under it, recursively. With no
argument, `dagu test` runs every test file under the DAGs directory. Files
run in path order.
Cases run one at a time, in file order.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--run` | none | Run only cases whose name matches this regular expression |
| `--timeout` | `5m` | Longest a case may run. A case that exceeds it is stopped and fails |
| `--format` | `text` | `text` or `json` |
| `--keep` | off | Keep each case's data directory and print its path |
| `--init` | off | Write a test file for the DAG file given as the one argument. See [Writing tests faster](#writing-tests-faster) |
| `--update` | off | Rewrite each case's `expect` from its actual run. See [Writing tests faster](#writing-tests-faster) |

Exit status:

| Status | Meaning |
| --- | --- |
| 0 | Every case passed |
| 1 | At least one case failed |
| 2 | At least one test file or case is invalid |

The highest applicable status wins. An invalid file runs none of its cases.
The other files still run.

### Where test files live

A test file ends in `.test.yaml` or `.test.yml` and sits beside the DAG it
tests:

```text
workflows/
  release.yaml
  release.test.yaml
  etl/
    daily.yaml
    daily.test.yaml
```

- Without `dag`, a test file tests the DAG beside it with the same name
  minus `.test`: `release.test.yaml` tests `release.yaml`, or `release.yml`
  when there is no `release.yaml`. Without `dag` and without such a DAG, the
  test file is invalid.
- `dag` names another DAG file, relative to the test file, for a test file
  kept elsewhere.
- One test file per DAG is the convention. Its scenarios are `cases`.
- Moving or renaming a DAG together with its test file keeps the test
  working.

A test file is never a DAG. DAG discovery in the DAGs directory, the watcher
on that directory, and DAG name lookup all skip files ending in `.test.yaml`
or `.test.yml`. The DAG list does not show them, the scheduler does not
schedule them, and `dagu start release.test` fails with a message that names
`dagu test`.

### The test file

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `dag` | path | no | DAG file, relative to the test file. Defaults to the DAG beside it |
| `params` | map | no | Run parameters for every case, as `dagu start -- name=value` takes them |
| `env` | map | no | Replaces DAG-level `env` entries of the same name. Values are literal |
| `secrets` | map | no | Values for DAG `secrets` entries and SSH credential references, by name. Needed only in a case with a `real` step. See [Isolation](#isolation) |
| `mocks` | map | no | Mocks for every case, keyed by step |
| `default_mock` | map | no | A mock for every step and handler that needs one and that `mocks` and `real` do not name. See [The default mock](#the-default-mock) |
| `real` | list | no | Steps that run their real action in every case |
| `cases` | list | yes | Non-empty list of cases |

Case fields:

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | Unique inside the file |
| `params` | map | no | Merged over the file's `params`, by name |
| `env` | map | no | Merged over the file's `env`, by name |
| `scheduled_at` | date or time | no | The schedule slot of the run, as `dagu dry --scheduled-at` takes it |
| `trigger` | trigger type | no | `context.trigger.type` of the run (Spec 017). Defaults to `manual` |
| `precondition` | `met` or `not_met` | no | Result of the DAG-level preconditions that run a command: command checks and `eval` with command substitution |
| `mocks` | map | no | Replaces the file's mock for each step it names |
| `default_mock` | map | no | Replaces the file's `default_mock` |
| `real` | list | no | Added to the file's `real` |
| `expect` | map | yes | Assertions |

The DAG loads as `dagu start` loads it: base config, `defaults`, and dotenv
files included. Unknown fields at any level are invalid.

`trigger` takes any Spec 017 trigger type except `unknown` and `subdag`. It
sets only the value a DAG reads: `restart` tests the branch a service DAG
takes on its restarted runs, and `retry` does not retry a run.

### Steps and handlers

A key in `mocks`, `real`, or `expect.steps` names a step of the DAG's
entrypoint document by `name` or `id`, as Spec 009 allows. A lifecycle handler
is named `handler_on.<event>`, such as `handler_on.failure`. That includes a
handler the base config adds.

A step in `real` may also have a mock that sets only step fields (see
[Mocks](#mocks)).

### Mocks

A mock has two kinds of fields. Result fields script what the action
returns, once per execution. Step fields script what happens around the
action, once for the step.

Result fields:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `exit_code` | int | `0` | Exit code of the action. Non-zero fails the attempt |
| `stdout` | string | empty | Written to the step's stdout |
| `stderr` | string | empty | Written to the step's stderr |
| `outputs` | map | none | Named step outputs |
| `condition` | `met` or `not_met` | none | Result of a command-check `repeat_policy.condition` after this execution |
| `results` | list | none | One result per execution. Each item takes the result fields above |

Step fields:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `precondition` | `met` or `not_met` | none | Result of the step's preconditions that run a command. See [Preconditions](#preconditions) |
| `review` | map | none | Decision for each wait of the step. See [Steps that wait for a person](#steps-that-wait-for-a-person) |

Step fields sit beside `results` or the inline result fields, never inside a
result:

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

With `retry_policy.limit: 1`, Dagu checks the precondition once, the first
attempt fails, the retry succeeds, and the approval gate is approved when the
step reaches it. Spec 023 checks step preconditions once per step start, not
once per attempt, so `precondition` is a step field. `review` settles every
wait of the step with the same decision.

`results` excludes the inline result fields. Each execution of the action
takes the next result: the first attempt, each retry, and each repeat
iteration. After the last result, the last one repeats, except on a
continuous step (see [Continuous steps](#continuous-steps)). An inline mock
is a one-item `results`.

A mock replaces the step's action when it sets a result field, or when the
action is neither side-effect free nor in `real`. The result then defaults
to exit code 0 with no output. A mock with only step fields keeps the real
action of a side-effect-free step or a step in `real`. A mock that sets a
result field on a step in `real` is invalid.

How a result reaches the run:

- `stdout` and `stderr` are the step's captured streams. String-form
  `output: VAR` captures `stdout` as in a real run.
- `outputs` supplies every named output the step publishes, whatever its
  source as Spec 007 lists them: the output file (Spec 012),
  `output_schema`, object-form `output`, `stdout.outputs`, or the action
  itself. Dagu does not parse `stdout` or read a file to publish them. Only a
  successful attempt publishes outputs.
- A `string` output takes a YAML string. A `json` output takes any YAML value
  and is published as its JSON text.
- The step's `env`, `with`, and `run` fields resolve as in a real run, with
  the step's own executor metadata (see [How it runs](#how-it-runs)). A
  reference to an unavailable value is preserved as text with a passive
  notice, as Spec 003 defines, and does not fail the step.
- Checks that the real executor makes when it is built do not run, because a
  mocked step's executor is never built.
- A mocked step reads no file: not its `stdin`, and not a file named by an
  object-form `output`.

A mock is invalid when:

- it names a step that does not exist;
- it sets `outputs` on a step without `id`;
- a result with exit code 0 leaves out a named output the step declares, or
  names one it does not declare, when the step declares named outputs.

A real run would fail such a step or publish nothing for it. Rejecting the
mock at load reports the authoring mistake directly.

`{}` is a valid mock. On a step whose action is not side-effect free, it
replaces the action with exit code 0 and no output. On a side-effect-free
step, it sets nothing and the real action runs.

### The default mock

`default_mock` is a mock, with the same fields, that counts as the mock of
every step and handler that needs handling under
[the fail-closed rule](#the-fail-closed-rule) and that `mocks` and `real` do
not name. It never runs anything real, so it keeps the rule's guarantee and
removes the per-step `{}` lines, including those for handlers from the base
config:

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

- Its fields apply where they handle a source. Result fields replace the
  action; `precondition`, `condition`, and `review` handle their rows of the
  table. A source that none of its fields handles stays unhandled.
- It follows the rules of an ordinary mock: on a side-effect-free step, `{}`
  sets nothing, and the real action runs.
- On a step that declares named outputs, a successful default result
  publishes each with an empty string, or `null` for a `json` output. An
  explicit mock must still list its declared outputs.
- The report lists the steps and handlers that took the default mock, so an
  author sees which steps the test never scripted.

### Side-effect-free actions run for real

An executor may register itself as side-effect free in
`registry.ExecutorCapabilities`. Such an action reads only its resolved `with`
fields and the run's values, and writes only its stdout, stderr, and outputs.
The first set:

- `router.route`, and the `value` and `routes` step fields
- `outputs.write`
- `log.write`
- `noop`

`decision.evaluate` is not one: it calls a model provider. Other executors opt
in after review, one at a time. The default is not side-effect free.

An unmocked side-effect-free step runs its real action, so a test exercises
the routing and output logic the DAG owns. A mock on such a step replaces the
action only when it sets a result field, so a mock that sets only
`precondition` keeps the real routing.

### The fail-closed rule

A case is invalid, and none of its steps run, when the DAG has any of the
following and the test does not handle it as the table says:

| Source | Handled by |
| --- | --- |
| A step action that is not side-effect free | A mock that replaces the action, including `{}` and the default mock, or the step in `real` |
| A step precondition with a command check, or an `eval` with command substitution (Spec 023) | `precondition` in the step's mock, or the step in `real` |
| A command-check `repeat_policy.condition` (Spec 075) | `condition` in every result of the step's mock, or the step in `real` |
| A file named by an object-form `output` of an unmocked step | A mock that replaces the action, including the default mock, or the step in `real` |
| A `foreach` body step that is not side-effect free | A mock of the whole `foreach` step |
| A step that waits for a person (`human.task`, `approval`) and needs required values | `review` in the step's mock, also for a step in `real` |
| A DAG `secrets` entry or an SSH credential given as a secret reference, in a case with a `real` step | A value in `secrets` |
| A DAG-level precondition with a command check, or an `eval` with command substitution | The case's `precondition` |
| A `params[].eval` with command substitution (Spec 011) | A value for that param in `params` |

`default_mock` counts as the mock of every source it covers. No person
answers in a test: a wait with no required values is approved automatically
(see [Steps that wait for a person](#steps-that-wait-for-a-person)), and a
wait that needs values takes `review`, even when the step's action is real. An `eval` without backtick or `$()` command substitution runs
no command (Spec 011), so it evaluates as in a real run and needs nothing
from the test. Spec 003 allows command substitution only in
`params[].eval` and precondition `eval`, and command checks run only in
preconditions and repeat conditions, so the table covers every command Dagu
runs outside a step action.

The rule covers every step and handler, reached or not, so the result does
not depend on which branch a case takes. The error lists every offending
source at once:

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

Each invalid case ends with the YAML that would handle every listed source,
ready to paste into the test file. A test cannot reach a real system by
forgetting a mock. Running a real action
takes a line in `real`.

### Preconditions

Value-match preconditions evaluate as in a real run, against mocked outputs
and the case's params and env, including an `eval` without command
substitution. This is how output-driven branches are tested.

A mock's `precondition: met` treats the step's command checks and its `eval`
conditions with command substitution as met. `not_met` treats them as not met, so the step is skipped
(Spec 023). The step's other value-match conditions still evaluate. The
case-level `precondition` does the same for DAG-level preconditions, and
`not_met` aborts the run, as Spec 023 defines.

### Repeat conditions

A value-match `repeat_policy.condition` evaluates as in a real run. A
command-check one takes the `condition` of the result that the execution
used, with the meaning Spec 075 gives `met` and `not_met` for `while` and
`until`.

### Continuous steps

A continuous step has a `repeat` mode and no `limit` (Spec 083), such as the
heartbeat of a service DAG. With waits skipped, a mock whose last result
repeats would run it without pause until `--timeout`. Under test:

- A mocked continuous step runs once per result and then stops repeating,
  unless the step's own repeat rule stops it first. It ends with the status
  of its last execution. `attempts` counts its executions.
- An unmocked continuous step that is side-effect free makes the case
  invalid, because nothing bounds it. Mock it with `results`.
- A continuous step in `real` keeps its real intervals, and only `--timeout`
  ends it.
- A `limit` that comes from a reference, such as `${params.n}`, is decided
  when it resolves. A positive number bounds the step as written. A value
  that leaves the step without a limit makes it continuous: a mock's results
  bound it, a step in `real` keeps its real intervals, and any other one
  fails the case at run time with that reason.

### Steps that wait for a person

The run reaches the waiting checkpoint as a real run does: its status
becomes `waiting`, `handler_on.wait` runs, and the wait notification is
recorded. The test then settles each waiting step through the same
completion path that `dagu` uses for a person, and the run resumes.

A wait with no required values, a `human.task` whose form requires no field
or an `approval` gate with no `required` inputs, is approved automatically
with no values when its mock sets no `review`. The report lists each wait it
approved this way. A wait that needs values, or that the test rejects, takes
`review`:

```yaml
mocks:
  approve_release:
    review:
      decision: approve   # approve or reject
      values:
        reason: looks good
```

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `decision` | `approve` or `reject` | yes | How the wait is settled |
| `values` | map | no | Form values of a `human.task`, or inputs of an `approval` gate |

For `human.task`, `approve` completes the task and `values` become its form
outputs (Spec 031). `reject` is invalid for `human.task`. For a step with
`approval`, the step's action runs first, for real when the step is in
`real` and as mocked otherwise. The gate is then approved or rejected with
`values` as its inputs. Push-back is not covered. See
[Open questions](#open-questions).

### Sub-DAGs, `parallel`, and `foreach`

A mock on a `dag.run` or `dag.enqueue` step replaces the child run, and on a
`parallel` step it replaces every child. The mock's result is the step's
result, and its `outputs` are the step's outputs. No child run starts.
`results` counts executions of the step, not children. A mock on a step with
`nodes` (RFC 013, Spec 089) replaces every child, as on a `parallel` step.

A `dag.run` step in `real` starts its child in the same case. The child gets
no mocks, so the fail-closed rule applies to the child's steps: each must be
side-effect free. A child that cannot be resolved before the case starts
makes the case invalid. A `dag.enqueue` step in `real` queues its child in
the case directory, where nothing runs it.

A mock on a `foreach` step replaces the whole construct: item expansion, the
body, `collect`, and the aggregate status. The mock's result is the step's
result, its `outputs` are the step's outputs, and string-form `output: VAR`
captures its `stdout` in place of the aggregate. A whole-construct mock ends
the step `succeeded` or `failed`.

An unmocked `foreach` step runs for real when every body step is
side-effect free, over items taken from mocked upstream outputs. A body step
that is not side-effect free makes the case invalid unless the whole
`foreach` step is mocked. Mocks for individual body steps are deferred.

### Build workflows

A test of a `type: build` DAG (Spec 033) is invalid in the first version.
After a build step's action succeeds, Dagu verifies the step's path output
and commits it beside its final path, under the DAG's working directory. Its
reuse decision also hashes the input files there. A mock produces no file,
so the step would fail, and a mock that wrote one would write outside the
case directory. Supplying path outputs is an open question.

### Time

Under test, Dagu does not wait for retry intervals and backoff, repeat
intervals, or `delay_sec`. Execution order and counts are unchanged. Steps
in `real` keep their retry and repeat intervals: a real action reaches a
real system, and its intervals protect that system. A wait
action such as `wait.duration` is not side-effect free, so a test mocks it
like any other action. `timeout_sec` applies as in a real
run, and a mocked action returns immediately, so only a real step can reach
it.

### Run retries

A DAG-level `retry_policy` retries a failed run. In a real run the scheduler
starts each retry; a test has no scheduler, so `dagu test` starts the
retries itself, in the same case directory, without waiting for the
interval, up to the policy's limit. Each retry runs as the scheduler's retry
would. Mock results keep counting across run attempts: a step's executions
in every attempt take its results in order. Handlers and notifications
follow the rule a real run follows while a retry remains. The run-level
`attempts` assertion counts the run attempts.

### Isolation

Each case runs in a new temporary case directory. Its data directory holds
the case's run history, logs, artifacts, state (Spec 068), materialization
records, live-process records, and locks. Nothing is read from the
configured data directory, so `state` starts empty and no queue or scheduler
sees the run. The case directory is removed when the case ends, unless
`--keep` is set.

**Live runs.** A test runs next to the live runs of the same DAG. A service
DAG has one run at a time (Spec 080), and the check reads the run history
(`internal/runtime/manager.go:626`) and the live-process records
(`internal/intake/local.go:133`). Both are in the case's data directory, so
the check neither refuses the test while the service runs nor sees the
test's run. The scheduler does not see the test's run either, so it starts
no restart after it.

**Files.** The DAG's working directory is not created or changed, and only
`real` steps write outside the case directory:

- The `stdout` and `stderr` redirect paths of every other step are written
  under the case directory, not at their configured paths. Artifact
  redirects go to the case's artifact directory.
- A mocked step reads no file (see [Mocks](#mocks)). An unmocked step with a
  file-sourced output is invalid unless it is real.
- The run reads its inputs as `dagu start` does: the DAG file, the base
  config, dotenv files, and declared file dependencies (Spec 035).
- The agent's control socket is created where a real run creates it, under
  a name unique to the run, and removed when the run ends. A Unix socket
  path is limited to about 100 bytes, which a temporary case directory can
  exceed. It is the one file outside the case directory that every case
  creates.

**Run-level side effects.** The agent does more than run steps. Under test:

| Behavior | Under test |
| --- | --- |
| `mail_on` notifications and step `mail_on_error` | Not sent. The report lists each notification the run would have sent |
| OpenTelemetry trace export (`otel`) | Off |
| An explicit `working_dir` that does not exist | Not created, unless the case has a `real` step |
| DAG-level `container` | Not created, unless the case has a `real` step, which runs in it |
| DAG-level `ssh` client, which reads keys and known hosts | Not created, unless the case has a `real` step |
| Declared `tools` | Not installed, unless the case has a `real` step |
| Native resource limits (`resources.limits` without a DAG-level `container`) | Not applied, unless the case has a `real` step |

A `real` step is the explicit opt-in to side effects, so the working
directory, container, SSH client, tools, and resource limits it needs are
set up as in a real run. A case without one runs on a CI host that has none of them. Tools
install into the configured tools directory, the one write outside the case
directory that only `real` causes.

**Secrets.** Secrets never resolve through their providers under test.

- A case without a `real` step needs no secret values, because a mocked
  step never uses them. Each DAG `secrets` entry resolves to a placeholder,
  `<secret:NAME>`, unless `secrets` gives a value. SSH credentials given as
  secret references are not resolved at all, since no SSH client exists.
- A case with a `real` step needs a value in `secrets` for every DAG
  `secrets` entry and every SSH credential reference, because Dagu cannot
  tell which of them the real step reads. A missing one makes the case
  invalid.
- Values and placeholders are masked in logs and reports, as in a real run.

Committed test files therefore hold no credentials, and a case does not
depend on the CI machine's environment.

### Assertions

```yaml
expect:
  status: failed
  steps:
    test: failed
    route: aborted
    implement:
      status: succeeded
      attempts: 3
    collect:
      outputs:
        summary: 3 files changed
      stdout:
        contains: collected 3 files
    handler_on.failure: succeeded
  notifications:
    - kind: mail_on.failure
```

| Field | Meaning |
| --- | --- |
| `status` | Run status: `succeeded`, `failed`, `aborted`, `partially_succeeded`, or `rejected` |
| `steps.<step>` | A step status token, or an object with `status` and `attempts` |
| `status` (step) | A node status: `succeeded`, `failed`, `aborted`, `skipped`, `partially_succeeded`, `rejected`, or `not_started` |
| `attempts` | Times the step's action ran, counting retries and repeat iterations |
| `attempts` (run) | Run attempts, counting DAG-level retries (see [Run retries](#run-retries)) |
| `outputs` (step) | Expected named outputs. A `string` output matches exactly. A `json` output matches when every listed key has an equal value, recursively; unlisted keys are ignored, and arrays and scalars match exactly |
| `stdout`, `stderr` (step) | `contains` (text) and `matches` (RE2 regular expression) on the stream of the step's last execution. Both may be set |
| `notifications` | The notifications the run would have sent, compared without regard to order. Each item has `kind` (`mail_on.failure`, `mail_on.success`, `mail_on.wait`, or `mail_on_error`), and optional `step` and `to`. `[]` asserts that none would be sent |

A step or handler that never started is `not_started`. A step that has no
assertion is not checked, and neither are notifications without
`notifications`. An output assertion on a mocked step checks only what the
test supplied; it is allowed, and is most useful on side-effect-free steps,
which run for real. `expect` must hold at least one assertion. An
assertion on an unknown step or an unknown status token makes the case
invalid.

A case passes when the run reaches a terminal status and every assertion
holds. The report lists every failed assertion, not only the first.

### Report

```text
PASS workflows/release.test.yaml: deploys the built image (0.08s)
FAIL workflows/release.test.yaml: a failed build stops the deploy (0.06s)
  step deploy: expected skipped, got aborted (upstream step build failed)
2 cases: 1 passed, 1 failed
```

Under each case, the report also lists the steps that took the default mock,
the waits approved automatically, and the notifications the run would have
sent.

`--format json` writes one JSON object to stdout:

```json
{"cases": [{"file": "workflows/release.test.yaml", "name": "a failed build stops the deploy", "result": "fail", "duration_ms": 61, "failures": [{"step": "deploy", "field": "status", "expected": "skipped", "actual": "aborted"}]}]}
```

### Writing tests faster

- **`dagu test --init <dag file>`** writes the DAG's test file beside it, with
  a mock for every source the fail-closed rule needs and one case whose
  `expect` holds the result of a run with those mocks. It refuses to replace
  an existing file.
- **`dagu test --update`** runs the cases and rewrites each case's `expect`
  from its actual run, so a change in behavior shows as a diff to review.
  It never removes an assertion the author wrote. Exact facts take the
  actual values: run and step `status` and `attempts`, `notifications`, and
  the values of outputs the case already asserts. `contains` and `matches`
  stay as written and still report a failure. A step with no assertion
  gains its `status`. It rewrites only `expect` blocks, and leaves invalid
  and unfinished cases as they are.
- **Editor support.** Dagu publishes a JSON Schema for test files beside the
  DAG schema, so editors validate and complete `*.test.yaml`.

### How it runs

```text
test file --decode--> cases
                        |
DAG file --spec--> ir.DAG --apply mocks--> ir.DAG' --runtime agent--> run status --assert--> report
                                                     (temp data dir)
```

The `dagtest` package decodes the test file, loads the DAG through `spec`,
and applies each case to a copy of the `ir.DAG`:

- Replaced preconditions and repeat conditions take their scripted results.
- Redirect paths move into the case directory.

A mocked step keeps every field as written, including its action, executor
type, and `with` fields. Command, script, and path resolution and config
warnings read the executor's registry metadata by that type
(`internal/executor/registry/capabilities.go`), so a mocked step resolves
its fields exactly as the real step does. A mocked `ssh.run` command leaves
`$HOME` for the remote shell, as a real one does.

The mock is injected where the node builds its executor
(`internal/runtime/node.go:958`). For a mocked step, the runner's executor
factory returns a mock that holds the step's results instead of building the
real executor. No mock action type exists, so a DAG cannot name one.

The case then runs through the same local agent as `dagu start`, with paths
pointing at the case directory. Three options exist only for tests:

- the runner builds mocked steps' executors through the injected factory;
- the runner skips waits;
- the agent turns off notifications and trace export, and creates the
  working directory, container, SSH client, and tools only for a `real`
  step.

Dependency evaluation, status propagation, retries, and handlers do not
branch on tests.

## Example: an agent pipeline

```yaml
# workflows/change.yaml
params:
  - name: task
    type: string
    required: true
steps:
  - id: plan
    run: agent plan "${params.task}"
    outputs:
      - name: risk
  - id: implement
    depends: plan
    run: agent implement
    retry_policy:
      limit: 2
      interval_sec: 60
  - id: test
    depends: implement
    run: make test
  - id: route
    depends: test
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
handler_on:
  failure:
    action: mail.send
    with:
      to: oncall@example.com
      subject: change failed
```

```yaml
# workflows/change.test.yaml
params:
  task: Add retries to the uploader
mocks:
  plan:
    outputs:
      risk: low
  implement: {}
  test: {}
  review: {}
  merge: {}
  handler_on.failure: {}
cases:
  - name: low risk merges without review
    expect:
      status: succeeded
      steps:
        merge: succeeded
        review: skipped
        handler_on.failure: not_started

  - name: high risk goes to review
    mocks:
      plan:
        outputs:
          risk: high
    expect:
      steps:
        review: succeeded
        merge: skipped

  - name: flaky implement passes on the third attempt
    mocks:
      implement:
        results:
          - exit_code: 1
          - exit_code: 1
          - exit_code: 0
    expect:
      steps:
        implement:
          status: succeeded
          attempts: 3

  - name: failing tests stop the run and send mail
    mocks:
      test:
        exit_code: 2
        stderr: FAIL ./uploader
    expect:
      status: failed
      steps:
        test: failed
        route: aborted
        handler_on.failure: succeeded
```

`route` runs for real, so the cases test the routing the DAG defines. The
two retry intervals of 60 seconds are not waited for. Removing the
`handler_on.failure` mock makes every case invalid before any step runs.

## Performance

No performance risk to the server, scheduler, or workers. `dagu test` is a
CLI command and touches no request, tick, or SSE path.

- **Per case.** One temporary data directory holding one run's status file
  and logs, the size a real run of the same DAG writes. It is removed when
  the case ends.
- **Idle cost.** None. Nothing runs between invocations.
- **Configured data directory.** Not read or written.
- **DAG discovery.** One file-name suffix check per file. Test files are not
  read, and the watcher reloads nothing when one changes.
- **Upgrade.** None.

## Compatibility and rollout

DAG YAML, persisted run data, the REST API, and the coordinator protocol do
not change.

The one break is the file name. DAG names may contain dots
(`internal/ir/validator.go:11`), so `foo.test.yaml` is a valid DAG today.
After the upgrade it is a test file: it leaves the DAG list and its schedule
stops. The release notes must say so. `dagu test` reports such a file, one
with top-level `steps` and no `cases`, as invalid and asks for a rename. The
server reads no extra file to detect it.

An older binary has no `dagu test`. It lists test files beside DAGs as DAGs
that fail to load, because they have no `steps`. A downgrade is safe, but
those entries show until the test files are moved or the binary is upgraded
again.

Rollout:

1. **Spec** for the test file and `dagu test`, beside Spec 064.
2. **`dagtest` and mock injection**: decoding, mocks, `results`, the
   fail-closed rule, isolation, assertions, and the text and JSON reports.
   DAG discovery, the watcher, and name lookup skip test files.
3. **Side-effect-free opt-in** for the first executors, mock injection and
   skipped waits in the runtime, and the agent options for notifications,
   trace export, the working directory, the DAG container, the SSH client,
   and tools.
4. **Preconditions, repeat conditions, `params[].eval`, redirects, reviews,
   and sub-DAG and `foreach` mocks.**
5. **Documentation**: writing tests, and running them in CI.
6. **Authoring tools**, after the first release: `--init` and `--update`.

Steps 1 to 4 ship together, with the default mock, automatic approval,
placeholder secrets, run retries, the assertions, and the JSON Schema. A release with only part of the rule would let a
test start a real command its author thought was mocked.

## Security

- The fail-closed rule is the main safeguard. A real action runs only when
  `real` names its step. The same rule covers the commands Dagu runs outside
  actions: `params[].eval`, precondition `eval` and command checks, and
  repeat command checks.
- Without a `real` step, a case sends no notification, exports no trace,
  creates no working directory or container, opens no SSH client, installs
  no tool, and writes no file outside its case directory.
- Test files are usually committed. Their `secrets` values should be fake.
  The docs say so, and a real run never reads test files.

## Verification and acceptance criteria

1. A test of an unchanged DAG runs with every `run` step mocked and starts no
   process for any mocked step.
2. A case with an unmocked `run`, `ssh.run`, `http.request`, `mail.send`, or
   `chat.completion` step is invalid, lists every such step, and runs
   nothing. The command exits with status 2.
3. An unmocked `router.route` step routes on a mocked output, and the
   unmatched targets end `skipped`.
4. A mock with `results: [1, 1, 0]` exit codes on a step with
   `retry_policy.limit: 2` ends `succeeded` with `attempts: 3`, without
   waiting for the retry interval.
5. A failed mocked step ends its dependents `aborted` and runs
   `handler_on.failure`. With `continue_on: failed` on the failed step, its
   dependents run.
6. Mocked `outputs` resolve in a later step's `${steps.<id>.outputs.<name>}`
   and value-match preconditions. A failed attempt publishes none.
7. A mock that leaves out a declared output or names an undeclared one is
   invalid.
8. `precondition: not_met` skips the step, and its dependents end `skipped`.
9. `review: {decision: approve}` completes a `human.task`, and its values
   resolve as its outputs. An unmocked `human.task` makes the case invalid.
10. After a test, the configured data directory is unchanged, and no
    temporary directory remains unless `--keep` is set.
11. A failed assertion exits with status 1 and lists every failed assertion
    of the case.
12. A `release.test.yaml` beside `release.yaml` is not listed, scheduled, or
    reloaded when it changes. `dagu start release.test` fails. `dagu test`
    with no argument runs it against `release.yaml`.
13. A `foo.test.yaml` with top-level `steps` and no `cases` is reported by
    `dagu test` as invalid, with a request to rename it.
14. A `params[].eval` with command substitution and no value in the test, a
    step precondition whose `eval` has command substitution and no
    `precondition` in its mock, and a DAG-level command check without the
    case's `precondition` each make the case invalid, and no command runs.
15. A step with `repeat: while` and a command-check condition, mocked with
    results whose `condition` is `met`, `met`, `not_met`, runs three times.
16. A failing case with no `real` step, on a DAG with `mail_on.failure`,
    `otel`, and a DAG-level `container`, sends no mail, exports no span, and
    creates no container. The report lists the failure notification.
17. A mocked step with `stdout: result.txt` leaves the working directory
    unchanged, and the redirect file is in the case directory.
18. A mocked `foreach` step ends `succeeded` without expanding items. An
    unmocked `foreach` whose body has a `run` step makes the case invalid.
19. On a DAG with an explicit `working_dir` that does not exist and a
    DAG-level `ssh` whose key file is missing, a case with no `real` step
    passes, creates no directory, and reads no key.
20. A mocked `run` step and a mocked `ssh.run` step resolve their command,
    script, and path fields to the same text as a real run of the same step.
21. A mocked step whose `env` references an output its dependency never
    published starts and takes its mock result, with the reference kept as
    text, as in a real run.
22. A test of a service DAG passes while a run of the same DAG runs on the
    same host. The live run is not stopped, and no restart follows the
    test's run.
23. A mocked continuous step with three results that all succeed ends
    `succeeded` with `attempts: 3`, well before `--timeout`. An unmocked
    side-effect-free continuous step makes the case invalid.
24. A case with `trigger: restart` resolves `${context.trigger.type}` to
    `restart`. A case without `trigger` resolves it to `manual`.
25. A test of a `type: build` DAG is invalid, and the error names Spec 033.
26. A step in `real` with an `approval` gate and `review: {decision:
    approve}` runs its real action and continues without waiting. The same
    step with a result field in its mock is invalid.
27. A mock with `precondition`, `review`, and two `results` on a step with
    `retry_policy.limit: 1` checks the precondition once, runs twice, and is
    approved once.
28. A `params[].eval` without command substitution evaluates as in a real
    run and needs no value in the test.
29. A mocked step with `nodes` starts no child run.
30. A `real` step with `retry_policy.interval_sec` waits its interval. A
    mocked step with the same policy does not.
31. A case with no `real` step applies no `resources.limits` and resolves
    no SSH credential given as a secret reference.
32. A `real` `dag.run` step whose child has an unmocked `run` step makes the
    case invalid.
33. A case with `default_mock: {}` and no `mocks`, on a DAG whose steps all
    run commands, starts no process and lists every step that took the
    default mock.
34. Under `default_mock: {}`, a step that declares outputs publishes each
    with an empty value.
35. A case with no `real` step passes on a DAG with `secrets` and no test
    values, and a step that prints a secret shows it masked. A case with a
    `real` step and a missing secret value is invalid.
36. A `human.task` with no required field and an approval gate with no
    required input are approved automatically and listed in the report. A
    form with a required field and no `review` makes the case invalid.
37. A run reaches `waiting` and runs `handler_on.wait` before the test
    settles the waiting step.
38. A DAG with a DAG-level `retry_policy.limit: 2`, whose first attempt
    fails and second succeeds, ends `succeeded` with run-level `attempts: 2`
    and no wait.
39. A `string` output assertion matches exactly. A `json` output assertion
    that lists a subset of the keys matches. `stdout` `contains` and
    `matches` check the last execution's stream.
40. `notifications: [{kind: mail_on.failure}]` passes for a failing run with
    `mail_on.failure`, and `notifications: []` fails it.
41. With `repeat_policy.limit: ${params.n}`, a mocked step stops after its
    results when `n` leaves it without a limit, and runs three times when
    `n` is 3.
42. The YAML an invalid case prints, pasted into the test file, makes the
    case valid.
43. `dagu test --init` writes a valid test file whose case passes, and
    `--update` rewrites `expect` to the actual results.

## Alternatives considered

- **Extend `dagu dry` with mocks.** Spec 064 fixes `dagu dry` at exit status
  0 with every step succeeded. Dry runs also skip step preparation, so their
  behavior already differs from a real run. Mocks and assertions would
  change that contract, and command-line flags cannot express several cases
  or results per attempt.
- **Mocks inside the DAG**, such as a `mock` field active under a flag. The
  production DAG is edited for tests, which the issue rules out, and every
  reader of the DAG sees test data.
- **A separate simulator.** It would encode the author's model instead of
  the engine's, as the `skipped` versus `aborted` example shows.
- **Run unmocked steps for real**, as the issue's example does: it asserts
  `review` succeeded without mocking it, so `agent review` would run. One
  missing line would call a real agent, deploy, or send mail from CI.
- **Unmocked steps succeed implicitly, as in `dagu dry`.** A missing mock
  would go unnoticed. `default_mock` gives the same brevity only when the
  test asks for it, and the report lists the steps that took it.
- **Every secret needs a test value.** Committed test files would carry fake
  credentials, and nothing gains in safety: only a `real` step can use a
  secret, and a case with one still needs every value.
- **Run real steps in a sandbox**, such as a container per step. It costs a
  container runtime and still calls the APIs the steps reach.
- **A `tests/` directory at the project root.** A project root is often a
  repository root that already has a `tests/` for its own code. The tree would
  mirror `workflows/` and drift from it. Paths such as
  `../../workflows/etl/daily.yaml` break when a DAG moves. A plain DAGs
  directory would need a second convention.
- **Go's `_test.yaml` suffix.** Names such as `smoke_test.yaml` and
  `integration_test.yaml` are likely real DAGs, ones that run test suites.
  `.test.yaml`, as in JavaScript's `*.test.ts`, is rarer as a DAG name.

## Consequences

- Authors can test branches, retries, and handlers in CI in milliseconds, and
  agents that write DAGs can check behavior as well as syntax.
- Every new executor decides whether it is side-effect free. The default
  keeps it mocked.
- Tests name steps, so renaming a step breaks its tests. This is intended.
- Mocks can drift from what the real commands do. A test shows that the
  orchestration is right for the given results, not that the commands
  produce them.
- `default_mock` covers handlers from the base config, so a test file need
  not name them.
- A DAG whose params use `eval` needs those params in every test file.
- The runtime gains a skipped-waits option that only `dagu test` sets.

## Open questions

1. Mocks for individual `foreach` body steps, and per-item results for
   `foreach` and `parallel`, keyed by item.
2. Mocks for the steps of a child run started by a `real` `dag.run` step.
3. Push-back for waiting steps, which reruns upstream steps and would take
   their next results.
4. Should the MCP server expose `dagu test`, so an agent that writes a DAG
   can test it?
5. Should a test seed `state` (Spec 068) values?
6. Should a case assert the samples and events a continuous step records
   (Spec 083)?
7. How should a test of a `type: build` DAG supply path outputs, such as
   file content in the mock, staged and committed inside the case
   directory?
