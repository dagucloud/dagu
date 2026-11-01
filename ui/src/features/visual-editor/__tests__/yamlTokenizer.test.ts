// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  tokenizeYaml,
  tokenizeYamlValue,
  type YamlLine,
} from '../yamlTokenizer';

function kinds(line: YamlLine) {
  return line.tokens
    .filter((t) => t.kind !== 'plain')
    .map((t) => `${t.kind}:${t.text}`);
}

function first(source: string) {
  return kinds(tokenizeYaml(source)[0]!);
}

describe('tokenizeYaml', () => {
  it('colours a key and a plain string', () => {
    expect(first('name: build')).toEqual([
      'key:name',
      'punct::',
      'string:build',
    ]);
  });

  it('colours numbers, true, false and null', () => {
    expect(first('a: 42')).toContain('number:42');
    expect(first('a: -1.5e3')).toContain('number:-1.5e3');
    expect(first('a: 0x1F')).toContain('number:0x1F');
    expect(first('a: true')).toContain('keyword:true');
    expect(first('a: False')).toContain('keyword:False');
    expect(first('a: null')).toContain('keyword:null');
    expect(first('a: ~')).toContain('keyword:~');
    expect(first('a: 1.2.3')).toContain('string:1.2.3');
  });

  it('colours quoted strings, with an escape and the empty string', () => {
    expect(first('url: ""')).toEqual(['key:url', 'punct::', 'string:""']);
    expect(first('a: "x \\" y"')).toContain('string:"x \\" y"');
    expect(first("a: 'it''s'")).toContain("string:'it''s'");
    expect(first('a: "#not a comment"')).toContain('string:"#not a comment"');
  });

  it('colours a comment, alone or after a value', () => {
    expect(first('# note')).toEqual(['comment:# note']);
    expect(first('a: 1 # note')).toEqual([
      'key:a',
      'punct::',
      'number:1',
      'comment:# note',
    ]);
    // A hash inside a word is part of it.
    expect(first('a: b#c')).toContain('string:b#c');
  });

  it('colours variable references inside a string', () => {
    expect(first('a: "x ${NAME} y"')).toEqual([
      'key:a',
      'punct::',
      'string:"x ',
      'variable:${NAME}',
      'string: y"',
    ]);
    expect(first('a: $HOME/bin')).toContain('variable:$HOME');
  });

  it('keeps the indent, the key and the list markers', () => {
    const [a, b, c] = tokenizeYaml('with:\n  - url: x\n  - plain item');
    expect(a).toMatchObject({ key: 'with', indent: 0 });
    expect(b).toMatchObject({ key: 'url', indent: 2 });
    expect(kinds(b!)).toEqual(['punct:-', 'key:url', 'punct::', 'string:x']);
    expect(c!.key).toBeUndefined();
    expect(kinds(c!)).toEqual(['punct:-', 'string:plain item']);
  });

  it('reads a quoted key', () => {
    const [line] = tokenizeYaml('"my key": 1');
    expect(line!.key).toBe('my key');
    expect(kinds(line!)[0]).toBe('key:"my key"');
  });

  it('does not take the colon of a URL for a key end', () => {
    const [line] = tokenizeYaml('url: http://x.io:80/a');
    expect(line!.key).toBe('url');
    expect(kinds(line!)).toEqual([
      'key:url',
      'punct::',
      'string:http://x.io:80/a',
    ]);
  });

  it('colours a flow mapping and a flow sequence', () => {
    expect(first('with: {}')).toEqual(['key:with', 'punct::', 'punct:{}']);
    expect(first('a: [1, "x", true]')).toEqual([
      'key:a',
      'punct::',
      'punct:[',
      'number:1',
      'punct:,',
      'string:"x"',
      'punct:,',
      'keyword:true',
      'punct:]',
    ]);
    expect(first('a: {b: 1}')).toEqual([
      'key:a',
      'punct::',
      'punct:{',
      'key:b',
      'punct::',
      'number:1',
      'punct:}',
    ]);
  });

  it('colours the lines of a block scalar as a string', () => {
    const lines = tokenizeYaml('run: |\n  echo 1\n  # not a comment\nnext: 2');
    expect(kinds(lines[0]!)).toEqual(['key:run', 'punct::', 'punct:|']);
    expect(kinds(lines[1]!)).toEqual(['string:echo 1']);
    expect(kinds(lines[2]!)).toEqual(['string:# not a comment']);
    expect(lines[3]!.key).toBe('next');
  });

  it('ends a block scalar of a list item at the next key of the item', () => {
    const lines = tokenizeYaml('- run: |\n    echo\n  name: x\n- |\n text');
    expect(kinds(lines[1]!)).toEqual(['string:echo']);
    expect(lines[2]!.key).toBe('name');
    expect(kinds(lines[2]!)).toEqual(['key:name', 'punct::', 'string:x']);
    // The text of a block item may sit one column right of the marker.
    expect(kinds(lines[4]!)).toEqual(['string:text']);
  });

  it('colours anchors and document markers', () => {
    expect(first('a: &x 1')).toContain('variable:&x');
    expect(first('---')).toEqual(['punct:---']);
  });

  it('returns one line for each line of the source', () => {
    expect(tokenizeYaml('a: 1\nb: 2\n')).toHaveLength(3);
    expect(tokenizeYaml('')).toHaveLength(1);
  });

  // The text of the tokens must join to the input for any input.
  it('joins back to the source for any input', () => {
    const samples = [
      'action: http.request\nwith:\n  method: GET\n  url: ""\n',
      'a: "unclosed',
      "a: 'unclosed",
      'a: {b: [1, {c: d}], "e": f}',
      '- - nested\n  - list',
      'key:\tvalue',
      '  indented: "x" # c',
      ': stray',
      '{[}]',
      'a: |-\n  x\n\n  y\nb: >\n  z',
      'emoji: "日本語 ✓"',
    ];
    for (const source of samples) {
      const joined = tokenizeYaml(source)
        .map((line) => line.tokens.map((t) => t.text).join(''))
        .join('\n');
      expect(joined).toBe(source);
    }
  });
});

describe('tokenizeYamlValue', () => {
  it('tokenizes one value', () => {
    expect(tokenizeYamlValue('"a b"')).toEqual([
      { kind: 'string', text: '"a b"' },
    ]);
    expect(tokenizeYamlValue('8080')).toEqual([
      { kind: 'number', text: '8080' },
    ]);
    expect(tokenizeYamlValue('')).toEqual([]);
  });
});
