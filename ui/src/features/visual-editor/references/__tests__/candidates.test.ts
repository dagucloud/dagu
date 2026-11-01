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
      'steps',
      'params',
      'consts',
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
        'run.status',
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
    expect(labels).toHaveLength(18);
    expect(contextOf(null).map((c) => c.label)).not.toContain('step.id');
  });

  it('returns only context candidates without a scope', () => {
    const out = buildReferenceCandidates(undefined, null);
    expect(out).toHaveLength(13);
    expect(out.every((c) => c.group === 'context')).toBe(true);
    expect(buildReferenceCandidates(undefined, 'a')).toHaveLength(18);
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
