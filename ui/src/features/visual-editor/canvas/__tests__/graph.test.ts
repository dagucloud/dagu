// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus } from '@/api/v1/schema';
import type { InputProvenance } from '../../run/inputProvenance';
import type {
  CanvasGraph,
  CanvasGraphInput,
  StepRunState,
  StepView,
  ValidationIssue,
} from '../../contracts';
import {
  detailsArraySteps,
  issuesSample,
  modelArraySteps,
  namesArraySteps,
  overlaySample,
} from '../../testing/fixtures';
import type { CanvasNodeWithRoutes } from '../types';
import { buildCanvasGraph, buildStepResolver } from '../graph';
import {
  makeLargeGraphInput,
  makeModel,
  makeNames,
  makeStep,
} from './fixtures';

describe('buildStepResolver', () => {
  it('resolves explicit names', () => {
    const resolve = buildStepResolver(modelArraySteps, namesArraySteps);
    expect(resolve('fetch')).toBe('steps[0]');
    expect(resolve('publish')).toBe('steps[2]');
    expect(resolve('missing')).toBeNull();
  });

  it('resolves ids and prefers explicit names', () => {
    const model = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'a', id: 'x' }),
      makeStep({ key: 'steps[1]', explicitName: 'x', id: 'b' }),
    ]);
    const resolve = buildStepResolver(model, makeNames(model));
    expect(resolve('b')).toBe('steps[1]');
    expect(resolve('x')).toBe('steps[1]');
  });

  it('resolves server names of unnamed steps', () => {
    const model = makeModel([makeStep({ key: 'steps[0]' })]);
    const resolve = buildStepResolver(model, makeNames(model));
    expect(resolve('step_1')).toBe('steps[0]');
  });

  it('never resolves handlers or inherited names', () => {
    const handler = makeStep({
      key: 'handler_on.failure',
      index: null,
      explicitName: 'h',
    });
    const step = makeStep({ key: 'steps[0]', explicitName: 'a' });
    const model = makeModel([step], { handlers: [handler] });
    const names = makeNames(model);
    names.byName.h = 'handler_on.failure';
    const resolve = buildStepResolver(model, names);
    expect(resolve('h')).toBeNull();
    expect(resolve('toString')).toBeNull();
  });
});

const noIssues = { byStep: {}, dag: [] };

function nodeOf(graph: CanvasGraph, key: string): CanvasNodeWithRoutes {
  const node = graph.nodes.find((n) => n.key === key);
  if (!node) {
    throw new Error(`no node ${key}`);
  }
  return node;
}

function runOf(name: string): StepRunState {
  const run = overlaySample[name];
  if (!run) {
    throw new Error(`no run ${name}`);
  }
  return run;
}

function inputOf(
  model: CanvasGraphInput['model'],
  names = makeNames(model)
): CanvasGraphInput {
  return { model, names, issues: noIssues };
}

describe('buildCanvasGraph nodes', () => {
  it('projects steps in YAML order', () => {
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: noIssues,
    });
    expect(graph.nodes.map((n) => n.key)).toEqual([
      'steps[0]',
      'steps[1]',
      'steps[2]',
    ]);
    expect(nodeOf(graph, 'steps[1]')).toMatchObject({
      key: 'steps[1]',
      serverName: 'transform',
      label: 'transform',
      subtitle: 'head -n 1 data.csv',
      kind: 'run',
      lane: 'main',
    });
    expect(nodeOf(graph, 'steps[1]').action).toBeUndefined();
    expect(graph.editableEdges).toBe(true);
  });

  it('uses the action as subtitle and falls back to the key', () => {
    const model = makeModel([
      makeStep({ key: 'steps[0]', kind: 'action', action: 'http.request' }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: { byKey: {}, byName: {} },
      issues: noIssues,
    });
    expect(nodeOf(graph, 'steps[0]')).toMatchObject({
      label: 'steps[0]',
      subtitle: 'http.request',
      action: 'http.request',
    });
    expect(nodeOf(graph, 'steps[0]').serverName).toBeUndefined();
  });

  // The Graph view draws a sub-DAG as a subroutine, a fan-out of sub-DAGs as
  // a stack and a router as a diamond: the nodes carry what those shapes need.
  it('tells the sub-DAG a call step runs, and a fan-out', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'call',
        action: 'dag.run',
        value: { action: 'dag.run', with: { dag: 'greeter' } },
      }),
      makeStep({
        key: 'steps[1]',
        kind: 'call',
        value: { call: 'reporter', parallel: { items: ['a', 'b'] } },
      }),
      makeStep({ key: 'steps[2]', kind: 'run', value: { run: 'echo' } }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: { byKey: {}, byName: {} },
      issues: noIssues,
    });
    expect(nodeOf(graph, 'steps[0]')).toMatchObject({
      subDag: 'greeter',
      subtitle: '\u2192 greeter',
    });
    expect(nodeOf(graph, 'steps[0]').parallel).toBeUndefined();
    expect(nodeOf(graph, 'steps[1]')).toMatchObject({
      subDag: 'reporter',
      parallel: true,
    });
    expect(nodeOf(graph, 'steps[2]').subDag).toBeUndefined();
  });

  it('carries the repeat policy of a step', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'run',
        value: {
          run: 'poll',
          repeat_policy: { repeat: 'while', interval_sec: 10 },
        },
      }),
      makeStep({ key: 'steps[1]', kind: 'run', value: { run: 'echo' } }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: { byKey: {}, byName: {} },
      issues: noIssues,
    });
    expect(nodeOf(graph, 'steps[0]').repeat).toEqual({
      mode: 'while',
      intervalSec: 10,
    });
    expect(nodeOf(graph, 'steps[1]').repeat).toBeUndefined();
  });

  it('tells what a router routes on', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        kind: 'router',
        action: 'router.route',
        value: {
          action: 'router.route',
          with: { value: '${params.ENV}', routes: { prod: ['a'], dev: [] } },
        },
      }),
    ]);
    const graph = buildCanvasGraph({
      model,
      names: { byKey: {}, byName: {} },
      issues: noIssues,
    });
    expect(nodeOf(graph, 'steps[0]')).toMatchObject({
      routeValue: '${params.ENV}',
      routes: ['prod', 'dev'],
    });
  });

  it('uses the server name as label of an unnamed step', () => {
    const model = makeModel([makeStep({ key: 'steps[0]' })]);
    const graph = buildCanvasGraph(inputOf(model));
    expect(nodeOf(graph, 'steps[0]').label).toBe('step_1');
  });

  // The caption of a compact card is the label: the name, or the id when the
  // name is empty.
  it('labels a step with an empty name by its id', () => {
    const named = makeModel([
      makeStep({ key: 'steps[0]', explicitName: '', id: 'load_orders' }),
    ]);
    const graph = buildCanvasGraph({
      ...inputOf(named),
    });
    expect(nodeOf(graph, 'steps[0]').label).toBe('load_orders');
    const both = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'Load', id: 'load_orders' }),
    ]);
    expect(nodeOf(buildCanvasGraph(inputOf(both)), 'steps[0]').label).toBe(
      'Load'
    );
  });

  it('puts handlers in their lane after the steps', () => {
    const step = makeStep({ key: 'steps[0]', explicitName: 'a' });
    const model = makeModel([step], {
      handlers: [
        makeStep({
          key: 'handler_on.success',
          index: null,
          handler: 'success',
          runPreview: 'echo ok',
        }),
        makeStep({
          key: 'handler_on.failure',
          index: null,
          handler: 'failure',
        }),
      ],
    });
    const graph = buildCanvasGraph(inputOf(model));
    expect(graph.nodes.map((n) => [n.key, n.lane])).toEqual([
      ['steps[0]', 'main'],
      ['handler_on.success', 'handler'],
      ['handler_on.failure', 'handler'],
    ]);
    expect(nodeOf(graph, 'handler_on.success').subtitle).toBe('echo ok');
    expect(graph.edges).toEqual([]);
  });
});

describe('buildCanvasGraph issues and run state', () => {
  const [error, warning, notice] = issuesSample as [
    ValidationIssue,
    ValidationIssue,
    ValidationIssue,
  ];
  const grouped = {
    byStep: {
      'steps[1]': [error, { ...error, message: 'second' }, warning],
      'steps[2]': [notice],
    },
    dag: [],
  };

  it('counts errors and warnings, not notices', () => {
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: grouped,
    });
    expect(nodeOf(graph, 'steps[0]').issues).toEqual({
      errors: 0,
      warnings: 0,
    });
    expect(nodeOf(graph, 'steps[1]').issues).toEqual({
      errors: 2,
      warnings: 1,
    });
    expect(nodeOf(graph, 'steps[2]').issues).toEqual({
      errors: 0,
      warnings: 0,
    });
  });

  // parity:canvas-node-issue-badges
  it('keeps the messages of the errors and warnings, not of notices', () => {
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: grouped,
    });
    expect(nodeOf(graph, 'steps[1]').issueMessages).toEqual({
      errors: [error.message, 'second'],
      warnings: [warning.message],
    });
    expect(nodeOf(graph, 'steps[0]').issueMessages).toBeUndefined();
    expect(nodeOf(graph, 'steps[2]').issueMessages).toBeUndefined();
  });

  it('attaches the run state by server name', () => {
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: noIssues,
      overlay: overlaySample,
    });
    expect(nodeOf(graph, 'steps[1]').run).toBe(runOf('transform'));
    expect(nodeOf(graph, 'steps[2]').run?.status).toBe(runOf('publish').status);
  });

  it('has no run state without overlay entry', () => {
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: noIssues,
      overlay: { fetch: runOf('fetch') },
    });
    expect(nodeOf(graph, 'steps[0]').run).toBe(runOf('fetch'));
    expect(nodeOf(graph, 'steps[1]').run).toBeUndefined();
  });

  it('falls back to the explicit name for the run state', () => {
    const model = makeModel([makeStep({ key: 'steps[0]', explicitName: 'a' })]);
    const graph = buildCanvasGraph({
      model,
      names: { byKey: {}, byName: {} },
      issues: noIssues,
      overlay: { a: runOf('fetch') },
    });
    expect(nodeOf(graph, 'steps[0]').run).toBe(runOf('fetch'));
  });
});

describe('buildCanvasGraph depends edges', () => {
  it('draws one edge for each written dependency', () => {
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: noIssues,
    });
    expect(graph.edges).toEqual([
      {
        id: 'depends:steps[0]->steps[1]',
        source: 'steps[0]',
        target: 'steps[1]',
        kind: 'depends',
        written: 'fetch',
      },
      {
        id: 'depends:steps[1]->steps[2]',
        source: 'steps[1]',
        target: 'steps[2]',
        kind: 'depends',
        written: 'transform',
      },
    ]);
  });

  it('resolves ids and skips unknown, self and duplicate entries', () => {
    const model = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'a', id: 'first' }),
      makeStep({
        key: 'steps[1]',
        explicitName: 'b',
        depends: ['first', 'a', 'ghost', 'b'],
      }),
    ]);
    const graph = buildCanvasGraph(inputOf(model));
    expect(graph.edges).toHaveLength(1);
    expect(graph.edges[0]).toMatchObject({
      id: 'depends:steps[0]->steps[1]',
      written: 'first',
    });
  });

  it('orders edges by target, then by written order', () => {
    const model = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'a' }),
      makeStep({ key: 'steps[1]', explicitName: 'b' }),
      makeStep({ key: 'steps[2]', explicitName: 'c', depends: ['b', 'a'] }),
    ]);
    const ids = buildCanvasGraph(inputOf(model)).edges.map((e) => e.id);
    expect(ids).toEqual([
      'depends:steps[1]->steps[2]',
      'depends:steps[0]->steps[2]',
    ]);
  });

  it('gives handlers no edges', () => {
    const step = makeStep({ key: 'steps[0]', explicitName: 'a' });
    const handler = makeStep({
      key: 'handler_on.failure',
      index: null,
      depends: ['a'],
    });
    const model = makeModel([step], { handlers: [handler] });
    expect(buildCanvasGraph(inputOf(model)).edges).toEqual([]);
  });
});

describe('buildCanvasGraph inferred edges', () => {
  it('adds an edge for a step output reference', () => {
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: noIssues,
      details: detailsArraySteps,
    });
    expect(graph.edges.map((e) => [e.kind, e.id])).toEqual([
      ['depends', 'depends:steps[0]->steps[1]'],
      ['depends', 'depends:steps[1]->steps[2]'],
      ['inferred', 'inferred:steps[0]->steps[2]'],
    ]);
    expect(graph.edges[2]).not.toHaveProperty('written');
  });

  it('skips pairs that already have a depends edge', () => {
    const details = {
      name: 'x',
      steps: [{ name: 'transform', inferredDepends: ['fetch', 'fetch'] }],
    };
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: noIssues,
      details,
    });
    expect(graph.edges.map((e) => e.kind)).toEqual(['depends', 'depends']);
  });

  it('skips unknown names and self references', () => {
    const details = {
      name: 'x',
      steps: [
        { name: 'publish', inferredDepends: ['publish', 'ghost'] },
        { name: 'ghost', inferredDepends: ['fetch'] },
      ],
    };
    const graph = buildCanvasGraph({
      model: modelArraySteps,
      names: namesArraySteps,
      issues: noIssues,
      details,
    });
    expect(graph.edges.filter((e) => e.kind === 'inferred')).toEqual([]);
  });
});

describe('buildCanvasGraph order edges', () => {
  const chain = (steps: StepView[]) =>
    buildCanvasGraph(inputOf(makeModel(steps, { dagType: 'chain' })));

  it('links consecutive steps and locks the edges', () => {
    const graph = chain([
      makeStep({ key: 'steps[0]', explicitName: 'a' }),
      makeStep({ key: 'steps[1]', explicitName: 'b' }),
      makeStep({ key: 'steps[2]', explicitName: 'c' }),
    ]);
    expect(graph.edges.map((e) => [e.kind, e.id])).toEqual([
      ['order', 'order:steps[0]->steps[1]'],
      ['order', 'order:steps[1]->steps[2]'],
    ]);
    expect(graph.editableEdges).toBe(false);
  });

  it('links each step of a parallel group to the next group', () => {
    const graph = chain([
      makeStep({ key: 'steps[0]', explicitName: 'a' }),
      makeStep({ key: 'steps[1]', explicitName: 'b', parallelGroup: 0 }),
      makeStep({ key: 'steps[2]', explicitName: 'c', parallelGroup: 0 }),
      makeStep({ key: 'steps[3]', explicitName: 'd' }),
    ]);
    expect(graph.edges.map((e) => e.id)).toEqual([
      'order:steps[0]->steps[1]',
      'order:steps[0]->steps[2]',
      'order:steps[1]->steps[3]',
      'order:steps[2]->steps[3]',
    ]);
  });

  it('keeps two adjacent groups apart', () => {
    const graph = chain([
      makeStep({ key: 'steps[0]', parallelGroup: 0 }),
      makeStep({ key: 'steps[1]', parallelGroup: 0 }),
      makeStep({ key: 'steps[2]', parallelGroup: 1 }),
      makeStep({ key: 'steps[3]', parallelGroup: 1 }),
    ]);
    expect(graph.edges.map((e) => e.id)).toEqual([
      'order:steps[0]->steps[2]',
      'order:steps[0]->steps[3]',
      'order:steps[1]->steps[2]',
      'order:steps[1]->steps[3]',
    ]);
  });

  it('adds no order edge to other DAG types', () => {
    const steps = [
      makeStep({ key: 'steps[0]' }),
      makeStep({ key: 'steps[1]' }),
    ];
    const graph = buildCanvasGraph(inputOf(makeModel(steps)));
    expect(graph.edges).toEqual([]);
  });

  it('locks the edges of an agent DAG', () => {
    const model = makeModel([makeStep({ key: 'steps[0]' })], {
      dagType: 'agent',
    });
    expect(buildCanvasGraph(inputOf(model)).editableEdges).toBe(false);
  });
});

describe('buildCanvasGraph agent DAGs', () => {
  const agent = (details?: object, overlay?: object) =>
    buildCanvasGraph({
      ...inputOf(
        makeModel(
          [
            makeStep({ key: 'steps[0]', explicitName: 'plan' }),
            makeStep({ key: 'steps[1]', explicitName: 'build' }),
          ],
          { dagType: 'agent' }
        )
      ),
      details: details as never,
      overlay: overlay as never,
    });

  // The steps are the actions of the agent: they hang from its node.
  it('puts an agent node at the root of the steps', () => {
    const graph = agent({ tasks: [{ name: 'a' }, { name: 'b' }] });

    expect(graph.nodes.map((node) => node.key)).toEqual([
      'agent',
      'steps[0]',
      'steps[1]',
    ]);
    expect(graph.nodes[0]).toMatchObject({
      kind: 'agent',
      label: 'Agent',
      lane: 'main',
      taskCount: 2,
      subtitle: '2 tasks',
    });
    expect(
      graph.edges.map((edge) => [edge.kind, edge.source, edge.target])
    ).toEqual([
      ['agent', 'agent', 'steps[0]'],
      ['agent', 'agent', 'steps[1]'],
    ]);
    expect(graph.editableEdges).toBe(false);
  });

  it('says one task in the singular and nothing when the tasks are unknown', () => {
    expect(agent({ tasks: [{ name: 'a' }] }).nodes[0]?.subtitle).toBe('1 task');
    const unknown = agent().nodes[0]!;
    expect(unknown.taskCount).toBeUndefined();
    expect(unknown.subtitle).toBeUndefined();
  });

  it('takes the state of the agent from its own step of the run', () => {
    const run = {
      status: 1,
      statusLabel: 'running',
      retryCount: 0,
      doneCount: 0,
    };
    expect(agent(undefined, { __agent__: run }).nodes[0]?.run).toEqual(run);
  });

  it('adds no agent node to another type of DAG', () => {
    const graph = buildCanvasGraph(
      inputOf(makeModel([makeStep({ key: 'steps[0]' })]))
    );
    expect(graph.nodes.map((node) => node.kind)).not.toContain('agent');
  });
});

describe('buildCanvasGraph agent hub and tools', () => {
  const yaml = `type: agent
llm:
  provider: anthropic
  model: claude-opus-5
  max_tool_iterations: 30
  system: |
    You ship a small release.
    Never publish without approval.
steps:
  - name: plan
    description: Write the plan.
    run: echo plan
  - name: build
    action: dag.run
tasks:
  - name: planned
    description: Finished when the plan exists.
  - name: published
`;
  const build = (extra: object = {}, text = yaml) =>
    buildCanvasGraph({
      ...inputOf(
        makeModel(
          [
            makeStep({
              key: 'steps[0]',
              explicitName: 'plan',
              description: 'Write the plan.',
            }),
            makeStep({
              key: 'steps[1]',
              explicitName: 'build',
              action: 'dag.run',
              kind: 'action',
            }),
          ],
          { dagType: 'agent', text }
        )
      ),
      ...extra,
    });
  const hub = (graph: CanvasGraph) =>
    (graph.nodes[0] as CanvasNodeWithRoutes).hub!;
  const tool = (graph: CanvasGraph, key: string) =>
    (graph.nodes.find((n) => n.key === key) as CanvasNodeWithRoutes).tool;

  it('reads the model, the prompt, the limit and the tasks from the YAML', () => {
    const graph = build();
    expect(hub(graph)).toEqual({
      provider: 'anthropic',
      model: 'claude-opus-5',
      maxIterations: 30,
      systemFirstLine: 'You ship a small release.',
      tasks: [
        { name: 'planned', description: 'Finished when the plan exists.' },
        { name: 'published' },
      ],
    });
    expect(graph.nodes[0]).toMatchObject({
      taskCount: 2,
      subtitle: '2 tasks',
    });
  });

  it('takes the tasks of the server when the YAML has none', () => {
    const graph = build({ details: { tasks: [{ name: 'a' }] } }, 'type: agent');
    expect(hub(graph).tasks).toEqual([{ name: 'a' }]);
    expect(graph.nodes[0]?.taskCount).toBe(1);
  });

  it('marks every step as a tool card with its description', () => {
    const graph = build();
    expect(tool(graph, 'steps[0]')).toEqual({ description: 'Write the plan.' });
    expect(tool(graph, 'steps[1]')).toEqual({});
    expect(tool(graph, 'agent')).toBeUndefined();
  });

  // The run is not in the graph: the graph is what the YAML decides.
  it('has the same graph with and without a run', () => {
    const run = {
      status: 4,
      statusLabel: 'succeeded',
      retryCount: 0,
      doneCount: 0,
    };
    const strip = (graph: CanvasGraph) =>
      graph.nodes.map((node) => ({ ...node, run: undefined }));
    expect(strip(build({ overlay: { plan: run } }))).toEqual(strip(build()));
  });

  it('adds no tool to another type of DAG', () => {
    const graph = buildCanvasGraph(
      inputOf(makeModel([makeStep({ key: 'steps[0]' })]))
    );
    expect(graph.nodes.some((n) => (n as CanvasNodeWithRoutes).tool)).toBe(
      false
    );
  });
});

describe('buildCanvasGraph routers', () => {
  const routerModel = () =>
    makeModel([
      makeStep({
        key: 'steps[0]',
        explicitName: 'pick',
        kind: 'router',
        action: 'router.route',
        value: {
          name: 'pick',
          action: 'router.route',
          with: {
            value: '${params.CODE}',
            routes: {
              're:^5\\d\\d$': ['server_error'],
              're:.*': 'other',
              ignored: [1, 'missing'],
            },
          },
        },
      }),
      makeStep({ key: 'steps[1]', explicitName: 'server_error' }),
      makeStep({ key: 'steps[2]', explicitName: 'other' }),
    ]);

  it('exposes one port for each route pattern', () => {
    const graph = buildCanvasGraph(inputOf(routerModel()));
    const router = nodeOf(graph, 'steps[0]');
    expect(router.routes).toEqual(['re:^5\\d\\d$', 're:.*', 'ignored']);
    expect(router.routeTargets).toEqual({
      're:^5\\d\\d$': ['server_error'],
      're:.*': ['other'],
      ignored: ['missing'],
    });
    expect(nodeOf(graph, 'steps[1]').routes).toBeUndefined();
  });

  it('adds a route edge for each resolved target', () => {
    const graph = buildCanvasGraph(inputOf(routerModel()));
    expect(graph.edges).toEqual([
      {
        id: 'route:steps[0]:re:^5\\d\\d$->steps[1]',
        source: 'steps[0]',
        target: 'steps[1]',
        kind: 'route',
        label: 're:^5\\d\\d$',
        written: 'server_error',
      },
      {
        id: 'route:steps[0]:re:.*->steps[2]',
        source: 'steps[0]',
        target: 'steps[2]',
        kind: 'route',
        label: 're:.*',
        written: 'other',
      },
    ]);
  });

  it('reads the deprecated top-level routes', () => {
    const model = makeModel([
      makeStep({
        key: 'steps[0]',
        explicitName: 'r',
        kind: 'router',
        value: { routes: { x: ['t'] } },
      }),
      makeStep({ key: 'steps[1]', explicitName: 't' }),
    ]);
    const graph = buildCanvasGraph(inputOf(model));
    expect(nodeOf(graph, 'steps[0]').routes).toEqual(['x']);
    expect(graph.edges.map((e) => e.id)).toEqual([
      'route:steps[0]:x->steps[1]',
    ]);
  });

  it('keeps a router without routes', () => {
    const model = makeModel([
      makeStep({ key: 'steps[0]', action: 'router.route', value: {} }),
    ]);
    const router = nodeOf(buildCanvasGraph(inputOf(model)), 'steps[0]');
    expect(router.routes).toEqual([]);
  });
});

describe('makeLargeGraphInput', () => {
  it('builds the same graph for each call', () => {
    const first = buildCanvasGraph(makeLargeGraphInput(120));
    const second = buildCanvasGraph(makeLargeGraphInput(120));
    expect(first).toEqual(second);
  });

  it('has several components, routers and three handlers', () => {
    const graph = buildCanvasGraph(makeLargeGraphInput(120));
    expect(graph.nodes).toHaveLength(123);
    expect(graph.nodes.filter((n) => n.lane === 'handler')).toHaveLength(3);
    expect(graph.nodes.filter((n) => n.routes).length).toBeGreaterThan(0);
    expect(graph.edges.some((e) => e.kind === 'route')).toBe(true);
    expect(graph.edges.every((e) => e.source !== e.target)).toBe(true);
  });
});

// A step run reuses the previous steps of the steps that it executes. The
// outline marks only those: not the steps after them, nor another branch.
describe('buildCanvasGraph reuse outline', () => {
  const source = (step: string): InputProvenance => ({
    step,
    source: 'run',
    runId: 'r0abcdef12',
    stale: false,
    capturedAt: null,
  });
  const base = { retryCount: 0, doneCount: 0, statusLabel: 'x' };
  const ran: StepRunState = {
    ...base,
    status: NodeStatus.Success,
  };
  const carried = (step: string): StepRunState => ({
    ...base,
    status: NodeStatus.Skipped,
    reused: source(step),
  });
  const failed: StepRunState = {
    ...base,
    status: NodeStatus.Skipped,
    reuseFailed: { runId: 'r0abcdef12' },
  };

  // a -> b -> d, a -> c -> d, and e after d, and u on another branch.
  const diamond = () => {
    const model = makeModel([
      makeStep({ key: 'steps[0]', explicitName: 'a' }),
      makeStep({ key: 'steps[1]', explicitName: 'b', depends: ['a'] }),
      makeStep({ key: 'steps[2]', explicitName: 'c', depends: ['a'] }),
      makeStep({ key: 'steps[3]', explicitName: 'd', depends: ['b', 'c'] }),
      makeStep({ key: 'steps[4]', explicitName: 'e', depends: ['d'] }),
      makeStep({ key: 'steps[5]', explicitName: 'u' }),
    ]);
    return (overlay: Record<string, StepRunState>) =>
      buildCanvasGraph({
        model,
        names: makeNames(model),
        issues: noIssues,
        overlay,
      });
  };
  const outlines = (graph: CanvasGraph) =>
    Object.fromEntries(graph.nodes.map((n) => [n.label, n.reuseOutline]));

  it('marks every previous step of the executed step of a diamond', () => {
    const graph = diamond()({
      a: carried('a'),
      b: carried('b'),
      c: failed,
      d: ran,
      e: carried('e'),
      u: carried('u'),
    });
    expect(outlines(graph)).toEqual({
      a: 'success',
      b: 'success',
      c: 'failure',
      d: undefined,
      // After the executed step, or on another branch: none, even though the
      // run carries their outputs.
      e: undefined,
      u: undefined,
    });
  });

  it('marks only the steps before the executed step of one branch', () => {
    const graph = diamond()({
      a: carried('a'),
      b: ran,
      c: carried('c'),
      d: carried('d'),
      e: failed,
      u: failed,
    });
    expect(outlines(graph)).toEqual({
      a: 'success',
      b: undefined,
      c: undefined,
      d: undefined,
      e: undefined,
      u: undefined,
    });
  });

  it('marks the steps before every executed step', () => {
    const graph = diamond()({
      a: carried('a'),
      b: ran,
      c: ran,
      d: ran,
      e: carried('e'),
      u: carried('u'),
    });
    expect(outlines(graph).a).toBe('success');
    expect(outlines(graph).e).toBeUndefined();
  });

  it('marks nothing when no step ran, or no step was reused', () => {
    expect(
      Object.values(
        outlines(diamond()({ a: carried('a'), b: carried('b') }))
      ).every((v) => v === undefined)
    ).toBe(true);
    expect(
      Object.values(outlines(diamond()({ a: ran, b: ran, d: ran }))).every(
        (v) => v === undefined
      )
    ).toBe(true);
  });

  it('follows the order of a chain DAG', () => {
    const model = makeModel(
      [
        makeStep({ key: 'steps[0]', explicitName: 'one' }),
        makeStep({ key: 'steps[1]', explicitName: 'two' }),
        makeStep({ key: 'steps[2]', explicitName: 'three' }),
      ],
      { dagType: 'chain' }
    );
    const graph = buildCanvasGraph({
      model,
      names: makeNames(model),
      issues: noIssues,
      overlay: { one: carried('one'), two: ran, three: carried('three') },
    });
    expect(outlines(graph)).toEqual({
      one: 'success',
      two: undefined,
      three: undefined,
    });
  });
});
