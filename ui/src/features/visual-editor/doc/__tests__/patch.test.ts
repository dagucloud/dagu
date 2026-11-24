// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  parseAllDocuments,
  parseDocument,
  type Document,
  type YAMLSeq,
} from 'yaml';
import type { EditResult } from '../../contracts';
import {
  applyTextEdits,
  chooseEmitOptions,
  diffLines,
  documentZeroEnd,
  editDocument,
  editDocumentDetailed,
  splitLines,
  type LineHunk,
} from '../patch';
import {
  yamlArraySteps,
  yamlBroken,
  yamlMapSteps,
  yamlMultiDoc,
  yamlRouter,
} from '../../testing/fixtures';

describe('documentZeroEnd', () => {
  it('is the text length for one document', () => {
    const text = 'name: a\nsteps:\n  - echo hi\n';
    expect(documentZeroEnd(text)).toBe(text.length);
  });

  it('is the line start of the second document', () => {
    const at = yamlMultiDoc.indexOf('---');
    expect(documentZeroEnd(yamlMultiDoc)).toBe(at);
    expect(yamlMultiDoc[at - 1]).toBe('\n');
  });

  it('keeps a trailing comment of document 0', () => {
    const text = 'a: 1\n# tail\n---\nb: 2\n';
    expect(documentZeroEnd(text)).toBe(text.indexOf('---'));
  });

  it('handles a leading marker and CRLF', () => {
    const text = '---\r\na: 1\r\n---\r\nb: 2\r\n';
    expect(documentZeroEnd(text)).toBe(text.lastIndexOf('---'));
  });

  it('stops at a document end marker', () => {
    const text = 'a: 1\n...\n%YAML 1.2\n---\nb: 2\n';
    expect(documentZeroEnd(text)).toBe(text.indexOf('...'));
  });

  it('is zero for an empty text', () => {
    expect(documentZeroEnd('')).toBe(0);
  });
});

describe('applyTextEdits', () => {
  it('applies sorted edits', () => {
    const text = 'abcdef';
    expect(
      applyTextEdits(text, [
        { from: 1, to: 2, insert: 'XX' },
        { from: 4, to: 4, insert: '-' },
        { from: 5, to: 6, insert: '' },
      ])
    ).toBe('aXXcd-e');
  });

  it('returns the text for no edits', () => {
    expect(applyTextEdits('abc', [])).toBe('abc');
  });

  it('throws for overlapping edits', () => {
    expect(() =>
      applyTextEdits('abcdef', [
        { from: 1, to: 3, insert: '' },
        { from: 2, to: 4, insert: '' },
      ])
    ).toThrow(RangeError);
  });
});

describe('diffLines', () => {
  const apply = (a: string[], b: string[], hunks: LineHunk[]) => {
    const out: string[] = [];
    let at = 0;
    for (const h of hunks) {
      out.push(...a.slice(at, h.aStart), ...b.slice(h.bStart, h.bEnd));
      at = h.aEnd;
    }
    return [...out, ...a.slice(at)];
  };

  it('gives no hunk for equal lines', () => {
    expect(diffLines(['a', 'b'], ['a', 'b'])).toEqual([]);
    expect(diffLines([], [])).toEqual([]);
  });

  it('finds one replaced line', () => {
    expect(diffLines(['a', 'b', 'c'], ['a', 'X', 'c'])).toEqual([
      { aStart: 1, aEnd: 2, bStart: 1, bEnd: 2 },
    ]);
  });

  it('finds an insertion and a deletion', () => {
    expect(diffLines(['a', 'b'], ['a', 'n', 'b'])).toEqual([
      { aStart: 1, aEnd: 1, bStart: 1, bEnd: 2 },
    ]);
    expect(diffLines(['a', 'n', 'b'], ['a', 'b'])).toEqual([
      { aStart: 1, aEnd: 2, bStart: 1, bEnd: 1 },
    ]);
  });

  it('finds separate hunks', () => {
    const a = ['1', '2', '3', '4', '5', '6'];
    const b = ['1', 'x', '3', '4', '5', 'y', 'z'];
    expect(diffLines(a, b)).toEqual([
      { aStart: 1, aEnd: 2, bStart: 1, bEnd: 2 },
      { aStart: 5, aEnd: 6, bStart: 5, bEnd: 7 },
    ]);
  });

  it('handles an empty side', () => {
    expect(diffLines([], ['a'])).toEqual([
      { aStart: 0, aEnd: 0, bStart: 0, bEnd: 1 },
    ]);
    expect(diffLines(['a'], [])).toEqual([
      { aStart: 0, aEnd: 1, bStart: 0, bEnd: 0 },
    ]);
  });

  it('rebuilds b from random inputs', () => {
    let seed = 12345;
    const rand = (n: number) => {
      seed = (seed * 1103515245 + 12345) & 0x7fffffff;
      return seed % n;
    };
    for (let round = 0; round < 300; round++) {
      const a = Array.from({ length: rand(30) }, () => String(rand(5)));
      const b = Array.from({ length: rand(30) }, () => String(rand(5)));
      const hunks = diffLines(a, b);
      expect(apply(a, b, hunks)).toEqual(b);
      for (const h of hunks) {
        expect(h.aEnd > h.aStart || h.bEnd > h.bStart).toBe(true);
      }
    }
  });

  // Two separate insertions leave repeated lines between them, so the
  // Myers search runs instead of the common prefix and suffix shortcut.
  it('is fast for 1500 lines with two separate insertions', () => {
    const a = Array.from({ length: 1500 }, (_, i) => `line ${i % 40}`);
    const b = [
      ...a.slice(0, 300),
      'new',
      ...a.slice(300, 1200),
      'new 2',
      ...a.slice(1200),
    ];
    const start = performance.now();
    const hunks = diffLines(a, b);
    expect(performance.now() - start).toBeLessThan(200);
    expect(apply(a, b, hunks)).toEqual(b);
    expect(hunks.length).toBe(2);
  });
});

describe('splitLines', () => {
  it('drops line ends and the empty last line', () => {
    expect(splitLines('a\nb\n')).toEqual(['a', 'b']);
    expect(splitLines('a\r\nb')).toEqual(['a', 'b']);
    expect(splitLines('')).toEqual([]);
    expect(splitLines('\n')).toEqual(['']);
  });
});

describe('chooseEmitOptions', () => {
  const choose = (text: string) => chooseEmitOptions(parseDocument(text), text);

  it('reproduces the fixtures exactly', () => {
    for (const text of [yamlMapSteps, yamlRouter]) {
      const choice = choose(text);
      expect(choice.exact).toBe(true);
      expect(choice.text).toBe(text);
    }
  });

  it('keeps the style but not the comment gap', () => {
    const choice = choose(yamlArraySteps);
    expect(choice.exact).toBe(false);
    expect(choice.options).toMatchObject({ indent: 2, indentSeq: true });
    expect(choice.text).toBe(
      yamlArraySteps.replace('transform   #', 'transform #')
    );
  });

  it('picks a 4-space indent and flush sequences', () => {
    const text = 'steps:\n- name: a\n  with:\n      k: v\n- name: b\n';
    const text4 = 'steps:\n-   name: a\n    with:\n        k: v\n';
    expect(choose(text4).options).toMatchObject({
      indent: 4,
      indentSeq: false,
    });
    expect(choose(text).options).toMatchObject({ indentSeq: false });
  });

  it('picks an indented sequence with a 4-space indent', () => {
    const text = 'a:\n    b:\n        - x\n        - y\n';
    const choice = choose(text);
    expect(choice.exact).toBe(true);
    expect(choice.options).toMatchObject({ indent: 4, indentSeq: true });
  });

  it('is not exact when the text drifts', () => {
    const text = 'a: { x: 1 }\nb:     2\nc: 3\n';
    const choice = choose(text);
    expect(choice.exact).toBe(false);
    expect(choice.text).toBe('a: {x: 1}\nb: 2\nc: 3\n');
  });
});

describe('editDocument', () => {
  const setIn =
    (path: (string | number)[], value: unknown) => (doc: Document) => {
      doc.setIn(path, value);
    };
  const okText = (result: EditResult): string => {
    if (!result.ok) throw new Error(`${result.code}: ${result.message}`);
    return result.text;
  };

  it('gives noop when nothing changes', () => {
    const result = editDocument(yamlArraySteps, () => {});
    expect(result).toMatchObject({ ok: false, code: 'noop' });
    expect(
      editDocument(yamlArraySteps, setIn(['name'], 'nightly-import'))
    ).toMatchObject({
      ok: false,
      code: 'noop',
    });
  });

  it('changes one scalar with one edit', () => {
    const result = editDocument(
      yamlArraySteps,
      setIn(['steps', 2, 'run'], './publish.sh out.csv')
    );
    const old = '    run: ./publish.sh sorted.csv\n';
    const from = yamlArraySteps.indexOf(old);
    expect(result).toEqual({
      ok: true,
      text: yamlArraySteps.replace(old, '    run: ./publish.sh out.csv\n'),
      edits: [
        {
          from,
          to: from + old.length,
          insert: '    run: ./publish.sh out.csv\n',
        },
      ],
    });
    // The comment gap that the emitter would normalize stays.
    expect(okText(result)).toContain('name: transform   # keep the header row');
  });

  it('adds a key to a step', () => {
    const result = editDocument(
      yamlArraySteps,
      setIn(['steps', 0, 'timeout'], '30s')
    );
    const at = yamlArraySteps.indexOf('\n\n  - name: transform') + 1;
    expect(result).toEqual({
      ok: true,
      text: applyTextEdits(yamlArraySteps, [
        { from: at, to: at, insert: '    timeout: 30s\n' },
      ]),
      edits: [{ from: at, to: at, insert: '    timeout: 30s\n' }],
    });
  });

  it('removes a key from a step', () => {
    const result = editDocument(yamlArraySteps, (doc) => {
      doc.deleteIn(['steps', 1, 'depends']);
    });
    const line = '    depends: [fetch]\n';
    const from = yamlArraySteps.indexOf(line);
    expect(result).toEqual({
      ok: true,
      text: yamlArraySteps.replace(line, ''),
      edits: [{ from, to: from + line.length, insert: '' }],
    });
  });

  it('adds a step at the end', () => {
    const result = editDocument(yamlArraySteps, (doc) => {
      (doc.get('steps') as YAMLSeq).add(
        doc.createNode({ name: 'notify', run: 'echo done' })
      );
      return { ok: true, select: 'steps[3]' };
    });
    expect(result).toEqual({
      ok: true,
      text: `${yamlArraySteps}  - name: notify\n    run: echo done\n`,
      edits: [
        {
          from: yamlArraySteps.length,
          to: yamlArraySteps.length,
          insert: '  - name: notify\n    run: echo done\n',
        },
      ],
      select: 'steps[3]',
    });
  });

  it('never changes bytes after the document marker', () => {
    const at = yamlMultiDoc.indexOf('---');
    const result = editDocument(yamlMultiDoc, (doc) => {
      doc.setIn(['steps', 0, 'name'], 'launch-child');
      doc.setIn(['steps', 0, 'with', 'dag'], 'other');
    });
    const text = okText(result);
    expect(text.endsWith(yamlMultiDoc.slice(at))).toBe(true);
    expect(text).toContain('name: launch-child');
    expect(result.ok && result.edits.every((e) => e.to <= at)).toBe(true);
  });

  const flush = `name: x
params:
    FOO: bar
steps:
- name: a
  run: echo a   # keep
- name: b
  run: echo b
`;

  it('keeps 4-space indent and flush sequences', () => {
    const changed = editDocument(flush, setIn(['steps', 1, 'run'], 'echo c'));
    expect(okText(changed)).toBe(flush.replace('echo b', 'echo c'));

    const added = editDocument(flush, (doc) => {
      (doc.get('steps') as YAMLSeq).add(
        doc.createNode({ name: 'c', run: 'echo c' })
      );
    });
    expect(okText(added)).toBe(`${flush}- name: c\n  run: echo c\n`);
  });

  it('keeps CRLF line ends', () => {
    const crlf = yamlArraySteps.replace(/\n/g, '\r\n');
    const changed = editDocument(crlf, setIn(['steps', 2, 'run'], './go.sh'));
    expect(okText(changed)).toBe(
      crlf.replace('    run: ./publish.sh sorted.csv', '    run: ./go.sh')
    );
    expect(changed.ok && changed.edits).toHaveLength(1);

    const added = editDocument(crlf, setIn(['steps', 0, 'timeout'], '30s'));
    const text = okText(added);
    expect(text.replace(/\r\n/g, '')).not.toContain('\n');
    expect(text).toContain('    timeout: 30s\r\n\r\n  - name: transform');
  });

  it('uses UTF-16 offsets after non-ASCII text', () => {
    const text =
      '# café 日本語 😀 résumé\nname: x\nsteps:\n  - name: a\n    run: echo a\n';
    const result = editDocument(text, setIn(['steps', 0, 'run'], 'echo b'));
    const from = text.indexOf('    run:');
    expect(result).toEqual({
      ok: true,
      text: text.replace('echo a', 'echo b'),
      edits: [{ from, to: text.length, insert: '    run: echo b\n' }],
    });
  });

  it('handles a last line without a line end', () => {
    const text = 'steps:\n  - name: a\n    run: echo a';
    expect(
      okText(editDocument(text, setIn(['steps', 0, 'run'], 'echo b')))
    ).toBe('steps:\n  - name: a\n    run: echo b');
    expect(
      okText(editDocument(text, setIn(['steps', 0, 'timeout'], '5s')))
    ).toBe('steps:\n  - name: a\n    run: echo a\n    timeout: 5s');
    expect(
      okText(
        editDocument(
          'steps:\n  - name: a\n    run: echo a\n    timeout: 5s',
          (doc) => {
            doc.deleteIn(['steps', 0, 'timeout']);
          }
        )
      )
    ).toBe('steps:\n  - name: a\n    run: echo a');
  });

  it('gives parseError for broken YAML', () => {
    const result = editDocument(yamlBroken, () => {});
    expect(result).toMatchObject({ ok: false, code: 'parseError' });
    expect(!result.ok && result.message.length > 0).toBe(true);
  });

  it('passes a failure outcome through', () => {
    const result = editDocument(yamlArraySteps, () => ({
      ok: false,
      code: 'unknownStep',
      message: 'No step steps[9].',
    }));
    expect(result).toEqual({
      ok: false,
      code: 'unknownStep',
      message: 'No step steps[9].',
    });
  });

  it('gives invalidYaml when mutate throws', () => {
    const result = editDocument(yamlArraySteps, () => {
      throw new Error('boom');
    });
    expect(result).toMatchObject({ ok: false, code: 'invalidYaml' });
  });

  it('does not change the original text object', () => {
    const copy = `${yamlArraySteps}`;
    editDocument(copy, setIn(['name'], 'other'));
    expect(copy).toBe(yamlArraySteps);
  });

  it('uses a 2-space indent when the text has no indented line', () => {
    const result = editDocument('name: x\n', (doc) => {
      doc.set('steps', doc.createNode([{ name: 'step', run: 'echo c' }]));
    });
    expect(okText(result)).toBe(
      'name: x\nsteps:\n  - name: step\n    run: echo c\n'
    );
  });

  it('keeps a drifted line next to an inserted step', () => {
    const result = editDocument(yamlArraySteps, (doc) => {
      (doc.get('steps') as YAMLSeq).items.splice(
        1,
        0,
        doc.createNode({ name: 'extra', run: 'echo extra' })
      );
    });
    const text = okText(result);
    expect(text).toContain('  - name: transform   # keep the header row\n');
    expect(text).toBe(
      yamlArraySteps.replace(
        '\n\n  - name: transform',
        '\n  - name: extra\n    run: echo extra\n\n  - name: transform'
      )
    );
    expect(result.ok && result.edits).toHaveLength(1);
  });

  it('keeps the comment gap on an edited line', () => {
    const result = editDocument(
      yamlArraySteps,
      setIn(['steps', 1, 'name'], 'convert')
    );
    expect(okText(result)).toBe(
      yamlArraySteps.replace('transform   # keep', 'convert   # keep')
    );
  });

  it('does not use the fallback for a local edit', () => {
    const { result, fallback } = editDocumentDetailed(
      flush,
      setIn(['steps', 1, 'run'], 'echo c')
    );
    expect(result.ok).toBe(true);
    expect(fallback).toBe(false);
  });

  it('replaces the document 0 text when hunks do not fit', () => {
    // The emitter cannot reproduce the 4-space map and the flush sequence together.
    const text = `---\n${flush}---\nname: other\n`;
    const { result, fallback } = editDocumentDetailed(
      text,
      setIn(['params', 'BAR'], 'x')
    );
    expect(fallback).toBe(true);
    if (!result.ok) throw new Error(result.message);
    expect(result.edits).toHaveLength(1);
    expect(result.text.endsWith('---\nname: other\n')).toBe(true);
    const docs = parseAllDocuments(result.text);
    expect(docs[0]!.toJS()).toMatchObject({ params: { FOO: 'bar', BAR: 'x' } });
  });
});

describe('editDocument speed', () => {
  // About 5 lines per step, with a comment and a trailing blank line.
  const big = (count: number) => {
    const steps = Array.from(
      { length: count },
      (_, i) =>
        `  # step ${i}\n  - name: step-${i}\n    depends: [${i === 0 ? '' : `step-${i - 1}`}]\n` +
        `    run: echo ${i}\n\n`
    );
    return `name: big\nsteps:\n${steps.join('')}`;
  };

  it('edits a 300-step file quickly', () => {
    const text = big(300);
    expect(text.split('\n').length).toBeGreaterThan(1500);
    let best = Infinity;
    for (let round = 0; round < 3; round++) {
      const start = performance.now();
      const { result, fallback } = editDocumentDetailed(text, (doc) => {
        doc.setIn(['steps', 150, 'run'], 'echo changed');
      });
      best = Math.min(best, performance.now() - start);
      expect(fallback).toBe(false);
      expect(result.ok && result.edits.length).toBe(1);
    }
    // The bound is loose: the machine can be busy. Typical time is below 60 ms.
    expect(best).toBeLessThan(300);
  });
});
