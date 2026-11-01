// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  NodeStatus,
  NodeStatusLabel,
  ReferenceScopeOutputSource,
  ReferenceScopeStepOutputsKnown,
  ValidationIssueSeverity,
  ValidationIssueSource,
} from '@/api/v1/schema';
import type {
  ActionCatalogEntry,
  ApiDAGDetails,
  DagDocModel,
  ReferenceScope,
  RunOverlay,
  StepNameMap,
  StepOutputs,
  ValidationIssue,
} from '../contracts';

/** Three block steps in array form, with comments, a block scalar and a flow `depends`. */
export const yamlArraySteps = `# Nightly import
name: nightly-import
steps:
  # Download the source file.
  - name: fetch
    run: curl -sSf https://example.com/data.csv -o data.csv

  - name: transform   # keep the header row
    depends: [fetch]
    run: |
      head -n 1 data.csv
      sort data.csv > sorted.csv

  # Last step.
  - name: publish
    depends:
      - transform
    run: ./publish.sh sorted.csv
`;

/** Map form: the key of each entry is the step name. */
export const yamlMapSteps = `name: map-form
steps:
  # Entry point.
  fetch:
    run: curl -sSf https://example.com/data.csv -o data.csv

  parse:
    depends: [fetch]
    run: wc -l data.csv
`;

/** Shorthand strings next to a block step. */
export const yamlShorthandSteps = `name: shorthand
steps:
  - echo one
  - echo two   # shorthand step
  - name: three
    run: echo three
`;

/** A nested array is one parallel group between two ordinary steps. */
export const yamlNestedParallel = `name: ci
steps:
  - name: prepare
    run: ./prepare.sh
  # Parallel group.
  - - name: lint
      run: make lint
    - name: unit
      run: make test
  - name: report
    run: ./report.sh
`;

/** Chain type: each step runs after the previous step. */
export const yamlChain = `# Chain DAG
type: chain
name: release
steps:
  - name: build
    run: make build
  - name: test
    run: make test
  - name: deploy
    run: make deploy
`;

/** Main DAG plus one sub-DAG after the document separator. */
export const yamlMultiDoc = `name: parent
steps:
  - name: run-child
    action: dag.run
    with:
      dag: child

---
name: child
steps:
  - name: work
    run: echo work   # sub-DAG step
`;

/** Router step with two patterns and two targets. */
export const yamlRouter = `name: route-by-code
params:
  - CODE: "200"
steps:
  - name: pick
    action: router.route
    with:
      value: "\${params.CODE}"
      routes:
        "re:^5\\\\d\\\\d$": [server_error]
        "re:.*": [other]
  - name: server_error
    run: echo 5xx
  # Fallback branch.
  - name: other
    run: echo other
`;

/** Handlers for success and failure. */
export const yamlHandlers = `name: with-handlers
handler_on:
  success:
    run: echo ok
  failure:
    # Notify on failure.
    run: ./notify.sh
steps:
  - name: main
    run: ./job.sh
`;

/** YAML syntax error: the flow sequence is not closed. */
export const yamlBroken = `name: broken
steps:
  - name: a
    depends: [b, c
    run: echo a
`;

/** A tab in the top level keys: no step can be read, so the canvas freezes. */
export const yamlUnreadable = `\tname: broken
steps:
  - name: a
    run: echo a
`;

/** An http.request step feeds a jq.filter step through a step output reference. */
export const yamlHttpJq = `name: fetch-items
params:
  - API_URL: https://api.example.com/items
steps:
  - id: fetch
    name: fetch
    action: http.request
    with:
      method: GET
      url: \${params.API_URL}

  - id: names
    name: extract
    depends: [fetch]
    action: jq.filter
    with:
      filter: '[.[].name]'
      data: \${steps.fetch.outputs.body}
`;

/*
 * Range rule: a step range starts at the first character after "- " of its
 * entry and ends after the last character of its last value. Leading and
 * trailing comments, blank lines and the line end after the value are not
 * part of the range. An inline comment before the last value is part of it.
 * `to` is exclusive; `endColumn` is the column just after the last character.
 */
export const modelArraySteps: DagDocModel = {
  text: yamlArraySteps,
  ok: true,
  parseErrors: [],
  documentCount: 1,
  dagType: 'graph',
  stepsForm: 'array',
  steps: [
    {
      key: 'steps[0]',
      index: 0,
      form: 'block',
      explicitName: 'fetch',
      kind: 'run',
      runPreview: 'curl -sSf https://example.com/data.csv -o data.csv',
      depends: [],
      value: {
        name: 'fetch',
        run: 'curl -sSf https://example.com/data.csv -o data.csv',
      },
      range: { from: 79, to: 150, startLine: 5, startColumn: 5, endLine: 6, endColumn: 60 },
    },
    {
      key: 'steps[1]',
      index: 1,
      form: 'block',
      explicitName: 'transform',
      kind: 'run',
      runPreview: 'head -n 1 data.csv',
      depends: ['fetch'],
      value: {
        name: 'transform',
        depends: ['fetch'],
        run: 'head -n 1 data.csv\nsort data.csv > sorted.csv\n',
      },
      range: { from: 156, to: 285, startLine: 8, startColumn: 5, endLine: 12, endColumn: 33 },
    },
    {
      key: 'steps[2]',
      index: 2,
      form: 'block',
      explicitName: 'publish',
      kind: 'run',
      runPreview: './publish.sh sorted.csv',
      depends: ['transform'],
      value: {
        name: 'publish',
        depends: ['transform'],
        run: './publish.sh sorted.csv',
      },
      range: { from: 306, to: 383, startLine: 15, startColumn: 5, endLine: 18, endColumn: 33 },
    },
  ],
  handlers: [],
};

export const namesArraySteps: StepNameMap = {
  byKey: {
    'steps[0]': 'fetch',
    'steps[1]': 'transform',
    'steps[2]': 'publish',
  },
  byName: {
    fetch: 'steps[0]',
    transform: 'steps[1]',
    publish: 'steps[2]',
  },
};

export const detailsArraySteps: ApiDAGDetails = {
  name: 'nightly-import',
  steps: [
    {
      name: 'fetch',
      commands: [
        {
          command: 'curl',
          args: ['-sSf', 'https://example.com/data.csv', '-o', 'data.csv'],
        },
      ],
    },
    {
      name: 'transform',
      script: 'head -n 1 data.csv\nsort data.csv > sorted.csv\n',
      depends: ['fetch'],
    },
    {
      name: 'publish',
      commands: [{ command: './publish.sh', args: ['sorted.csv'] }],
      depends: ['transform'],
      // Reference to an earlier step that is not in `depends`.
      inferredDepends: ['fetch'],
    },
  ],
};

export const issuesSample: ValidationIssue[] = [
  {
    severity: ValidationIssueSeverity.error,
    source: ValidationIssueSource.build,
    message: 'step "transform": unknown dependency "fetch2"',
    stepName: 'transform',
    stepPath: 'steps[1]',
    fieldPath: 'steps[1].depends',
    line: 9,
    column: 5,
  },
  {
    severity: ValidationIssueSeverity.warning,
    source: ValidationIssueSource.warning,
    message: 'the DAG has no description',
    fieldPath: 'description',
  },
  {
    severity: ValidationIssueSeverity.notice,
    source: ValidationIssueSource.valueReference,
    message: 'output "rows" of step "transform" is not declared',
    stepName: 'publish',
    stepPath: 'steps[2]',
    fieldPath: 'steps[2].run',
    line: 18,
    column: 5,
    reason: 'unknown_output_name',
  },
];

export const scopeHttpJq: ReferenceScope = {
  params: [
    {
      name: 'API_URL',
      reference: '${params.API_URL}',
      defaultValue: 'https://api.example.com/items',
    },
  ],
  consts: [],
  env: [{ name: 'HOME', reference: '${env.HOME}' }],
  secrets: [],
  steps: [
    {
      name: 'fetch',
      id: 'fetch',
      stepPath: 'steps[0]',
      upstream: [],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
      outputs: [
        {
          name: 'body',
          reference: '${steps.fetch.outputs.body}',
          source: ReferenceScopeOutputSource.action,
          type: 'string',
        },
        {
          name: 'status',
          reference: '${steps.fetch.outputs.status}',
          source: ReferenceScopeOutputSource.action,
          type: 'integer',
        },
      ],
    },
    {
      name: 'extract',
      id: 'names',
      stepPath: 'steps[1]',
      upstream: ['fetch'],
      outputsKnown: ReferenceScopeStepOutputsKnown.static,
      outputs: [
        {
          name: 'result',
          reference: '${steps.names.outputs.result}',
          source: ReferenceScopeOutputSource.action,
        },
      ],
    },
  ],
};

export const stepOutputsSample: StepOutputs = {
  stepName: 'fetch',
  stepId: 'fetch',
  status: NodeStatus.Success,
  statusLabel: NodeStatusLabel.succeeded,
  outputs: [
    { name: 'status', value: '200', truncated: false, size: 3 },
    // Cut at 65536 bytes; `size` is the length before the cut.
    {
      name: 'body',
      value: '[{"name":"alpha"},{"name":"beta"}]',
      truncated: true,
      size: 131072,
    },
  ],
};

export const overlaySample: RunOverlay = {
  fetch: {
    status: NodeStatus.Success,
    statusLabel: 'succeeded',
    startedAt: '2026-01-15T02:00:00Z',
    finishedAt: '2026-01-15T02:00:03Z',
    retryCount: 0,
    doneCount: 1,
  },
  transform: {
    status: NodeStatus.Failed,
    statusLabel: 'failed',
    startedAt: '2026-01-15T02:00:03Z',
    finishedAt: '2026-01-15T02:00:04Z',
    retryCount: 2,
    doneCount: 1,
    error: 'exit status 1',
  },
  publish: {
    status: NodeStatus.Running,
    statusLabel: 'running',
    startedAt: '2026-01-15T02:00:05Z',
    retryCount: 0,
    doneCount: 0,
  },
};

export const catalogSample: ActionCatalogEntry[] = [
  {
    name: 'run',
    namespace: 'core',
    title: 'Run command',
    description: 'Run a shell command or script.',
    source: 'core',
    template: { run: '' },
  },
  {
    name: 'http.request',
    namespace: 'http',
    title: 'HTTP request',
    description: 'Send an HTTP request.',
    source: 'builtin',
    template: { action: 'http.request', with: { method: 'GET', url: '' } },
  },
  {
    name: 'jq.filter',
    namespace: 'jq',
    title: 'jq filter',
    description: 'Filter JSON data with jq.',
    source: 'builtin',
    template: { action: 'jq.filter', with: { filter: '.', data: '' } },
  },
  {
    name: 'router.route',
    namespace: 'router',
    title: 'Router',
    description: 'Choose the next steps from a value.',
    source: 'builtin',
    template: { action: 'router.route', with: { value: '', routes: {} } },
  },
  {
    name: 'send-slack',
    namespace: 'custom',
    title: 'Send Slack message',
    source: 'custom',
    template: { action: 'send-slack', with: {} },
  },
];
