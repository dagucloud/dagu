# RFC 020: Workflow Tests

- **Status:** Draft
- **Date:** 2026-11-22
- **Issue:** https://github.com/dagucloud/dagu/issues/2864
- **Affected areas:** `cmd`, a new `dagtest` package, `executor/registry`,
  `runtime` (mock executor, skipped waits), `runtime/builtin` (side-effect
  opt-in), `persis/file/dag` (discovery, watcher, name lookup), `specs`,
  `conformance`, and `docs`

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
propagation all work as in a real run. A step whose action has side effects
must be mocked or explicitly listed as real. Otherwise the case is invalid and
nothing runs.

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
2. No action with side effects runs unless the test names it.
3. A DAG needs no change to be testable.
4. One file holds several scenarios of one DAG.
5. A test reads nothing from and writes nothing to the configured data
   directory.
6. Retry intervals, repeat intervals, and start delays are not waited for, so
   a mocked case finishes in well under a second.
7. Runs headless in CI, with an exit status and a report a CI system can read.

## Non-goals

- Testing the commands themselves. Mocks fix their results.
- Scheduler, queue, coordinator, and worker behavior. A test runs locally,
  in the CLI process.
- `type: agent` DAGs, where a model picks the next step.
- HTTP or service virtualization. A mock replaces a whole action.
- Timing and performance assertions.
- REST API, UI, and MCP surfaces. See [Open questions](#open-questions).

## Design

### Running tests

```sh
dagu test [flags] <test file or directory>...
```

A directory argument runs every test file under it, recursively. With no
argument, `dagu test` runs every test file under the DAGs directory, or under
the workflow directory in a project (Spec 001). Files run in path order.
Cases run one at a time, in file order.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--run` | none | Run only cases whose name matches this regular expression |
| `--timeout` | `5m` | Longest a case may run. A case that exceeds it is stopped and fails |
| `--format` | `text` | `text` or `json` |
| `--keep` | off | Keep each case's data directory and print its path |

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

A test file is never a DAG. DAG discovery in the DAGs directory and in a
project's workflow directory (Spec 001), the watcher on those directories,
and DAG name lookup all skip files ending in `.test.yaml` or `.test.yml`. The
DAG list does not show them, the scheduler does not schedule them, and
`dagu start release.test` fails with a message that names `dagu test`.

### The test file

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `dag` | path | no | DAG file, relative to the test file. Defaults to the DAG beside it |
| `params` | map | no | Run parameters for every case, as `dagu start -- name=value` takes them |
| `env` | map | no | Replaces DAG-level `env` entries of the same name. Values are literal |
| `secrets` | map | no | Value for each DAG `secrets` entry, by name |
| `mocks` | map | no | Mocks for every case, keyed by step |
| `real` | list | no | Steps that run their real action in every case |
| `cases` | list | yes | Non-empty list of cases |

Case fields:

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | Unique inside the file |
| `params` | map | no | Merged over the file's `params`, by name |
| `env` | map | no | Merged over the file's `env`, by name |
| `scheduled_at` | date or time | no | The schedule slot of the run, as `dagu dry --scheduled-at` takes it |
| `precondition` | `met` or `not_met` | no | Result of the DAG-level command-check preconditions |
| `mocks` | map | no | Replaces the file's mock for each step it names |
| `real` | list | no | Added to the file's `real` |
| `expect` | map | yes | Assertions |

The DAG loads as `dagu start` loads it: base config, `defaults`, and dotenv
files included. Unknown fields at any level are invalid.

### Steps and handlers

A key in `mocks`, `real`, or `expect.steps` names a step of the DAG's
entrypoint document by `name` or `id`, as Spec 009 allows. A lifecycle handler
is named `handler_on.<event>`, such as `handler_on.failure`. That includes a
handler the base config adds.

A step may not appear in both `mocks` and `real` for the same case.

### Mocks

A mock replaces the step's action with a result:

| Field | Type | Default | Meaning |
| --- | --- | --- | --- |
| `exit_code` | int | `0` | Exit code of the action. Non-zero fails the attempt |
| `stdout` | string | empty | Written to the step's stdout |
| `stderr` | string | empty | Written to the step's stderr |
| `outputs` | map | none | Named step outputs, published as Spec 012 publishes them |
| `results` | list | none | One result per execution. Each item takes the four fields above |

`results` excludes the four inline fields. Each execution of the action takes
the next result: the first attempt, each retry, and each repeat iteration.
After the last result, the last one repeats. An inline mock is a one-item
`results`.

A mocked result goes through the same capture path as a real action:

- `stdout` feeds string-form `output: VAR`, object-form `output`, and
  `stdout.outputs`, as in a real run.
- `outputs` is written to the step's `DAGU_OUTPUT_FILE` and parsed by Spec 012
  only when the attempt succeeds. A failed attempt publishes nothing.
- A `string` output takes a YAML string. A `json` output takes any YAML value
  and is published as its JSON text.
- The step's `env`, `with`, and `run` fields are still resolved. A reference
  that does not resolve fails the step as it would in a real run.

A mock is invalid when:

- it names a step that does not exist;
- it sets `outputs` on a step without `id`;
- a result with exit code 0 leaves out an output the step declares, or names
  one the step does not declare, when the step declares `outputs`.

Spec 012 would fail such a step at run time. Rejecting the mock at load
reports the authoring mistake directly.

`{}` is a valid mock: exit code 0, no output.

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

A case is invalid, and none of its steps run, when any step or handler of the
DAG:

- has an action that is not side-effect free, and is neither mocked nor in
  `real`; or
- has a command-check precondition (Spec 023), and its mock sets no
  `precondition`, and it is not in `real`; or
- waits for a person (`action: human.task` or `approval`), and its mock sets
  no `review`.

The rule covers every step, reached or not, so the result does not depend on
which branch a case takes. The error lists every offending step at once:

```text
INVALID workflows/release.test.yaml: a failed build stops the deploy
  step deploy: action ssh.run has side effects; mock it or list it under real
  handler_on.failure: action mail.send has side effects; mock it or list it under real
```

A test cannot reach a real system by forgetting a mock. Running a real action
takes a line in `real`.

### Preconditions

Value-match preconditions evaluate as in a real run, against mocked outputs
and the case's params and env. This is how output-driven branches are tested.

A mock's `precondition: met` treats the step's command-check conditions as
passed. `not_met` treats one as not met, so the step is skipped (Spec 023).
Value-match conditions of the same step still evaluate. The case-level
`precondition` does the same for DAG-level command checks.

### Steps that wait for a person

`review` settles the wait as soon as the step reaches `waiting`, through the
same completion path that `dagu` uses for a person:

```yaml
mocks:
  approve_release:
    review:
      decision: approve   # approve or reject
      values:
        reason: looks good
```

For `human.task`, `approve` completes the task and `values` become its form
outputs (Spec 031). `reject` is invalid for `human.task`. For a step with
`approval`, the step's action runs or is mocked first, and then the gate is
approved or rejected with `values` as its inputs. Push-back is not covered.
See [Open questions](#open-questions).

### Sub-DAGs, `parallel`, and `foreach`

A mock on a `dag.run` or `dag.enqueue` step replaces the child run. The mock's
result is the step's result, and its `outputs` are the step's outputs. No
child run starts.

A `dag.run` step in `real` starts its child in the same case. The child gets
no mocks, so the fail-closed rule applies to the child's steps: each must be
side-effect free.

On a `parallel` or `foreach` step, the mock applies to every item, and
`results` is invalid, because items do not run in a fixed order. Each item
takes the same result. Per-item results are an open question.

### Time

Under test, Dagu does not wait for retry intervals and backoff, repeat
intervals, or `delay_sec`. Execution order and counts are unchanged. A wait
action such as `wait.duration` is not side-effect free, so a test mocks it
like any other action. `timeout_sec` applies as in a real
run, and a mocked action returns immediately, so only a real step can reach
it.

### Isolation

Each case runs in a new temporary data directory. It holds that case's run
history, logs, artifacts, state (Spec 068), materialization records, and
locks. Nothing is read from the configured data directory, so `state` starts
empty and no queue or scheduler sees the run. The directory is removed when
the case ends, unless `--keep` is set.

The DAG's working directory is not changed. Mocked and side-effect-free
steps do not write to it. Real steps do.

Secrets resolve only from the test's `secrets` map, and every DAG `secrets`
entry needs a value there. Values are masked in logs and reports, as in a
real run. Command substitution in `params`, `env`, and `eval` (Spec 011)
runs as in a real run. A test replaces such a value through `params` or
`env`.

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
    handler_on.failure: succeeded
```

| Field | Meaning |
| --- | --- |
| `status` | Run status: `succeeded`, `failed`, `aborted`, `partially_succeeded`, or `rejected` |
| `steps.<step>` | A step status token, or an object with `status` and `attempts` |
| `status` (step) | A node status: `succeeded`, `failed`, `aborted`, `skipped`, `partially_succeeded`, `rejected`, or `not_started` |
| `attempts` | Times the step's action ran, counting retries and repeat iterations |

A step or handler that never started is `not_started`. A step that has no
assertion is not checked. `expect` must hold at least one assertion. An
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

`--format json` writes one JSON object to stdout:

```json
{"cases": [{"file": "workflows/release.test.yaml", "name": "a failed build stops the deploy", "result": "fail", "duration_ms": 61, "failures": [{"step": "deploy", "field": "status", "expected": "skipped", "actual": "aborted"}]}]}
```

### How it runs

```text
test file --decode--> cases
                        |
DAG file --spec--> ir.DAG --apply mocks--> ir.DAG' --runtime agent--> run status --assert--> report
                                                     (temp data dir)
```

The `dagtest` package decodes the test file, loads the DAG through `spec`,
and applies each case to a copy of the `ir.DAG`. A mocked step's executor is
replaced with a mock executor that holds the step's results. Its other fields
stay as written: `retry_policy`, `repeat_policy`, `continue_on`, `output`,
`outputs`, `env`, and preconditions. The case then runs through the same
local agent as `dagu start`, with paths pointing at the temporary directory
and waits skipped. The run plan, runner, and node code do not branch on
tests.

The mock executor is not an action authors can name in a DAG. `dagu validate`
rejects it like any unknown action.

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
2. **`dagtest` and the mock executor**: decoding, mocks, `results`, the
   fail-closed rule, isolation, assertions, and the text and JSON reports.
   DAG discovery, the watcher, and name lookup skip test files.
3. **Side-effect-free opt-in** for the first executors, and skipped waits in
   the runtime.
4. **Preconditions, reviews, and sub-DAG mocks.**
5. **Documentation**: writing tests, and running them in CI.

Steps 1 to 4 ship together. A release with only part of the rule would let a
test start a real command its author thought was mocked.

## Security

- The fail-closed rule is the main safeguard. A real action runs only when
  `real` names its step.
- Test files are usually committed. Their `secrets` values should be fake.
  The docs say so, and a real run never reads test files.
- Command substitution still runs. A DAG whose `env` runs a command with side
  effects still runs it under test. See [Open questions](#open-questions).

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
- **Unmocked steps succeed, as in `dagu dry`.** Safe, but a missing mock goes
  unnoticed and the test asserts behavior it never set up. Writing `{}` costs
  one line and states the intent.
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
- Handlers from the base config must be mocked in every test file. File-level
  `mocks` keep this to one entry per file.
- The runtime gains a skipped-waits option that only `dagu test` sets.

## Open questions

1. Should command substitution in `params`, `env`, and `eval` fail closed
   too, unless the test replaces the value?
2. Per-item results for `parallel` and `foreach`, keyed by item.
3. Mocks for the steps of a child run started by a `real` `dag.run` step.
4. Should assertions on outputs and stdout ship in the first version, given
   that side-effect-free steps run for real and produce outputs worth
   checking?
5. Push-back for waiting steps, which reruns upstream steps and would take
   their next results.
6. Should the MCP server expose `dagu test`, so an agent that writes a DAG
   can test it?
7. Should a test seed `state` (Spec 068) values?
8. Should `secrets` from the `env` provider resolve without a test value?
