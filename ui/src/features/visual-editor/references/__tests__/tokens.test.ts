// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  classifyReferencePath,
  findOpenReference,
  findReferenceTokens,
  insertReference,
  isEscapedDollar,
  referenceTokenAt,
} from '../tokens';

describe('findReferenceTokens', () => {
  it('returns the range, text and path of a token', () => {
    const text = 'echo ${params.x} done';
    expect(findReferenceTokens(text)).toEqual([
      {
        from: 5,
        to: 16,
        text: '${params.x}',
        path: 'params.x',
        kind: 'canonical',
        namespace: 'params',
      },
    ]);
  });

  it('finds adjacent tokens', () => {
    const tokens = findReferenceTokens('${a}${b.c}');
    expect(tokens.map((t) => [t.from, t.to, t.path])).toEqual([
      [0, 4, 'a'],
      [4, 10, 'b.c'],
    ]);
  });

  it('finds a token inside JSON-like braces', () => {
    const text = '{"k": "${params.x}", "n": {"m": "${env.HOME}"}}';
    expect(findReferenceTokens(text).map((t) => t.text)).toEqual([
      '${params.x}',
      '${env.HOME}',
    ]);
  });

  it('skips a "${" without a closing brace', () => {
    expect(findReferenceTokens('echo ${params.x')).toEqual([]);
    expect(findReferenceTokens('${a ${b}').map((t) => t.text)).toEqual([
      '${b}',
    ]);
    expect(findReferenceTokens('${a${b}').map((t) => t.text)).toEqual(['${b}']);
  });

  it('does not let a token cross a line break or a brace', () => {
    expect(findReferenceTokens('${a\nb}')).toEqual([]);
    expect(findReferenceTokens('${a{b}')).toEqual([]);
  });

  it('keeps an empty token and ignores a lone dollar', () => {
    expect(findReferenceTokens('${}')[0]?.path).toBe('');
    expect(findReferenceTokens('$x {y} $')).toEqual([]);
  });
});

describe('escaped references', () => {
  it('skips a token after an odd run of backslashes', () => {
    expect(findReferenceTokens('\\${x}')).toEqual([]);
    expect(findReferenceTokens('\\\\\\${x}')).toEqual([]);
  });

  it('keeps a token after an even run of backslashes', () => {
    expect(findReferenceTokens('\\\\${x}')).toEqual([
      { from: 2, to: 6, text: '${x}', path: 'x', kind: 'variable' },
    ]);
    expect(
      findReferenceTokens('${x}\\${y}\\\\${z}').map((t) => t.path)
    ).toEqual(['x', 'z']);
  });

  it('does not let a backslash after the dollar matter', () => {
    expect(findReferenceTokens('$\\{x}')).toEqual([]);
    expect(findReferenceTokens('${a\\b}')).toHaveLength(1);
  });

  it('tells whether a dollar is escaped', () => {
    expect(isEscapedDollar('${x}', 0)).toBe(false);
    expect(isEscapedDollar('\\${x}', 1)).toBe(true);
    expect(isEscapedDollar('\\\\${x}', 2)).toBe(false);
    expect(isEscapedDollar('a\\\\\\$', 4)).toBe(true);
  });
});

describe('classifyReferencePath', () => {
  const canonical = (namespace: string) => ({ kind: 'canonical', namespace });

  it('accepts the canonical namespaces', () => {
    expect(classifyReferencePath('params.region')).toEqual(canonical('params'));
    expect(classifyReferencePath('consts.Max_1')).toEqual(canonical('consts'));
    expect(classifyReferencePath('env.HOME')).toEqual(canonical('env'));
    expect(classifyReferencePath('env._X')).toEqual(canonical('env'));
    expect(classifyReferencePath('steps.fetch.outputs.body')).toEqual(
      canonical('steps')
    );
    expect(classifyReferencePath('context.run.id')).toEqual(
      canonical('context')
    );
    expect(classifyReferencePath('foreach.index')).toEqual(
      canonical('foreach')
    );
    expect(classifyReferencePath('foreach.key')).toEqual(canonical('foreach'));
    expect(classifyReferencePath('foreach.item')).toEqual(canonical('foreach'));
    expect(classifyReferencePath('foreach.item.name')).toEqual(
      canonical('foreach')
    );
  });

  // A field of an item is a data key, such as a workbook header in any script.
  it('accepts a foreach item field in any script', () => {
    for (const path of [
      'foreach.row._row',
      'foreach.row.請求番号',
      'foreach.row.Straße',
      'foreach.row.列2',
    ]) {
      expect(classifyReferencePath(path), path).toEqual(canonical('foreach'));
    }
    expect(classifyReferencePath('foreach.row.2列')).toEqual({
      kind: 'unsupported',
    });
  });

  it('classifies a single env-style name as a variable', () => {
    expect(classifyReferencePath('NAME')).toEqual({ kind: 'variable' });
    expect(classifyReferencePath('_x1')).toEqual({ kind: 'variable' });
    expect(classifyReferencePath('params')).toEqual({ kind: 'variable' });
  });

  it('classifies older dotted forms as legacy', () => {
    for (const path of [
      'build.stdout',
      'build.stderr',
      'build.exitCode',
      'build.outputs.tag',
      'RESULT.data.items.name',
      'RESULT.items[0]',
    ]) {
      expect(classifyReferencePath(path), path).toEqual({ kind: 'legacy' });
    }
  });

  it('marks malformed or unknown text as unsupported', () => {
    for (const path of [
      '',
      'steps.a.outputs.b.c',
      'steps.a.bar',
      'steps.a-b.outputs.c',
      'steps.1a.outputs.c',
      'steps.a.outputs',
      'params.1x',
      'params.a.b',
      'consts.',
      'env.1X',
      'env.A.B',
      'context.run',
      'context.run.id.x',
      'foreach.a.b.c',
      'foreach.',
      '1a.b',
      'a..b',
      'a.b.',
      'a b.c',
      'NAME:-x',
      'a-b',
    ]) {
      expect(classifyReferencePath(path), path).toEqual({
        kind: 'unsupported',
      });
    }
  });

  it('puts the classification on found tokens', () => {
    const tokens = findReferenceTokens(
      '${env.HOME} ${PATH} ${a.stdout} ${steps.a.bar}'
    );
    expect(tokens.map((t) => [t.kind, t.namespace])).toEqual([
      ['canonical', 'env'],
      ['variable', undefined],
      ['legacy', undefined],
      ['unsupported', undefined],
    ]);
  });
});

describe('findOpenReference', () => {
  const at = (text: string) => findOpenReference(text, text.length);

  it('finds an open "${" with its query', () => {
    expect(at('echo ${')).toEqual({ from: 5, query: '' });
    expect(at('echo ${par')).toEqual({ from: 5, query: 'par' });
    expect(at('${steps.fetch.out')).toEqual({
      from: 0,
      query: 'steps.fetch.out',
    });
  });

  it('finds a lone dollar before the caret', () => {
    expect(at('echo $')).toEqual({ from: 5, query: '' });
    expect(at('$$')).toEqual({ from: 1, query: '' });
  });

  it('uses the text before the caret only', () => {
    expect(findOpenReference('a ${par} b', 7)).toEqual({
      from: 2,
      query: 'par',
    });
    expect(findOpenReference('a ${par} b', 3)).toEqual({ from: 2, query: '' });
    expect(findOpenReference('a ${par} b', 2)).toBeNull();
    expect(findOpenReference('a ${par} b', 9)).toBeNull();
    expect(findOpenReference('a ${x', 0)).toBeNull();
  });

  it('ignores a reference that is closed before the caret', () => {
    expect(at('${a} ')).toBeNull();
    expect(at('${a}')).toBeNull();
    expect(at('${a} ${b')).toEqual({ from: 5, query: 'b' });
    expect(at('${a} $')).toEqual({ from: 5, query: '' });
  });

  it('ignores an escaped dollar', () => {
    expect(at('\\${par')).toBeNull();
    expect(at('\\$')).toBeNull();
    expect(at('\\\\${par')).toEqual({ from: 2, query: 'par' });
    expect(at('\\\\$')).toEqual({ from: 2, query: '' });
  });

  it('does not look back over a line break', () => {
    expect(at('${a\nb')).toBeNull();
    expect(at('${\n')).toBeNull();
    expect(at('x\n$')).toEqual({ from: 2, query: '' });
    expect(at('${a\n${b')).toEqual({ from: 4, query: 'b' });
  });

  it('keeps the query of a foreach item field in any script', () => {
    expect(at('${foreach.row.請求')).toEqual({
      from: 0,
      query: 'foreach.row.請求',
    });
  });

  it('rejects characters that cannot be in a reference name', () => {
    expect(at('${a b')).toBeNull();
    expect(at('${a-b')).toBeNull();
    expect(at('${a{b')).toBeNull();
    expect(at('${a:-b')).toBeNull();
  });
});

describe('insertReference', () => {
  it('replaces the range and puts the caret after the token', () => {
    expect(insertReference('echo ${par done', 5, 10, '${params.x}')).toEqual({
      text: 'echo ${params.x} done',
      caret: 16,
    });
  });

  it('inserts at an empty range', () => {
    expect(insertReference('ab', 1, 1, '${x}')).toEqual({
      text: 'a${x}b',
      caret: 5,
    });
  });
});

describe('referenceTokenAt', () => {
  const text = 'a ${x} ${y.z}';

  it('returns the token that contains the offset', () => {
    expect(referenceTokenAt(text, 2)?.text).toBe('${x}');
    expect(referenceTokenAt(text, 5)?.text).toBe('${x}');
    expect(referenceTokenAt(text, 7)?.text).toBe('${y.z}');
  });

  it('excludes the end offset and offsets outside tokens', () => {
    expect(referenceTokenAt(text, 6)).toBeNull();
    expect(referenceTokenAt(text, 1)).toBeNull();
    expect(referenceTokenAt(text, 13)).toBeNull();
  });
});
