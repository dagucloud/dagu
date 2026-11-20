// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// @ts-expect-error -- Monaco ships no types for its internal Monarch modules.
import { compile } from 'monaco-editor/esm/vs/editor/standalone/common/monarch/monarchCompile.js';
// @ts-expect-error -- Monaco ships no types for its internal Monarch modules.
import { MonarchTokenizer } from 'monaco-editor/esm/vs/editor/standalone/common/monarch/monarchLexer.js';
import { describe, expect, it } from 'vitest';
import { DAGU_YAML_LANGUAGE } from '../monaco-yaml-language';

type Piece = [text: string, type: string];

// The real Monarch runtime of Monaco, with the services that the tokenizer
// reads stubbed out. The grammar is the one that the editors register.
function makeTokenizer() {
  const lexer = compile('yaml', DAGU_YAML_LANGUAGE);
  const configuration = {
    getValue: () => 20000,
    onDidChangeConfiguration: () => ({ dispose() {} }),
  };
  return new MonarchTokenizer(
    {},
    {},
    'yaml',
    lexer,
    configuration
  ) as unknown as {
    getInitialState(): unknown;
    tokenize(
      line: string,
      hasEOL: boolean,
      state: unknown
    ): {
      tokens: { offset: number; type: string }[];
      endState: unknown;
    };
  };
}

// The tokens of each line, as [text, type] without the ".yaml" postfix and
// without the white tokens. Neighbouring tokens of one type stay apart only
// when the grammar emits a type change.
function tokenize(text: string): Piece[][] {
  const tokenizer = makeTokenizer();
  let state = tokenizer.getInitialState();
  return text.split('\n').map((line, index, all) => {
    const result = tokenizer.tokenize(line, index < all.length - 1, state);
    state = result.endState;
    return result.tokens
      .map((token, i) => {
        const end = result.tokens[i + 1]?.offset ?? line.length;
        return [
          line.slice(token.offset, end),
          token.type.replace(/\.yaml$/, ''),
        ] as Piece;
      })
      .filter(([, type]) => type !== 'white' && type !== '');
  });
}

// The types of one line, in order.
function types(text: string): string[] {
  return tokenize(text).flatMap((line) => line.map(([, type]) => type));
}

describe('YAML grammar: structure', () => {
  it('colours keys, operators and plain values', () => {
    expect(tokenize('name: build')[0]).toEqual([
      ['name', 'type'],
      [':', 'operators'],
      ['build', 'string'],
    ]);
  });

  it('colours a number, a boolean and null as constants', () => {
    expect(types('count: 42')).toEqual(['type', 'operators', 'number']);
    expect(types('on: true')).toEqual(['type', 'operators', 'keyword']);
    expect(types('x: null # none')).toEqual([
      'type',
      'operators',
      'keyword',
      'comment',
    ]);
    expect(types('day: 2026-11-02')).toEqual(['type', 'operators', 'number']);
  });

  it('keeps a number that is part of text as text', () => {
    expect(types('version: 42 items')).toEqual(['type', 'operators', 'string']);
  });

  it('ends a comment at the end of the line', () => {
    const lines = tokenize('a: b # note\nc: d');
    expect(lines[0]).toEqual([
      ['a', 'type'],
      [':', 'operators'],
      ['b', 'string'],
      [' # note', 'comment'],
    ]);
    expect(lines[1]?.map(([, type]) => type)).toEqual([
      'type',
      'operators',
      'string',
    ]);
  });

  it('reads a key with no value and a sequence entry', () => {
    const lines = tokenize('steps:\n  - name: a\n  - plain text');
    expect(lines[0]).toEqual([
      ['steps', 'type'],
      [':', 'operators'],
    ]);
    expect(lines[1]?.map(([, type]) => type)).toEqual([
      'operators',
      'type',
      'operators',
      'string',
    ]);
    expect(lines[2]).toEqual([
      ['-', 'operators'],
      ['plain text', 'string'],
    ]);
  });

  it('reads a flow sequence of constants', () => {
    expect(types('items: [1, two, "3"]')).toEqual([
      'type',
      'operators',
      'delimiter.square',
      'number',
      'delimiter.comma',
      'string',
      'delimiter.comma',
      'string',
      'delimiter.square',
    ]);
  });
});

describe('YAML grammar: a value is one scalar', () => {
  it('does not read a colon inside a plain value as a key', () => {
    expect(
      tokenize('prompt: Cite la source : https://x.test/a?b=1')[0]
    ).toEqual([
      ['prompt', 'type'],
      [':', 'operators'],
      ['Cite la source : https://x.test/a?b=1', 'string'],
    ]);
  });

  it('does not read a # without a space before it as a comment', () => {
    expect(types('url: http://x.test/#top')).toEqual([
      'type',
      'operators',
      'string',
    ]);
  });

  it('marks a reference inside a plain value', () => {
    expect(tokenize('run: echo ${params.name} done')[0]).toEqual([
      ['run', 'type'],
      [':', 'operators'],
      ['echo ', 'string'],
      ['${params.name}', 'variable.reference'],
      [' done', 'string'],
    ]);
  });

  it('keeps a lone dollar and an escaped reference as text', () => {
    expect(tokenize('cost: 5$ or \\${x}')[0]?.slice(2)).toEqual([
      ['5$ or \\${x}', 'string'],
    ]);
  });
});

describe('YAML grammar: a prompt pasted as a plain scalar', () => {
  // Markdown, a URL and a colon in one line: all of it is one scalar.
  it('keeps a long French line as one string', () => {
    const line =
      'prompt: Contexte **gras** : le pass \u2014 31 PRs, `feat/x` (2026-11-02) - voir https://x.test/a?b=c&d=e: true';
    expect(tokenize(line)[0]?.slice(2)).toEqual([
      [line.slice('prompt: '.length), 'string'],
    ]);
  });
});

describe('YAML grammar: quoted strings', () => {
  it('colours a double-quoted string, its escapes and its references', () => {
    expect(tokenize('p: "a\\n${x} b"')[0]).toEqual([
      ['p', 'type'],
      [':', 'operators'],
      ['"a', 'string'],
      ['\\n', 'string.escape'],
      ['${x}', 'variable.reference'],
      [' b"', 'string'],
    ]);
  });

  it('lets a double-quoted string span lines without an invalid mark', () => {
    const lines = tokenize('p: "first: one\n  second: 42 # no\n  end"\nk: v');
    for (const line of lines.slice(0, 3)) {
      for (const [, type] of line.slice(line === lines[0] ? 2 : 0)) {
        expect(type).toBe('string');
      }
    }
    expect(lines[3]?.map(([, type]) => type)).toEqual([
      'type',
      'operators',
      'string',
    ]);
  });

  it('reads a single-quoted string with a doubled quote', () => {
    expect(tokenize("p: 'l''essentiel: true'")[0]?.slice(2)).toEqual([
      ["'l", 'string'],
      ["''", 'string.escape'],
      ["essentiel: true'", 'string'],
    ]);
  });
});

describe('YAML grammar: block scalars', () => {
  const PROMPT = [
    'with:',
    '  prompt: |',
    '    # Title',
    '',
    '    **Context**: the 31 PRs — 2026-11-02',
    '    - item: true',
    '        - nested: 42 # not a comment',
    '    key: value',
    '    1. Step ${steps.build.outputs.result} {braces}',
    '  other: 1',
  ].join('\n');

  it('colours every line of the scalar as one string', () => {
    const lines = tokenize(PROMPT);
    for (const index of [2, 4, 5, 6, 7]) {
      expect(lines[index]?.map(([, type]) => type)).toEqual(['string']);
    }
    expect(lines[3]).toEqual([]);
  });

  it('marks only the references inside the scalar', () => {
    const line = tokenize(PROMPT)[8];
    expect(line).toEqual([
      ['1. Step ', 'string'],
      ['${steps.build.outputs.result}', 'variable.reference'],
      [' {braces}', 'string'],
    ]);
  });

  it('colours the header of the scalar as an operator', () => {
    expect(tokenize(PROMPT)[1]).toEqual([
      ['prompt', 'type'],
      [':', 'operators'],
      ['|', 'operators'],
    ]);
  });

  it('leaves the scalar at the first line that is not more indented', () => {
    expect(tokenize(PROMPT)[9]).toEqual([
      ['other', 'type'],
      [':', 'operators'],
      ['1', 'number'],
    ]);
  });

  it('reads a header with a chomping mark and a comment', () => {
    const lines = tokenize('a: >-  # why\n  x: 1\nb: 2');
    expect(lines[0]?.map(([, type]) => type)).toEqual([
      'type',
      'operators',
      'operators',
      'comment',
    ]);
    expect(lines[1]).toEqual([['x: 1', 'string']]);
    expect(lines[2]?.[0]).toEqual(['b', 'type']);
  });

  it('reads a block scalar at the top level of the file', () => {
    const lines = tokenize('prompt: |\n key: v\nnext: 1');
    expect(lines[1]).toEqual([['key: v', 'string']]);
    expect(lines[2]?.[0]).toEqual(['next', 'type']);
  });

  it('reads a block scalar of a sequence entry', () => {
    const lines = tokenize('list:\n  - |\n    a: b\n  - c');
    expect(lines[2]).toEqual([['a: b', 'string']]);
    expect(lines[3]?.map(([, type]) => type)).toEqual(['operators', 'string']);
  });

  it('keeps a sibling key of a dash entry out of the scalar', () => {
    const lines = tokenize('- prompt: |\n    text: 1\n  name: x');
    expect(lines[1]).toEqual([['text: 1', 'string']]);
    expect(lines[2]?.[0]).toEqual(['name', 'type']);
  });

  it('stays in the scalar across blank lines and a deeper block', () => {
    const lines = tokenize(
      ['k: |', '  a', '', '', '      deep: 1', '  b', 'z: 3'].join('\n')
    );
    expect(lines[4]).toEqual([['deep: 1', 'string']]);
    expect(lines[5]).toEqual([['b', 'string']]);
    expect(lines[6]?.[0]).toEqual(['z', 'type']);
  });

  it('handles tabs, emoji, right-to-left text and long lines', () => {
    const text = `k: |\n  tab\there \u{1F600} שלום ${'x'.repeat(10000)}\nn: 1`;
    const lines = tokenize(text);
    expect(lines[1]?.map(([, type]) => type)).toEqual(['string']);
    expect(lines[2]?.[0]).toEqual(['n', 'type']);
  });
});
