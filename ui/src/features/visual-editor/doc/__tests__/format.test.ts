// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { parse, parseAllDocuments, stringify } from 'yaml';
import {
  DAG_KEY_ORDER,
  FORMAT_LINE_WIDTH,
  STEP_KEY_ORDER,
  formatStepRange,
  formatYaml,
  isFormattable,
  isSameYaml,
  minimalEdit,
} from '../format';
import { parseDagDoc } from '../parse';
import { applyTextEdits } from '../patch';

const here = path.dirname(fileURLToPath(import.meta.url));
const fixtureDir = path.join(here, 'fixtures');
const e2eDir = path.resolve(here, '../../../../../e2e/fixtures/dags');

const base = 'name: demo\nsteps:\n  - name: a\n    run: echo a\n';

describe('isSameYaml', () => {
  it('ignores blank lines, trailing spaces and the final line end', () => {
    expect(isSameYaml(base, base + '\n\n')).toBe(true);
    expect(isSameYaml(base, base.replace('name: demo', 'name: demo   '))).toBe(
      true
    );
    expect(isSameYaml(base, base.replace('steps:', '\n\nsteps:'))).toBe(true);
    expect(isSameYaml(base, base.trimEnd())).toBe(true);
    expect(isSameYaml(base, '\n' + base)).toBe(true);
  });

  it('ignores line end style and indentation width', () => {
    expect(isSameYaml(base, base.replace(/\n/g, '\r\n'))).toBe(true);
    const wide = 'name: demo\nsteps:\n    - name: a\n      run: echo a\n';
    expect(isSameYaml(base, wide)).toBe(true);
  });

  it('counts a changed value, a new key and a changed key order', () => {
    expect(isSameYaml(base, base.replace('echo a', 'echo b'))).toBe(false);
    expect(isSameYaml(base, base + '    shell: bash\n')).toBe(false);
    expect(
      isSameYaml('a: 1\nb: 2\n', 'b: 2\na: 1\n'),
      'key order is a change'
    ).toBe(false);
    expect(isSameYaml('- 1\n- 2\n', '- 2\n- 1\n')).toBe(false);
  });

  it('counts a comment that is added, removed or changed', () => {
    expect(isSameYaml(base, '# note\n' + base)).toBe(false);
    expect(isSameYaml('# a\n' + base, '# b\n' + base)).toBe(false);
    expect(isSameYaml('# a\n' + base, base)).toBe(false);
    expect(isSameYaml(base, base.replace('echo a', 'echo a # why'))).toBe(
      false
    );
  });

  it('counts a comment that moves to another node', () => {
    const above = (step: string) =>
      ['a', 'b']
        .map((name) => {
          const comment = name === step ? '  # build\n' : '';
          return `${comment}  - name: ${name}\n    run: echo ${name}\n`;
        })
        .join('');
    expect(isSameYaml(`steps:\n${above('a')}`, `steps:\n${above('b')}`)).toBe(
      false
    );
    expect(isSameYaml('# a\nname: x\nb: 1\n', 'name: x\n# a\nb: 1\n')).toBe(
      false
    );
  });

  it('ignores a blank line between a comment and a key', () => {
    expect(isSameYaml('# a\nname: x\n', '# a\n\nname: x\n')).toBe(true);
    // The library gives the head to the first key or to the document.
    expect(isSameYaml('# a\n\n# b\nname: x\n', '# a\n\n# b\n\nname: x\n')).toBe(
      true
    );
    expect(isSameYaml('a: 1\n# tail\n', 'a: 1\n\n# tail\n\n')).toBe(true);
  });

  it('takes a quote style change as the same value', () => {
    expect(isSameYaml('name: "demo"\n', "name: 'demo'\n")).toBe(true);
    expect(isSameYaml('name: demo\n', 'name: "demo"\n')).toBe(true);
    expect(isSameYaml('name: "1"\n', 'name: 1\n')).toBe(false);
  });

  it('counts a change of an integer beyond the float precision', () => {
    expect(
      isSameYaml(
        'CHANNEL: 1234567890123456789\n',
        'CHANNEL: 1234567890123456788\n'
      )
    ).toBe(false);
  });

  it('keeps the blank lines of a block scalar as content', () => {
    expect(isSameYaml('run: |\n  a\n\n  b\n', 'run: |\n  a\n  b\n')).toBe(
      false
    );
  });

  it('compares text without blank lines when a text does not parse', () => {
    const broken = 'steps:\n  - name: a\n   run: [x';
    expect(isSameYaml(broken, broken + '\n\n')).toBe(true);
    expect(isSameYaml(broken, broken.replace('run', 'cmd'))).toBe(false);
    expect(isSameYaml(broken, base)).toBe(false);
    expect(isSameYaml('', '\n\n')).toBe(true);
  });

  // An alias without its anchor parses, but the document has no value.
  it('compares text when an alias has no anchor', () => {
    const unresolved = 'steps:\n  - name: a\n    run: *missing\n';
    expect(isSameYaml(base, unresolved)).toBe(false);
    expect(isSameYaml(unresolved, unresolved + '\n\n')).toBe(true);
  });

  it('compares every document of a multi document text', () => {
    const one = 'name: a\n---\nname: b\n';
    expect(isSameYaml(one, 'name: a\n\n---\n\nname: b\n')).toBe(true);
    expect(isSameYaml(one, 'name: a\n---\nname: c\n')).toBe(false);
    expect(isSameYaml(one, 'name: a\n')).toBe(false);
  });
});

describe('formatYaml', () => {
  const messy = [
    '# header',
    'steps:',
    '- depends: [a,   b]',
    '  retry_policy: {limit: 2}',
    '  run:   echo go   ',
    '  name: second',
    '  id: second',
    '',
    '',
    '',
    '- name: first',
    '  run: echo 1',
    'queue: q1',
    'params:   [x]',
    'description: hi',
    'name: demo',
    '',
  ].join('\n');

  it('puts the keys in the canonical order, everywhere', () => {
    const out = formatYaml(messy);
    const docs = parseAllDocuments(out);
    const value = docs[0]!.toJS() as Record<string, unknown>;
    expect(Object.keys(value)).toEqual([
      'name',
      'description',
      'params',
      'queue',
      'steps',
    ]);
    const steps = value.steps as Record<string, unknown>[];
    expect(Object.keys(steps[0]!)).toEqual([
      'id',
      'name',
      'run',
      'depends',
      'retry_policy',
    ]);
    expect(out).toContain('# header');
  });

  it('prints block style with two spaces and one blank line between steps', () => {
    expect(formatYaml(messy)).toBe(
      [
        '# header',
        '',
        'name: demo',
        'description: hi',
        'params: [x]',
        'queue: q1',
        '',
        'steps:',
        '  - id: second',
        '    name: second',
        '    run: echo go',
        '    depends: [a, b]',
        '    retry_policy:',
        '      limit: 2',
        '',
        '  - name: first',
        '    run: echo 1',
        '',
      ].join('\n')
    );
  });

  it('keeps a long flow sequence in block style', () => {
    const names = Array.from({ length: 12 }, (_, i) => `step_number_${i}`);
    const out = formatYaml(
      `steps:\n  - name: x\n    run: echo\n    depends: [${names.join(', ')}]\n`
    );
    expect(out).toContain('    depends:\n      - step_number_0\n');
    expect(out).not.toMatch(/\[step_number/);
  });

  it('drops trailing spaces and ends with one line end', () => {
    const out = formatYaml('name: demo   \n# c   \n\n\n');
    expect(out).toBe('name: demo\n\n# c\n');
    expect(formatYaml('name: demo')).toBe('name: demo\n');
  });

  it('puts unknown keys after the known ones, in their own order', () => {
    const out = formatYaml('zeta: 1\nsteps: []\nalpha: 2\nname: n\n');
    expect(out.split('\n').filter(Boolean)).toEqual([
      'name: n',
      'steps: []',
      'zeta: 1',
      'alpha: 2',
    ]);
  });

  it('orders the handlers and map-form steps too', () => {
    const out = formatYaml(
      'handler_on:\n  failure:\n    run: b\n    name: f\n  init:\n    run: a\n'
    );
    const value = parseAllDocuments(out)[0]!.toJS() as {
      handler_on: Record<string, Record<string, unknown>>;
    };
    expect(Object.keys(value.handler_on)).toEqual(['init', 'failure']);
    expect(Object.keys(value.handler_on.failure!)).toEqual(['name', 'run']);
  });

  it('keeps the comment of a key with the key', () => {
    const out = formatYaml(
      'steps:\n  - run: x # why\n    # about the name\n    name: n\n'
    );
    expect(out).toContain('# why');
    expect(out).toContain('# about the name');
    expect(out.indexOf('# about the name')).toBeLessThan(
      out.indexOf('name: n')
    );
  });

  it('leaves long strings and block scalars as written', () => {
    const long = 'x'.repeat(200);
    const out = formatYaml(
      `steps:\n  - name: a\n    run: |\n      echo one\n\n      echo ${long}\n    description: ${long}\n`
    );
    expect(out).toContain(`description: ${long}\n`);
    expect(out).toContain('run: |\n      echo one\n\n      echo ');
  });

  it('returns a text that does not parse unchanged', () => {
    const broken = 'steps:\n  - name: a\n   run: [x';
    expect(formatYaml(broken)).toBe(broken);
    expect(isFormattable(broken)).toBe(false);
    expect(isFormattable(base)).toBe(true);
    expect(formatYaml('')).toBe('');
  });

  it('returns a text unchanged when an alias would come before its anchor', () => {
    // stdout goes before stderr in the canonical order.
    const text =
      'steps:\n  - name: a\n    run: echo\n    stderr: &log /tmp/a.log\n    stdout: *log\n';
    expect(formatYaml(text)).toBe(text);
  });

  it('keeps the line ends of a kept block scalar at the end', () => {
    const text = 'steps:\n  - name: a\n    script: |+\n      echo hi\n\n\n';
    expect(parse(formatYaml(text))).toEqual(parse(text));
  });

  it('keeps an integer beyond the float precision', () => {
    const text = 'env:\n  CHANNEL: 1234567890123456789\n';
    expect(formatYaml(text)).toBe(text);
  });

  it('formats every document of a multi document text', () => {
    const out = formatYaml('steps: []\nname: a\n---\nsteps: []\nname: b\n');
    expect(out).toBe('name: a\nsteps: []\n---\nname: b\nsteps: []\n');
  });

  it('has a canonical key order without a repeated key', () => {
    expect(new Set(DAG_KEY_ORDER).size).toBe(DAG_KEY_ORDER.length);
    expect(new Set(STEP_KEY_ORDER).size).toBe(STEP_KEY_ORDER.length);
    expect(STEP_KEY_ORDER.slice(0, 4)).toEqual([
      'id',
      'name',
      'description',
      'action',
    ]);
  });

  it('gives the edit of the text that one undo takes back', () => {
    const edit = minimalEdit(messy, formatYaml(messy));
    expect(edit).not.toBeNull();
    const { from, to, insert } = edit!;
    expect(messy.slice(0, from) + insert + messy.slice(to)).toBe(
      formatYaml(messy)
    );
    expect(minimalEdit('a', 'a')).toBeNull();
  });
});

/** The comment lines of a text, sorted: moving a comment does not lose it. */
function commentsOf(text: string): string[] {
  return text
    .split('\n')
    .flatMap((line) => {
      const hit = /(^|\s)#\s?(.*)$/.exec(line.trim());
      return hit && !/["']/.test(line.slice(0, hit.index))
        ? [hit[2]!.trim()]
        : [];
    })
    .sort();
}

function load(dir: string): { name: string; text: string }[] {
  try {
    return readdirSync(dir)
      .filter((name) => /\.ya?ml$/.test(name))
      .map((name) => ({
        name,
        text: readFileSync(path.join(dir, name), 'utf8'),
      }));
  } catch {
    return [];
  }
}

describe('formatYaml on the fixtures', () => {
  const files = [...load(fixtureDir), ...load(e2eDir)];

  it('has fixtures to read', () => {
    expect(files.length).toBeGreaterThan(20);
  });

  it.each(files.map((file) => [file.name, file.text] as const))(
    '%s: idempotent, same data, same comments',
    (_name, text) => {
      const once = formatYaml(text);
      expect(formatYaml(once)).toBe(once);
      if (!isFormattable(text)) {
        expect(once).toBe(text);
        return;
      }
      // The same documents, but for the order of the keys.
      const before = parseAllDocuments(text).map((doc) => doc.toJS());
      const after = parseAllDocuments(once).map((doc) => doc.toJS());
      expect(sortKeys(after)).toEqual(sortKeys(before));
      expect(commentsOf(once)).toEqual(commentsOf(text));
      // Every line fits the width, but a line that cannot be folded.
      for (const line of once.split('\n')) {
        if (line.length > FORMAT_LINE_WIDTH)
          expect(unfoldable(line)).toBe(true);
      }
      // A text with no content stays as it is.
      expect(once.endsWith('\n') || text.trim() === '').toBe(true);
      expect(once).not.toMatch(/[ \t]+\n/);
    }
  );
});

function sortKeys(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortKeys);
  if (value instanceof Map) return value;
  if (value && typeof value === 'object' && !(value instanceof Date)) {
    return Object.fromEntries(
      Object.entries(value as Record<string, unknown>)
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
        .map(([key, item]) => [key, sortKeys(item)])
    );
  }
  return value;
}

/** True for a line that the formatter cannot fold: a comment, a block scalar line or a long word. */
function unfoldable(line: string): boolean {
  const text = line.trim();
  if (text.startsWith('#')) return true;
  // The library may keep a long word on the line of the words before it.
  if (/\S{60,}/.test(text)) return true;
  // One word, or a key and one word.
  const rest = text.replace(/^(- )*([^\s:]+: )?/, '');
  return !/\s/.test(rest) || /^[|>]/.test(rest) || line.startsWith('      ');
}

const words = [
  'alpha',
  'beta',
  'x',
  "it's",
  'say "hi"',
  '${steps.build.outputs.result}',
  '${params.NAME}',
  '$HOME',
  'a: b',
  'a #b',
  '#tag',
  '- dash',
  '*star',
  '&amp',
  '!bang',
  '[x]',
  '{y}',
  '@at',
  '`tick`',
  '50%',
  'caf\u00e9',
  '\u4f60\u597d',
  '\ud83d\ude00',
  'https://example.com/a/very/long/path/without/any/space/inside/it/' +
    'p'.repeat(60),
  'true',
  'null',
  '123',
  '--flag=value',
  '|',
  '>',
  '\\n',
  'tab\there',
];

/** A small seeded generator: the property test is the same on each run. */
function random(seed: number): () => number {
  let state = seed;
  return () => {
    state = (state * 1664525 + 1013904223) % 4294967296;
    return state / 4294967296;
  };
}

function sentence(next: () => number): string {
  const count = 1 + Math.floor(next() * 40);
  let out = '';
  for (let i = 0; i < count; i++) {
    const gap = next() < 0.1 ? '  ' : ' ';
    out += (i > 0 ? gap : '') + words[Math.floor(next() * words.length)];
  }
  if (next() < 0.1) out = ' ' + out;
  if (next() < 0.1) out += ' ';
  if (next() < 0.1) out += '\n' + sentence(next);
  return out;
}

describe('formatYaml folds long lines', () => {
  const long = Array.from({ length: 30 }, (_, i) => `word${i}`).join(' ');

  const widest = (text: string) =>
    Math.max(...text.split('\n').map((line) => line.length));

  it('folds a long description at the width, with the same value', () => {
    const out = formatYaml(`name: n\ndescription: ${long}\nsteps: []\n`);
    expect(out.split('\n').length).toBeGreaterThan(4);
    expect(widest(out)).toBeLessThanOrEqual(FORMAT_LINE_WIDTH);
    expect((parse(out) as { description: string }).description).toBe(long);
  });

  it('folds a long step description and a long quoted string', () => {
    const quoted = `"${long} \\"inner\\" caf\u00e9"`;
    const text = `steps:\n  - name: a\n    description: ${long}\n    run: ${quoted}\n`;
    const out = formatYaml(text);
    expect(widest(out)).toBeLessThanOrEqual(FORMAT_LINE_WIDTH);
    expect(parse(out)).toEqual(parse(text));
    expect(out).toContain('    run: "word0');
  });

  it('leaves a long URL without spaces on one line', () => {
    const url = `https://example.com/${'segment/'.repeat(20)}end`;
    const out = formatYaml(`name: n\ndescription: ${url}\n`);
    expect(out).toBe(`name: n\ndescription: ${url}\n`);
  });

  it('folds a command with a step reference and keeps the value', () => {
    const run = `echo "result \${steps.build.outputs.result} ${long} for \${params.NAME}"`;
    const text = `steps:\n  - name: a\n    run: ${JSON.stringify(run)}\n`;
    const out = formatYaml(text);
    expect(out.split('\n').length).toBeGreaterThan(4);
    expect(widest(out)).toBeLessThanOrEqual(FORMAT_LINE_WIDTH);
    expect(parse(out)).toEqual(parse(text));
    expect(formatYaml(out)).toBe(out);
  });

  it('keeps the trailing space of a line of a multi-line string', () => {
    const text = 'steps:\n  - name: a\n    run: "echo one \\n echo two  \\n"\n';
    expect(parse(formatYaml(text))).toEqual(parse(text));
    const block =
      'steps:\n  - name: a\n    run: |-\n      echo one \n      echo two\n';
    expect(parse(formatYaml(block))).toEqual(parse(block));
  });

  it('keeps a literal block of a command as written', () => {
    const text = `steps:\n  - name: a\n    run: |\n      echo one\n      echo ${long} ${long}\n`;
    expect(formatYaml(text)).toBe(text);
  });

  it('writes a long flow sequence and a flow map in block style', () => {
    const names = Array.from({ length: 12 }, (_, i) => `dependency_${i}`);
    const text =
      `steps:\n  - name: x\n    run: echo\n    depends: [${names.join(', ')}]\n` +
      `    with: {first: 1, second: two, third: three, fourth: four, fifth: five, sixth: six, seventh: 7}\n`;
    const out = formatYaml(text);
    expect(out).toContain('    depends:\n      - dependency_0\n');
    expect(out).toContain('    with:\n      first: 1\n');
    expect(out).not.toMatch(/[[{]/);
    expect(parse(out)).toEqual(parse(text));
    expect(widest(out)).toBeLessThanOrEqual(FORMAT_LINE_WIDTH);
  });

  it('does not reflow a long comment', () => {
    const comment = `# ${long} ${long}`;
    const out = formatYaml(`${comment}\nname: n\n`);
    expect(out).toBe(`${comment}\nname: n\n`);
  });

  it('is idempotent and keeps the comments on a messy text with long lines', () => {
    const text = readFileSync(path.join(fixtureDir, 'long-lines.yaml'), 'utf8');
    const once = formatYaml(text);
    expect(formatYaml(once)).toBe(once);
    expect(formatYaml(once)).toBe(formatYaml(formatYaml(text)));
    expect(commentsOf(once)).toEqual(commentsOf(text));
    expect(sortKeys(parse(once))).toEqual(sortKeys(parse(text)));
    const lines = once.split('\n');
    expect(lines.length).toBeGreaterThan(text.split('\n').length);
    for (const line of lines) {
      if (line.length > FORMAT_LINE_WIDTH) expect(unfoldable(line)).toBe(true);
    }
  });

  it('keeps the data of 1500 generated strings, in every position', () => {
    const next = random(20260101);
    for (let i = 0; i < 1500; i++) {
      const value = sentence(next);
      const data = {
        name: 'generated',
        description: value,
        steps: [
          { name: 'a', run: value, env: { KEY: value }, depends: [value] },
          { name: 'b', with: { text: value }, command: [value, value] },
        ],
      };
      const text = stringify(data, { lineWidth: 0 });
      const out = formatYaml(text);
      expect(parse(out), JSON.stringify(value)).toEqual(data);
      expect(formatYaml(out), JSON.stringify(value)).toBe(out);
      // A value with a line end keeps its lines.
      if (value.includes('\n')) continue;
      for (const line of out.split('\n')) {
        if (line.length > FORMAT_LINE_WIDTH) {
          expect(unfoldable(line), line).toBe(true);
        }
      }
    }
  });
});

describe('formatStepRange', () => {
  const long = `echo ${'word '.repeat(30)}end`;
  // The second step is out of order and has a long line; the others are
  // canonical, so that formatYaml touches only the second step.
  const messy = [
    'name: demo',
    '',
    'steps:',
    '  - id: first',
    '    name: first',
    '    run: echo 1',
    '',
    '  - run: echo 2   # keep this',
    '    name: second',
    '    id: second',
    `    description: ${long}`,
    '    depends: [first]',
    '',
    '  - id: third',
    '    name: third',
    '    run: echo 3',
    '',
  ].join('\n');

  function formatStep(text: string, index: number): string {
    const model = parseDagDoc(text);
    const edit = formatStepRange(text, model.steps[index]!);
    return edit ? applyTextEdits(text, [edit]) : text;
  }

  it('formats only the text of the step', () => {
    const model = parseDagDoc(messy);
    const range = model.steps[1]!.range;
    const out = formatStep(messy, 1);
    expect(out).not.toBe(messy);
    // The text before and after the step is the same, byte for byte.
    expect(out.slice(0, range.from)).toBe(messy.slice(0, range.from));
    expect(out.endsWith(messy.slice(range.to))).toBe(true);
    // The keys are in canonical order, the comment and the indentation stay.
    expect(out).toContain('  - id: second\n    name: second\n');
    expect(out).toContain('# keep this');
    expect(parse(out)).toEqual(parse(messy));
  });

  it('gives the result of formatYaml for the same step', () => {
    // The long line folds at the same column as in the whole file.
    const whole = formatYaml(messy);
    expect(formatStep(messy, 1)).toBe(whole);
    for (const line of whole.split('\n')) {
      expect(line.length).toBeLessThanOrEqual(FORMAT_LINE_WIDTH);
    }
  });

  it('formats a step of the map form with its key', () => {
    const text = [
      'name: demo',
      '',
      'steps:',
      '  one:',
      '    depends: []',
      '    run: echo 1',
      '    id: one',
      '',
    ].join('\n');
    const out = formatStep(text, 0);
    expect(out).toBe(formatYaml(text));
    expect(out.indexOf('id: one')).toBeLessThan(out.indexOf('run: echo 1'));
  });

  it('formats a handler with its key', () => {
    const text = [
      'name: demo',
      '',
      'steps:',
      '  - id: a',
      '    name: a',
      '    run: echo a',
      '',
      'handler_on:',
      '  failure:',
      '    run: echo failed',
      '    name: on failure',
      '',
    ].join('\n');
    const handler = parseDagDoc(text).handlers[0]!;
    const edit = formatStepRange(text, handler);
    expect(edit).not.toBeNull();
    expect(applyTextEdits(text, [edit!])).toBe(formatYaml(text));
  });

  it('is idempotent and returns null for a canonical step', () => {
    const once = formatStep(messy, 1);
    const model = parseDagDoc(once);
    expect(formatStepRange(once, model.steps[1]!)).toBeNull();
    expect(formatStepRange(once, model.steps[0]!)).toBeNull();
  });

  it('keeps CRLF line ends', () => {
    const crlf = messy.replace(/\n/g, '\r\n');
    const out = formatStep(crlf, 1);
    expect(out).not.toMatch(/[^\r]\n/);
    expect(out.replace(/\r\n/g, '\n')).toBe(formatYaml(messy));
  });

  it('keeps a line of spaces inside a block scalar', () => {
    const text =
      'name: demo\n\nsteps:\n  - name: a\n    run: |\n      echo a\n        \n      echo b\n';
    const out = formatStep(text, 0);
    expect(parse(out)).toEqual(parse(text));
    expect(out).toBe(formatYaml(text));
  });

  it('keeps the line ends of a kept block scalar at the end of the step', () => {
    const text =
      'name: demo\n\nsteps:\n  - name: a\n    run: |+\n      echo a\n\n\n  - name: b\n    run: echo b\n';
    expect(formatStepRange(text, parseDagDoc(text).steps[0]!)).toBeNull();
  });

  it('returns null when an alias would come before its anchor', () => {
    const text =
      'name: demo\n\nsteps:\n  - name: a\n    run: echo\n    stderr: &log /tmp/a.log\n    stdout: *log\n';
    expect(formatStepRange(text, parseDagDoc(text).steps[0]!)).toBeNull();
  });

  // Block style cannot go inside a flow collection.
  it('returns null for a step in a flow collection', () => {
    for (const text of [
      'name: demo\nsteps: [{run: echo a, name: a}]\n',
      'name: demo\nsteps: [\n  {run: echo a, name: a}\n]\n',
      'name: demo\nsteps: {a: {run: echo a, description: x}}\n',
    ]) {
      expect(formatStepRange(text, parseDagDoc(text).steps[0]!)).toBeNull();
    }
  });

  it('formats a flow mapping step of a block list', () => {
    const text = 'name: demo\nsteps:\n  - {run: echo a, name: a}\n';
    expect(formatStep(text, 0)).toBe(
      'name: demo\nsteps:\n  - name: a\n    run: echo a\n'
    );
  });

  it('returns null for a broken step', () => {
    const text = 'name: demo\nsteps:\n  - name: a\n    run: [unclosed\n';
    const model = parseDagDoc(text);
    for (const step of model.steps) {
      expect(formatStepRange(text, step)).toBeNull();
    }
  });
});
