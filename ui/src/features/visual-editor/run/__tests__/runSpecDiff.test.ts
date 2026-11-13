// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { stepNeeds, staleUpstream } from '../reuseSource';
import { yamlUnindentedQuote } from '../../testing/fixtures';
import { diffRunSpec, normalizeSpec, specTextDiffers } from '../runSpecDiff';

const BASE = `name: demo
env:
  - A=1
steps:
  - name: fetch
    run: curl example.com
  - name: parse
    run: jq . file
    depends: [fetch]
  - name: store
    run: cp out dest
    depends: [parse]
`;

describe('diffRunSpec', () => {
  it('finds no difference in equal texts', () => {
    const diff = diffRunSpec(BASE, BASE);
    expect(diff.headChanged).toBe(false);
    expect([...diff.changed]).toEqual([]);
    expect([...diff.removedFromLive]).toEqual([]);
    expect([...diff.addedInLive]).toEqual([]);
  });

  it('ignores comments, indentation and key order', () => {
    const live = `# a comment
name: demo
env: [A=1]
steps:
    - name: fetch   # fetch the page
      run: curl example.com

    - run: jq . file
      name: parse
      depends: [fetch]
    - name: store
      run: cp out dest
      depends:
        - parse
`;
    const diff = diffRunSpec(BASE, live);
    expect(diff.headChanged).toBe(false);
    expect([...diff.changed]).toEqual([]);
  });

  it('marks a step whose run changed', () => {
    const diff = diffRunSpec(BASE, BASE.replace('jq . file', 'jq .items file'));
    expect([...diff.changed]).toEqual(['parse']);
    expect(diff.headChanged).toBe(false);
  });

  it('marks a step whose depends changed', () => {
    const diff = diffRunSpec(
      BASE,
      BASE.replace('depends: [parse]', 'depends: [fetch]')
    );
    expect([...diff.changed]).toEqual(['store']);
  });

  it('marks a change of the head', () => {
    expect(diffRunSpec(BASE, BASE.replace('A=1', 'A=2')).headChanged).toBe(
      true
    );
    expect(
      diffRunSpec(BASE, BASE.replace('name: demo', 'name: other')).headChanged
    ).toBe(true);
  });

  // A step can call a local sub-DAG of a later document.
  it('counts a later document as the head', () => {
    const withChild = (run: string) =>
      `${BASE}---\nname: child\nsteps:\n  - name: work\n    run: ${run}\n`;
    const child = withChild('echo a');
    expect(diffRunSpec(child, child).headChanged).toBe(false);
    expect(diffRunSpec(child, withChild('echo b')).headChanged).toBe(true);
    expect(diffRunSpec(BASE, child).headChanged).toBe(true);
    expect(diffRunSpec(child, `${BASE}---\nname: [\n`).headChanged).toBe(true);
  });

  it('does not count a handler as the head', () => {
    const withHandler = `${BASE}handler_on:
  failure:
    run: echo failed
`;
    const changed = withHandler.replace('echo failed', 'echo broken');
    const diff = diffRunSpec(withHandler, changed);
    expect(diff.headChanged).toBe(false);
    expect([...diff.changed]).toEqual([]);
  });

  it('lists added and removed steps', () => {
    const live = BASE.replace('name: store', 'name: save');
    const diff = diffRunSpec(BASE, live);
    expect([...diff.removedFromLive]).toEqual(['store']);
    expect([...diff.addedInLive]).toEqual(['save']);
  });

  it('matches a shorthand step with its explicit form', () => {
    const before = 'steps:\n  - name: a\n    run: echo x\n';
    const after = 'steps:\n  - {name: a, run: echo x}\n';
    expect([...diffRunSpec(before, after).changed]).toEqual([]);
  });

  // The server reads a quoted string whatever the indent of its later lines.
  it('reads texts with a quoted string with unindented lines', () => {
    const diff = diffRunSpec(
      yamlUnindentedQuote,
      `# Owner and notes\n${yamlUnindentedQuote}`
    );
    expect(diff.headChanged).toBe(false);
    expect([...diff.changed]).toEqual([]);
  });

  it('gives headChanged for a text that cannot be read', () => {
    expect(diffRunSpec(BASE, 'name: [\n').headChanged).toBe(true);
    expect(diffRunSpec('name: [\n', BASE).headChanged).toBe(true);
  });

  describe('with a sub-DAG after ---', () => {
    const multi = (command: string) => `steps:
  - name: child
    call: transform
  - name: use
    depends: [child]
    run: echo "\${steps.child.outputs.x}"
  - name: other
    run: echo free
---
name: transform
steps:
  - name: t
    run: ${command}
`;

    it('marks the head and the steps downstream when a sub-DAG changes', () => {
      const diff = diffRunSpec(multi('echo a'), multi('echo b'));
      expect(diff.headChanged).toBe(true);
      const stale = staleUpstream({
        diff,
        previous: ['child', 'use', 'other'],
        deps: stepNeeds([
          { name: 'child' },
          { name: 'use', depends: ['child'] },
          { name: 'other' },
        ]),
      });
      expect(stale).toEqual(expect.arrayContaining(['child', 'use']));
    });

    it('finds no difference in an unchanged text', () => {
      const diff = diffRunSpec(multi('echo a'), multi('echo a'));
      expect(diff.headChanged).toBe(false);
      expect([...diff.changed]).toEqual([]);
    });

    it('ignores comments and key order in the sub-DAG', () => {
      const live = multi('echo a').replace(
        'name: transform\nsteps:\n  - name: t\n    run: echo a\n',
        '# note\nsteps:\n  - run: echo a\n    name: t\nname: transform\n'
      );
      expect(live).not.toBe(multi('echo a'));
      expect(diffRunSpec(multi('echo a'), live).headChanged).toBe(false);
    });

    it('marks the head when a sub-DAG is added or removed', () => {
      const single = multi('echo a').split('---')[0] ?? '';
      expect(diffRunSpec(multi('echo a'), single).headChanged).toBe(true);
      expect(diffRunSpec(single, multi('echo a')).headChanged).toBe(true);
    });

    it('marks the head when a later document cannot be read', () => {
      const broken = `${multi('echo a').split('---')[0]}---\nsteps: [`;
      expect(diffRunSpec(multi('echo a'), broken).headChanged).toBe(true);
    });
  });

  // A chain step depends on the steps of the position before it.
  describe('a chain DAG', () => {
    const chain = (...items: string[]) =>
      `type: chain\nsteps:\n${items.map((item) => `  - ${item}\n`).join('')}`;

    it('marks the steps whose place in the chain moved', () => {
      const diff = diffRunSpec(
        chain(
          '{name: a, run: echo a}',
          '{name: b, run: echo b}',
          '{name: c, run: echo c}'
        ),
        chain(
          '{name: b, run: echo b}',
          '{name: a, run: echo a}',
          '{name: c, run: echo c}'
        )
      );
      expect([...diff.changed].sort()).toEqual(['a', 'b', 'c']);
    });

    it('marks the steps around a parallel group that changed', () => {
      const before = chain(
        '{name: a, run: echo a}',
        '[{name: b, run: echo b}, {name: c, run: echo c}]',
        '{name: d, run: echo d}'
      );
      const swapped = chain(
        '{name: a, run: echo a}',
        '[{name: c, run: echo c}, {name: b, run: echo b}]',
        '{name: d, run: echo d}'
      );
      expect([...diffRunSpec(before, swapped).changed]).toEqual([]);

      const flat = chain(
        '{name: a, run: echo a}',
        '{name: b, run: echo b}',
        '{name: c, run: echo c}',
        '{name: d, run: echo d}'
      );
      expect([...diffRunSpec(before, flat).changed].sort()).toEqual(['c', 'd']);
    });

    it('does not count the order of a graph DAG', () => {
      const graph = (first: string, second: string) =>
        `steps:\n  - ${first}\n  - ${second}\n`;
      const diff = diffRunSpec(
        graph('{name: a, run: echo a}', '{name: b, run: echo b}'),
        graph('{name: b, run: echo b}', '{name: a, run: echo a}')
      );
      expect([...diff.changed]).toEqual([]);
    });
  });

  it('names an unnamed step with the names of the server', () => {
    const before = 'steps:\n  - run: echo one\n  - run: echo two\n';
    const after = 'steps:\n  - run: echo one\n  - run: echo 2\n';
    const details = {
      name: 'demo',
      steps: [{ name: 'cmd_1' }, { name: 'cmd_2' }],
    } as never;
    const diff = diffRunSpec(before, after, {
      snapshot: details,
      live: details,
    });
    expect([...diff.changed]).toEqual(['cmd_2']);
  });
});

describe('normalizeSpec', () => {
  it('removes carriage returns and trailing blanks', () => {
    expect(normalizeSpec('a\r\nb\n\n')).toBe('a\nb');
    expect(specTextDiffers('a\r\nb\n', 'a\nb')).toBe(false);
    expect(specTextDiffers('a', 'b')).toBe(true);
  });
});
