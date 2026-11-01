// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { hashText, stepHashes } from '../stepHashes';

const SPEC = `type: graph
steps:
  - id: slow
    name: slow
    run: echo slow
  - id: middle
    name: middle
    depends: [slow]
    run: echo "middle \${steps.slow.outputs.data}"
  - id: use
    name: use
    depends: [middle]
    run: echo use
  - id: other
    name: other
    run: echo other
`;

describe('hashText', () => {
  it('gives equal text the same hash and other text another', () => {
    expect(hashText('a')).toBe(hashText('a'));
    expect(hashText('a')).not.toBe(hashText('b'));
  });
});

describe('stepHashes', () => {
  it('hashes each named step', () => {
    const hashes = stepHashes(SPEC);
    expect([...hashes.keys()]).toEqual(['slow', 'middle', 'use', 'other']);
    expect(new Set(hashes.values()).size).toBe(4);
  });

  it('does not change with a comment or spacing', () => {
    const noisy = SPEC.replace(
      'run: echo slow',
      'run:   echo slow   # the slow step'
    );
    expect(stepHashes(noisy)).toEqual(stepHashes(SPEC));
  });

  it('changes for the step that changed and for the steps that need it', () => {
    const before = stepHashes(SPEC);
    const after = stepHashes(SPEC.replace('echo slow', 'echo slower'));
    expect(after.get('slow')).not.toBe(before.get('slow'));
    // middle depends on slow, and use depends on middle.
    expect(after.get('middle')).not.toBe(before.get('middle'));
    expect(after.get('use')).not.toBe(before.get('use'));
    expect(after.get('other')).toBe(before.get('other'));
  });

  it('keeps the steps before an edited step the same', () => {
    const before = stepHashes(SPEC);
    const after = stepHashes(SPEC.replace('echo use', 'echo use2'));
    expect(after.get('use')).not.toBe(before.get('use'));
    expect(after.get('slow')).toBe(before.get('slow'));
    expect(after.get('middle')).toBe(before.get('middle'));
  });

  it('follows a reference to an output when the step has no depends', () => {
    const spec = `steps:
  - id: a
    name: a
    run: echo a
  - id: b
    name: b
    run: echo \${steps.a.outputs.x}
`;
    const before = stepHashes(spec);
    const after = stepHashes(spec.replace('echo a', 'echo A'));
    expect(after.get('b')).not.toBe(before.get('b'));
  });

  it('follows the step before it in a chain', () => {
    const spec = `type: chain
steps:
  - name: a
    run: echo a
  - name: b
    run: echo b
`;
    const before = stepHashes(spec);
    const after = stepHashes(spec.replace('echo a', 'echo A'));
    expect(after.get('b')).not.toBe(before.get('b'));
  });

  // A chain step with no name or id is still the step before the next one.
  it('follows a step with no name in a chain', () => {
    const spec = `type: chain
steps:
  - name: a
    run: echo a
  - run: echo middle
  - name: c
    run: echo c
`;
    const before = stepHashes(spec);
    const after = stepHashes(spec.replace('echo middle', 'echo MIDDLE'));
    expect([...before.keys()]).toEqual(['a', 'c']);
    expect(after.get('a')).toBe(before.get('a'));
    expect(after.get('c')).not.toBe(before.get('c'));
  });

  // The server makes each route target depend on its router.
  it('follows the router of a route target', () => {
    const spec = `steps:
  - name: route
    action: router.route
    with:
      value: \${CODE}
      routes:
        ok: [handle]
  - name: handle
    run: echo handle
`;
    const before = stepHashes(spec);
    const after = stepHashes(spec.replace('ok: [handle]', 'done: [handle]'));
    expect(after.get('handle')).not.toBe(before.get('handle'));
  });

  // The steps of a parallel group follow the entry before the group, not
  // each other, and the next step follows every step of the group.
  it('follows the entry before a parallel group in a chain', () => {
    const spec = `type: chain
steps:
  - name: a
    run: echo a
  - name: b
    run: echo b
  - name: c
    run: echo c
`;
    const grouped = `type: chain
steps:
  - - name: a
      run: echo a
    - name: b
      run: echo b
  - name: c
    run: echo c
`;
    const before = stepHashes(spec);
    expect(stepHashes(grouped).get('b')).not.toBe(before.get('b'));
    const edited = stepHashes(grouped.replace('echo a', 'echo A'));
    expect(edited.get('b')).toBe(stepHashes(grouped).get('b'));
    expect(edited.get('c')).not.toBe(stepHashes(grouped).get('c'));
  });

  // A step can run a sub-DAG that a later document defines.
  it('changes every step when a later document changes', () => {
    const child = (run: string) =>
      `${SPEC}---\nname: child\nsteps:\n  - name: work\n    run: ${run}\n`;
    const before = stepHashes(child('echo a'));
    const after = stepHashes(child('echo b'));
    for (const name of before.keys()) {
      expect(after.get(name)).not.toBe(before.get(name));
    }
  });

  it('gives one result to the calls with the same text', () => {
    expect(stepHashes(SPEC)).toBe(stepHashes(SPEC));
  });

  it('changes every step when a top-level key changes', () => {
    const before = stepHashes(SPEC);
    const after = stepHashes(`env:\n  - X: 1\n${SPEC}`);
    for (const name of before.keys()) {
      expect(after.get(name)).not.toBe(before.get(name));
    }
  });

  it('gives no hash for a text that cannot be read', () => {
    expect(stepHashes('steps: [').size).toBe(0);
  });
});
