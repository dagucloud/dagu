// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  ReferenceScopeOutputSource,
  ReferenceScopeStepOutputsKnown,
} from '@/api/v1/schema';
import type { ReferenceScope } from '../../contracts';
import { scopeHttpJq } from '../../testing/fixtures';
import {
  buildReferenceCandidates,
  filterReferenceCandidates,
  foreachFramesAt,
  REFERENCE_GROUP_ORDER,
  upstreamDistances,
} from '../candidates';

const variableScope: ReferenceScope = {
  params: [
    {
      name: 'region',
      reference: '${params.region}',
      description: 'Target region',
      defaultValue: 'eu',
    },
    { name: 'tier', reference: '${params.tier}', defaultValue: 'gold' },
    { name: 'plain', reference: '${params.plain}' },
  ],
  consts: [{ name: 'LIMIT', reference: '${consts.LIMIT}', defaultValue: '10' }],
  env: [{ name: 'HOME', reference: '${env.HOME}' }],
  secrets: [
    {
      name: 'TOKEN',
      reference: '${secrets.TOKEN}',
      description: 'API token',
    },
  ],
  steps: [],
};

describe('REFERENCE_GROUP_ORDER', () => {
  it('lists the groups in display order', () => {
    expect(REFERENCE_GROUP_ORDER).toEqual([
      'foreach',
      'inputs',
      'outputs',
      'steps',
      'params',
      'consts',
      'stepEnv',
      'env',
      'secrets',
      'context',
    ]);
  });
});

describe('buildReferenceCandidates variables', () => {
  it('returns nothing without a scope or a step', () => {
    expect(
      buildReferenceCandidates(undefined, null).filter(
        (c) => c.group !== 'context'
      )
    ).toEqual([]);
  });

  const build = (scope: ReferenceScope) =>
    buildReferenceCandidates(scope, null).filter((c) => c.group !== 'context');

  it('builds one candidate per variable in group order', () => {
    const out = build(variableScope);
    expect(out.map((c) => [c.group, c.label, c.token])).toEqual([
      ['params', 'region', '${params.region}'],
      ['params', 'tier', '${params.tier}'],
      ['params', 'plain', '${params.plain}'],
      ['consts', 'LIMIT', '${consts.LIMIT}'],
      ['env', 'HOME', '${env.HOME}'],
      ['secrets', 'TOKEN', '${secrets.TOKEN}'],
    ]);
    expect(out.every((c) => c.known === 'static')).toBe(true);
  });

  it('uses the description, then the default value, as detail', () => {
    const out = build(variableScope);
    expect(out.map((c) => c.detail)).toEqual([
      'Target region',
      '= gold',
      undefined,
      '= 10',
      undefined,
      'API token',
    ]);
  });

  it('uses the server reference as token', () => {
    const scope: ReferenceScope = {
      ...variableScope,
      params: [{ name: 'x', reference: '${params.custom_x}' }],
    };
    expect(build(scope)[0]?.token).toBe('${params.custom_x}');
  });

  it('does not show a default value for a secret', () => {
    const scope: ReferenceScope = {
      ...variableScope,
      secrets: [{ name: 'S', reference: '${secrets.S}', defaultValue: 'v' }],
    };
    const secret = build(scope).find((c) => c.group === 'secrets');
    expect(secret?.detail).toBeUndefined();
  });

  it('keeps the variables of the HTTP fixture', () => {
    const out = build(scopeHttpJq);
    expect(out.map((c) => c.token)).toEqual([
      '${params.API_URL}',
      '${env.HOME}',
    ]);
    expect(out[0]?.detail).toBe('= https://api.example.com/items');
  });
});

type ScopeStep = ReferenceScope['steps'][number];

function step(
  name: string,
  upstream: string[],
  outputs: string[] = []
): ScopeStep {
  return {
    name,
    stepPath: `steps[${name}]`,
    upstream,
    outputsKnown: ReferenceScopeStepOutputsKnown.static,
    outputs: outputs.map((o) => ({
      name: o,
      reference: `\${steps.${name}.outputs.${o}}`,
      source: ReferenceScopeOutputSource.declared,
    })),
  };
}

function scopeOf(...steps: ScopeStep[]): ReferenceScope {
  return { params: [], consts: [], env: [], secrets: [], steps };
}

describe('upstreamDistances', () => {
  it('gives distance 1 to a direct parent', () => {
    const d = upstreamDistances(scopeHttpJq, 'extract');
    expect([...d]).toEqual([['fetch', 1]]);
  });

  it('is empty for a root step and for an unknown step', () => {
    expect(upstreamDistances(scopeHttpJq, 'fetch').size).toBe(0);
    expect(upstreamDistances(scopeHttpJq, 'nope').size).toBe(0);
  });

  it('reduces an all-ancestors list to direct parents', () => {
    const scope = scopeOf(
      step('a', []),
      step('b', ['a']),
      step('c', ['a', 'b'])
    );
    expect(Object.fromEntries(upstreamDistances(scope, 'c'))).toEqual({
      b: 1,
      a: 2,
    });
  });

  it('gives the same distances for both shapes of the list', () => {
    const direct = scopeOf(
      step('a', []),
      step('b', ['a']),
      step('c', ['b']),
      step('d', ['c'])
    );
    const all = scopeOf(
      step('a', []),
      step('b', ['a']),
      step('c', ['a', 'b']),
      step('d', ['a', 'b', 'c'])
    );
    const expected = { c: 1, b: 2, a: 3 };
    expect(Object.fromEntries(upstreamDistances(direct, 'd'))).toEqual(
      expected
    );
    expect(Object.fromEntries(upstreamDistances(all, 'd'))).toEqual(expected);
  });

  it('uses the shortest walk in a diamond', () => {
    const scope = scopeOf(
      step('a', []),
      step('b', ['a']),
      step('c', ['a']),
      step('d', ['b', 'c'])
    );
    expect(Object.fromEntries(upstreamDistances(scope, 'd'))).toEqual({
      b: 1,
      c: 1,
      a: 2,
    });
  });

  it('ends on a cycle', () => {
    const scope = scopeOf(
      step('a', ['c']),
      step('b', ['a']),
      step('c', ['b']),
      step('self', ['self', 'a'])
    );
    const d = upstreamDistances(scope, 'c');
    expect([...d.keys()].sort()).toEqual(['a', 'b']);
    expect(upstreamDistances(scope, 'self').has('self')).toBe(false);
    expect(upstreamDistances(scope, 'a').size).toBe(2);
  });

  it('gives a distance to every ancestor of two steps in a cycle', () => {
    const scope = scopeOf(
      step('a', ['b']),
      step('b', ['a']),
      step('c', ['a', 'b'])
    );
    expect(upstreamDistances(scope, 'c').size).toBe(2);
  });
});

describe('buildReferenceCandidates step outputs', () => {
  const stepsOf = (scope: ReferenceScope, name: string | null) =>
    buildReferenceCandidates(scope, name).filter((c) => c.group === 'steps');

  it('puts the outputs of a parent first', () => {
    const out = buildReferenceCandidates(scopeHttpJq, 'extract');
    expect(out[0]).toEqual({
      token: '${steps.fetch.outputs.body}',
      label: 'fetch.body',
      group: 'steps',
      detail: 'string \u00b7 action',
      stepName: 'fetch',
      distance: 1,
      known: 'static',
    });
    expect(out.slice(0, 2).map((c) => c.token)).toEqual([
      '${steps.fetch.outputs.body}',
      '${steps.fetch.outputs.status}',
    ]);
    expect(out.slice(2).every((c) => c.group !== 'steps')).toBe(true);
  });

  it('does not show outputs to the first step or to no step', () => {
    expect(stepsOf(scopeHttpJq, 'fetch')).toEqual([]);
    expect(stepsOf(scopeHttpJq, null)).toEqual([]);
    expect(stepsOf(scopeHttpJq, 'unknown')).toEqual([]);
  });

  it('takes tokens from the scope reference strings', () => {
    const tokens = scopeHttpJq.steps.flatMap((s) =>
      s.outputs.map((o) => o.reference)
    );
    const seen = new Set(stepsOf(scopeHttpJq, 'extract').map((c) => c.token));
    expect(seen.size).toBeGreaterThan(0);
    for (const token of seen) expect(tokens).toContain(token);
  });

  it('labels with the step id when it exists', () => {
    const scope = scopeOf(
      { ...step('first', [], ['x']), id: 'one' },
      step('second', ['first'])
    );
    const c = stepsOf(scope, 'second')[0];
    expect(c?.label).toBe('one.x');
    expect(c?.stepName).toBe('first');
  });

  it('sorts by distance, then step order, then output order', () => {
    const scope = scopeOf(
      step('a', [], ['a1']),
      step('b', ['a'], ['b1', 'b2']),
      step('x', ['a'], ['x1']),
      step('c', ['a', 'b', 'x'], ['c1'])
    );
    scope.steps.push(step('d', ['a', 'b', 'x', 'c']));
    expect(stepsOf(scope, 'd').map((c) => [c.label, c.distance])).toEqual([
      ['c.c1', 1],
      ['b.b1', 2],
      ['b.b2', 2],
      ['x.x1', 2],
      ['a.a1', 3],
    ]);
  });

  it('keeps the runtime flag and omits missing detail parts', () => {
    const base = step('p', [], ['o']);
    const scope = scopeOf(
      {
        ...base,
        outputsKnown: ReferenceScopeStepOutputsKnown.runtime,
        outputs: [
          {
            name: 'o',
            reference: '${RESULT}',
            source: ReferenceScopeOutputSource.outputVariable,
          },
        ],
      },
      step('q', ['p'])
    );
    expect(stepsOf(scope, 'q')[0]).toMatchObject({
      token: '${RESULT}',
      detail: 'outputVariable',
      known: 'runtime',
    });
  });
});

describe('buildReferenceCandidates context', () => {
  const contextOf = (stepName: string | null) =>
    buildReferenceCandidates(scopeHttpJq, stepName).filter(
      (c) => c.group === 'context'
    );

  it('builds the fixed context tokens', () => {
    const out = contextOf(null);
    expect(out.map((c) => c.token)).toEqual(
      [
        'dag.name',
        'run.id',
        'run.scheduled_at',
        'run.root_name',
        'run.root_id',
        'attempt.id',
        'attempt.started_at',
        'trigger.type',
        'trigger.actor',
        'paths.log_file',
        'paths.work_dir',
        'paths.artifacts_dir',
      ].map((field) => `\${context.${field}}`)
    );
    expect(out[1]).toEqual({
      token: '${context.run.id}',
      label: 'run.id',
      group: 'context',
      known: 'runtime',
    });
  });

  it('adds the current step fields when a step owns the field', () => {
    const labels = contextOf('fetch').map((c) => c.label);
    expect(labels.slice(-5)).toEqual([
      'step.id',
      'step.name',
      'paths.step_stdout_file',
      'paths.step_stderr_file',
      'paths.step_output_file',
    ]);
    expect(labels).toHaveLength(17);
    expect(contextOf(null).map((c) => c.label)).not.toContain('step.id');
  });

  describe('fields that spec 017 limits to a scope', () => {
    const base = scopeHttpJq.steps[0]!;
    const scope: ReferenceScope = {
      ...scopeHttpJq,
      steps: [
        ...scopeHttpJq.steps,
        { ...base, name: 'no_id', id: undefined, stepPath: 'steps[2]' },
        {
          ...base,
          name: 'on_failure',
          id: undefined,
          stepPath: 'handler_on.failure',
        },
      ],
    };
    const labelsOf = (stepName: string | null, repeats = false) =>
      buildReferenceCandidates(scope, stepName, { repeats }).map(
        (c) => c.label
      );

    it('offers the run status in a handler only', () => {
      expect(labelsOf('on_failure')).toContain('run.status');
      expect(labelsOf('fetch')).not.toContain('run.status');
      expect(labelsOf(null)).not.toContain('run.status');
    });

    it('offers the repeat index for a repeating step only', () => {
      expect(labelsOf('fetch', true)).toContain('step.repeat_index');
      expect(labelsOf('fetch')).not.toContain('step.repeat_index');
      expect(labelsOf('on_failure', true)).not.toContain('step.repeat_index');
    });

    it('offers the step id for a step with an id only', () => {
      expect(labelsOf('fetch')).toContain('step.id');
      expect(labelsOf('no_id')).not.toContain('step.id');
      expect(labelsOf('on_failure')).not.toContain('step.id');
    });
  });

  it('returns only context candidates without a scope', () => {
    const out = buildReferenceCandidates(undefined, null);
    expect(out).toHaveLength(12);
    expect(out.every((c) => c.group === 'context')).toBe(true);
    expect(buildReferenceCandidates(undefined, 'a')).toHaveLength(17);
  });

  it('follows the group order', () => {
    const out = buildReferenceCandidates(
      {
        ...variableScope,
        steps: scopeHttpJq.steps,
      },
      'extract'
    );
    const rank = out.map((c) => REFERENCE_GROUP_ORDER.indexOf(c.group));
    expect(rank).toEqual([...rank].sort((a, b) => a - b));
    expect(new Set(out.map((c) => c.group)).size).toBe(6);
  });
});

function scopeStep(name: string, patch: Partial<ScopeStep> = {}): ScopeStep {
  return {
    name,
    id: name,
    stepPath: `steps[0]`,
    upstream: [],
    outputs: [],
    outputsKnown: ReferenceScopeStepOutputsKnown.static,
    ...patch,
  };
}

function withOutput(name: string, patch: Partial<ScopeStep> = {}): ScopeStep {
  return scopeStep(name, {
    outputs: [
      {
        name: 'out',
        reference: `\${steps.${name}.outputs.out}`,
        source: ReferenceScopeOutputSource.declared,
      },
    ],
    ...patch,
  });
}

const variable = (reference: string) => ({
  name: reference.slice(2, -1).split('.').slice(1).join('.'),
  reference,
});

function emptyScope(steps: ScopeStep[]): ReferenceScope {
  return { params: [], consts: [], env: [], secrets: [], steps };
}

describe('buildReferenceCandidates variables of the owner step', () => {
  const tokensOf = (scope: ReferenceScope, name: string, group: string) =>
    buildReferenceCandidates(scope, name)
      .filter((c) => c.group === group)
      .map((c) => c.token);

  it('lists the env of the owner step only', () => {
    const scope = emptyScope([
      scopeStep('a', { env: [variable('${env.TARGET}')] }),
      scopeStep('b'),
    ]);
    expect(tokensOf(scope, 'a', 'stepEnv')).toEqual(['${env.TARGET}']);
    expect(tokensOf(scope, 'b', 'stepEnv')).toEqual([]);
    expect(
      buildReferenceCandidates(scope, null).filter(
        (c) => c.group === 'stepEnv'
      )
    ).toEqual([]);
  });

  it('lists a name once when the root env has it too', () => {
    const scope = {
      ...emptyScope([scopeStep('a', { env: [variable('${env.PATH}')] })]),
      env: [variable('${env.PATH}'), variable('${env.HOME}')],
    };
    const tokens = buildReferenceCandidates(scope, 'a').map((c) => c.token);
    expect(tokens.filter((t) => t === '${env.PATH}')).toHaveLength(1);
    expect(tokensOf(scope, 'a', 'stepEnv')).toEqual(['${env.PATH}']);
    expect(tokensOf(scope, 'a', 'env')).toEqual(['${env.HOME}']);
  });

  it('lists the build paths of the owner step', () => {
    const scope = emptyScope([
      scopeStep('compile', {
        inputs: [variable('${inputs.source}')],
        pathOutputs: [variable('${outputs.artifact}')],
      }),
      scopeStep('other'),
    ]);
    expect(tokensOf(scope, 'compile', 'inputs')).toEqual(['${inputs.source}']);
    expect(tokensOf(scope, 'compile', 'outputs')).toEqual([
      '${outputs.artifact}',
    ]);
    expect(tokensOf(scope, 'other', 'inputs')).toEqual([]);
  });

  // A repeat policy can come from `defaults`, which the step entry does not show.
  it('offers the repeat index for a step that the scope says repeats', () => {
    const scope = emptyScope([
      scopeStep('poll', { repeats: true }),
      scopeStep('once'),
    ]);
    const labels = (name: string) =>
      buildReferenceCandidates(scope, name).map((c) => c.label);
    expect(labels('poll')).toContain('step.repeat_index');
    expect(labels('once')).not.toContain('step.repeat_index');
  });
});

describe('buildReferenceCandidates in a foreach body', () => {
  const item = ['item', 'item.url', 'index', 'key'].map((name) =>
    variable(`\${foreach.${name}}`)
  );
  const leaf = withOutput('leaf', {
    stepPath: 'steps[1].foreach.steps[2].foreach.steps[0]',
  });
  const inner = scopeStep('inner', {
    stepPath: 'steps[1].foreach.steps[2]',
    upstream: ['first'],
    foreach: {
      item: [variable('${foreach.row}'), variable('${foreach.index}')],
      steps: [leaf],
    },
  });
  const first = withOutput('first', { stepPath: 'steps[1].foreach.steps[0]' });
  const second = withOutput('second', {
    stepPath: 'steps[1].foreach.steps[1]',
    upstream: ['first'],
    env: [variable('${env.LOCAL}')],
    repeats: true,
  });
  const each = scopeStep('each', {
    stepPath: 'steps[1]',
    upstream: ['before'],
    foreach: { item, steps: [first, second, inner] },
  });
  const scope = emptyScope([
    withOutput('before'),
    each,
    withOutput('after', { stepPath: 'steps[2]', upstream: ['each'] }),
  ]);

  const tokens = (path: string[], group: string) =>
    buildReferenceCandidates(scope, 'each', {
      frames: foreachFramesAt(each, path),
    })
      .filter((c) => c.group === group)
      .map((c) => c.token);

  it('offers the item and the earlier body steps, nearest first', () => {
    const path = ['foreach', 'steps', '1', 'run'];
    expect(tokens(path, 'foreach')).toEqual([
      '${foreach.item}',
      '${foreach.item.url}',
      '${foreach.index}',
      '${foreach.key}',
    ]);
    expect(tokens(path, 'steps')).toEqual([
      '${steps.first.outputs.out}',
      '${steps.before.outputs.out}',
    ]);
  });

  it('offers no later body step and no step of its own', () => {
    expect(tokens(['foreach', 'steps', '0', 'run'], 'steps')).toEqual([
      '${steps.before.outputs.out}',
    ]);
  });

  it('marks the item as known after the run', () => {
    const [value] = buildReferenceCandidates(scope, 'each', {
      frames: foreachFramesAt(each, ['foreach', 'steps', '0', 'run']),
    });
    expect(value).toMatchObject({ group: 'foreach', known: 'runtime' });
  });

  it('reads the env and repeat policy of the body step', () => {
    const at = (index: string) =>
      buildReferenceCandidates(scope, 'each', {
        frames: foreachFramesAt(each, ['foreach', 'steps', index, 'run']),
      });
    expect(
      at('1').filter((c) => c.group === 'stepEnv').map((c) => c.token)
    ).toEqual(['${env.LOCAL}']);
    expect(at('1').map((c) => c.label)).toContain('step.repeat_index');
    expect(at('0').map((c) => c.label)).not.toContain('step.repeat_index');
    expect(at('0').map((c) => c.token)).not.toContain('${env.LOCAL}');
  });

  // Spec 018 gives `collect` the step outputs of the whole body.
  it('offers every body step in collect', () => {
    const path = ['foreach', 'collect', 'name'];
    expect(tokens(path, 'foreach')).toContain('${foreach.item}');
    expect(tokens(path, 'steps')).toEqual([
      '${steps.first.outputs.out}',
      '${steps.second.outputs.out}',
      '${steps.before.outputs.out}',
    ]);
  });

  // The key is what is being computed: it reads as empty in its own expression.
  it('offers the item but not the key or body steps in foreach.key', () => {
    const path = ['foreach', 'key'];
    expect(tokens(path, 'foreach')).toEqual([
      '${foreach.item}',
      '${foreach.item.url}',
      '${foreach.index}',
    ]);
    expect(tokens(path, 'steps')).toEqual(['${steps.before.outputs.out}']);
  });

  it('gives items no item scope, as they resolve before any item', () => {
    expect(tokens(['foreach', 'items'], 'foreach')).toEqual([]);
  });

  // A run showed that an inner body reads outer step outputs, while an outer
  // item is unresolved there.
  it('gives a nested body its own item and the steps around it', () => {
    const path = ['foreach', 'steps', '2', 'foreach', 'steps', '0', 'run'];
    expect(tokens(path, 'foreach')).toEqual([
      '${foreach.row}',
      '${foreach.index}',
    ]);
    expect(tokens(path, 'steps')).toEqual([
      '${steps.first.outputs.out}',
      '${steps.before.outputs.out}',
    ]);
  });

  it('keeps body steps away from the steps outside the foreach', () => {
    const all = buildReferenceCandidates(scope, 'after').map((c) => c.token);
    expect(all).not.toContain('${steps.first.outputs.out}');
    expect(all).not.toContain('${foreach.item}');
  });

  it('still offers the item for a body step the scope does not know yet', () => {
    expect(tokens(['foreach', 'steps', '9', 'run'], 'foreach')).toContain(
      '${foreach.item}'
    );
  });
});

describe('filterReferenceCandidates', () => {
  const all = buildReferenceCandidates(scopeHttpJq, 'extract');

  it('returns every candidate for an empty query', () => {
    expect(filterReferenceCandidates(all, '')).toEqual(all);
    expect(filterReferenceCandidates(all, '   ')).toEqual(all);
  });

  it('matches case-insensitively on the label', () => {
    const out = filterReferenceCandidates(all, 'FETCH.bo');
    expect(out.map((c) => c.token)).toEqual(['${steps.fetch.outputs.body}']);
  });

  it('matches on the token and on the detail', () => {
    expect(
      filterReferenceCandidates(all, 'params.api').map((c) => c.label)
    ).toEqual(['API_URL']);
    expect(
      filterReferenceCandidates(all, 'integer').map((c) => c.label)
    ).toEqual(['fetch.status']);
  });

  it('needs every word to match, in any order and field', () => {
    const out = filterReferenceCandidates(all, 'string fetch');
    expect(out.map((c) => c.label)).toEqual(['fetch.body']);
    expect(filterReferenceCandidates(all, 'fetch http')).toEqual([]);
    expect(
      filterReferenceCandidates(all, 'run  id').map((c) => c.label)
    ).toEqual(['run.id', 'run.root_id']);
  });

  it('keeps the input order', () => {
    const out = filterReferenceCandidates(all, 'a');
    expect(out).toEqual(all.filter((c) => out.includes(c)));
  });
});
