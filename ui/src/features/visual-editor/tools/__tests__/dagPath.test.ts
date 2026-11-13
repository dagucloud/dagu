// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { ApiStep } from '../../contracts';
import {
  backTo,
  currentDag,
  isRecursive,
  openDag,
  startPath,
  stepFacts,
} from '../dagPath';

describe('the path of the DAGs that the enlarged view opened', () => {
  it('starts at the DAG of the tool', () => {
    const path = startPath('main', ' lookup ');
    expect(path).toEqual({ host: 'main', opened: ['lookup'] });
    expect(currentDag(path)).toBe('lookup');
  });

  it('opens a called DAG one level deeper, and goes back to a level', () => {
    let path = startPath('main', 'a');
    path = openDag(path, 'b');
    path = openDag(path, 'c');
    expect(path.opened).toEqual(['a', 'b', 'c']);
    expect(currentDag(path)).toBe('c');
    expect(backTo(path, 2).opened).toEqual(['a', 'b']);
    // The tool DAG is the first level: the path never goes above it.
    expect(backTo(path, 0).opened).toEqual(['a']);
  });

  // A DAG that calls itself, or a loop A -> B -> A, is shown once: it is never
  // opened again, so the view cannot loop.
  it('refuses a call to a DAG that is open, to the DAG itself and to the host', () => {
    let path = startPath('main', 'a');
    expect(isRecursive(path, 'a')).toBe(true);
    expect(openDag(path, 'a')).toBe(path);
    path = openDag(path, 'b');
    expect(isRecursive(path, 'a')).toBe(true);
    expect(isRecursive(path, 'b')).toBe(true);
    expect(isRecursive(path, 'c')).toBe(false);
    // The DAG that holds the step is the root of the path.
    expect(isRecursive(path, 'main')).toBe(true);
    expect(openDag(path, 'main')).toBe(path);
    expect(isRecursive(path, ' a ')).toBe(true);
  });
});

describe('stepFacts', () => {
  const base = { name: 's' } as ApiStep;

  it('names the action of a step: call, router, an executor or a command', () => {
    expect(stepFacts({ ...base, call: 'child' } as ApiStep).action).toBe(
      'call'
    );
    expect(
      stepFacts({
        ...base,
        router: { value: 'x', routes: [] },
      } as ApiStep).action
    ).toBe('router');
    expect(
      stepFacts({ ...base, executorConfig: { type: 'http' } } as ApiStep).action
    ).toBe('http');
    expect(
      stepFacts({ ...base, executorConfig: { type: 'shell' } } as ApiStep)
        .action
    ).toBe('command');
    expect(stepFacts(base).action).toBe('command');
  });

  it('reads the command lines, the dependencies and the conditions', () => {
    const facts = stepFacts({
      name: 'build',
      description: ' Build it. ',
      commands: [{ command: 'make', args: ['all'] }],
      depends: ['a'],
      inferredDepends: ['b'],
      preconditions: [
        { condition: '${X}', expected: 'yes' },
        { eval: '${Y}' },
        { condition: 'test -f a' },
      ],
      parallel: { items: ['1'] },
    } as unknown as ApiStep);
    expect(facts).toMatchObject({
      name: 'build',
      description: 'Build it.',
      command: ['make all'],
      depends: ['a', 'b'],
      conditions: ['${X} = yes', '${Y}', 'test -f a'],
      parallel: true,
      call: undefined,
    });
  });

  it('keeps the first lines of a script, and leaves out the blank ones', () => {
    const facts = stepFacts({
      name: 's',
      script: 'a\n\nb\nc\nd\ne\nf',
    } as ApiStep);
    expect(facts.command).toEqual(['a', 'b', 'c', 'd']);
  });
});
