// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { parse } from 'yaml';
import { ReferenceScopeStepOutputsKnown } from '@/api/v1/schema';
import type { ReferenceScope } from '../../contracts';
import { parseDagDoc } from '../../doc/parse';
import { buildReferenceCandidates } from '../candidates';
import { publishEdit } from '../publishOutput';

type ScopeStep = ReferenceScope['steps'][number];

const scopeStep = (name: string, index: number, id?: string): ScopeStep => ({
  name,
  id,
  stepPath: `steps[${index}]`,
  upstream: index > 0 ? ['fetch'] : [],
  outputs: [],
  outputsKnown: ReferenceScopeStepOutputsKnown.static,
});

const scopeOf = (...steps: ScopeStep[]): ReferenceScope => ({
  params: [],
  consts: [],
  env: [],
  secrets: [],
  steps,
});

const doc = (first: string) =>
  `steps:\n  - id: fetch\n${first}\n  - id: use\n    depends: fetch\n    run: echo\n`;

const candidatesOf = (text: string) =>
  buildReferenceCandidates(
    scopeOf(scopeStep('fetch', 0, 'fetch'), scopeStep('use', 1, 'use')),
    'use',
    { doc: parseDagDoc(text) }
  ).filter((c) => c.group === 'steps');

describe('publishable outputs', () => {
  it('offers the stdout of an upstream run step that publishes nothing', () => {
    expect(candidatesOf(doc('    run: echo hi'))).toEqual([
      {
        token: '${steps.fetch.outputs.stdout}',
        label: 'fetch.stdout',
        group: 'steps',
        stepName: 'fetch',
        distance: 1,
        known: 'runtime',
        publish: { step: 'steps[0]' },
      },
    ]);
  });

  it.each([
    ['output', '    run: a\n    output: VALUE'],
    ['outputs', '    run: a\n    outputs:\n      - name: x'],
    ['output_schema', '    run: a\n    output_schema: {type: object}'],
    ['stdout', '    run: a\n    stdout: out.txt'],
  ])('offers nothing for a step that has %s', (_field, lines) => {
    expect(candidatesOf(doc(lines))).toEqual([]);
  });

  it.each([
    ['an action', '    action: http.request\n    with: {url: x}'],
    [
      'a foreach',
      '    foreach:\n      items: [a]\n      steps:\n        - run: a',
    ],
  ])('offers nothing for %s', (_kind, lines) => {
    expect(candidatesOf(doc(lines))).toEqual([]);
  });

  // Spec 033: a scalar output makes a build step lose its reuse.
  it('offers nothing in a build DAG', () => {
    expect(candidatesOf(`type: build\n${doc('    run: echo hi')}`)).toEqual([]);
  });

  it('offers nothing without the document', () => {
    const scope = scopeOf(scopeStep('fetch', 0, 'fetch'), scopeStep('use', 1));
    expect(
      buildReferenceCandidates(scope, 'use').filter((c) => c.group === 'steps')
    ).toEqual([]);
  });

  it('offers nothing for a step that is not upstream', () => {
    const text = doc('    run: echo hi');
    const scope = scopeOf(scopeStep('fetch', 0, 'fetch'), scopeStep('use', 1));
    expect(
      buildReferenceCandidates(scope, 'fetch', { doc: parseDagDoc(text) })
    ).not.toContainEqual(expect.objectContaining({ group: 'steps' }));
  });

  describe('for a step without an id', () => {
    const text = (name: string) =>
      `steps:\n  - name: ${name}\n    run: echo\n  - name: use\n    run: echo\n`;
    const offered = (name: string) =>
      buildReferenceCandidates(
        scopeOf(scopeStep(name, 0), {
          ...scopeStep('use', 1),
          upstream: [name],
        }),
        'use',
        { doc: parseDagDoc(text(name)) }
      ).filter((c) => c.group === 'steps');

    it('gives the step its name as an id', () => {
      expect(offered('fetch')[0]).toMatchObject({
        token: '${steps.fetch.outputs.stdout}',
        publish: { step: 'steps[0]', id: 'fetch' },
      });
    });

    it('offers nothing when no reference can use the name', () => {
      expect(offered('two words')).toEqual([]);
    });
  });
});

describe('publishEdit', () => {
  const text = doc('    run: echo hi');

  it('makes the step publish its stdout', () => {
    const result = publishEdit(text, { step: 'steps[0]' });
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(parse(result.text).steps[0]).toEqual({
      id: 'fetch',
      run: 'echo hi',
      output: { stdout: { from: 'stdout' } },
    });
    expect(parse(result.text).steps[1]).toEqual(parse(text).steps[1]);
  });

  it('gives one edit, inside the step, that makes the same text', () => {
    const result = publishEdit(text, { step: 'steps[0]' });
    if (!result.ok) throw new Error(result.message);
    expect(result.edits).toHaveLength(1);
    const [edit] = result.edits;
    expect(
      text.slice(0, edit!.from) + edit!.insert + text.slice(edit!.to)
    ).toBe(result.text);
    // It can share the indent of the next step, and stops before its text.
    expect(edit!.to).toBeLessThanOrEqual(text.indexOf('- id: use'));
  });

  it('sets the id of a step that has none', () => {
    const noId = 'steps:\n  - name: fetch\n    run: echo\n';
    const result = publishEdit(noId, { step: 'steps[0]', id: 'fetch' });
    if (!result.ok) throw new Error(result.message);
    expect(parse(result.text).steps[0]).toMatchObject({
      id: 'fetch',
      output: { stdout: { from: 'stdout' } },
    });
  });

  it('reports an edit that cannot apply', () => {
    expect(publishEdit(text, { step: 'steps[9]' }).ok).toBe(false);
  });
});
