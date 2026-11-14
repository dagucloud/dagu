// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  previousOfExecuted,
  stepsWithUpstream,
  upstreamStepNames,
} from '../upstreamSteps';

const steps = [
  { name: 'ship', depends: ['test', 'lint'] },
  { name: 'test', depends: ['build'] },
  { name: 'build' },
  { name: 'lint' },
  { name: 'unrelated' },
];

describe('upstreamStepNames', () => {
  it('lists the steps that a step needs, in the order of the file', () => {
    expect(upstreamStepNames(steps, 'ship')).toEqual(['test', 'build', 'lint']);
  });

  it('puts a step after the steps that it needs, in dependency order', () => {
    expect(upstreamStepNames(steps, 'ship', 'dependency')).toEqual([
      'build',
      'test',
      'lint',
    ]);
  });

  it('gives nothing for a step without upstream, an unknown step or no steps', () => {
    expect(upstreamStepNames(steps, 'build')).toEqual([]);
    expect(upstreamStepNames(steps, 'missing')).toEqual([]);
    expect(upstreamStepNames(undefined, 'ship')).toEqual([]);
  });

  it('ends on a cycle and ignores a dependency that is not a step', () => {
    const cyclic = [
      { name: 'a', depends: ['b', 'ghost'] },
      { name: 'b', depends: ['a'] },
    ];
    expect(upstreamStepNames(cyclic, 'a', 'dependency')).toEqual(['b']);
  });
});

describe('upstream steps that a step reads', () => {
  it('counts an inferred dependency as an upstream step', () => {
    const list = [
      { name: 'a' },
      { name: 'b', inferredDepends: ['a'] },
      { name: 'c', depends: ['b'] },
    ];
    expect(upstreamStepNames(list, 'c', 'dependency')).toEqual(['a', 'b']);
  });
});

describe('stepsWithUpstream', () => {
  it('lists the steps and the steps that they need, farthest first', () => {
    expect(stepsWithUpstream(steps, ['ship'], 'x')).toEqual([
      'build',
      'test',
      'lint',
      'ship',
    ]);
    // Between steps that do not depend on each other, the file decides.
    expect(stepsWithUpstream(steps, ['lint', 'test'], 'ship')).toEqual([
      'build',
      'test',
      'lint',
    ]);
  });

  it('leaves out the excluded step and the names that do not exist', () => {
    expect(stepsWithUpstream(steps, ['test', 'ship', 'ghost'], 'ship')).toEqual(
      ['build', 'test']
    );
    expect(stepsWithUpstream(null, ['a'], 'b')).toEqual([]);
  });
});

describe('previousOfExecuted', () => {
  // a -> b -> d, a -> c -> d, e after d, u alone.
  const diamond = [
    { name: 'a' },
    { name: 'b', depends: ['a'] },
    { name: 'c', depends: ['a'] },
    { name: 'd', depends: ['b', 'c'] },
    { name: 'e', depends: ['d'] },
    { name: 'u' },
  ];

  it('lists the steps before the executed step, transitively', () => {
    expect([...previousOfExecuted(diamond, ['d'])].sort()).toEqual([
      'a',
      'b',
      'c',
    ]);
  });

  it('leaves out the steps after the executed step and the other branches', () => {
    expect([...previousOfExecuted(diamond, ['b'])]).toEqual(['a']);
    expect(previousOfExecuted(diamond, ['a']).size).toBe(0);
  });

  it('leaves out an executed step, and reads inferred needs', () => {
    const steps = [
      { name: 'a' },
      { name: 'b', inferredDepends: ['a'] },
      { name: 'c', depends: ['b'] },
    ];
    expect([...previousOfExecuted(steps, ['b', 'c'])]).toEqual(['a']);
  });

  it('gives nothing without steps or without an executed step', () => {
    expect(previousOfExecuted(undefined, ['a']).size).toBe(0);
    expect(previousOfExecuted(diamond, []).size).toBe(0);
  });
});
