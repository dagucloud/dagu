// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { highlightPrompt } from '../promptHighlight';

const kinds = (text: string) =>
  highlightPrompt(text).map(({ kind, text: part }) => [kind, part]);

describe('highlightPrompt', () => {
  it('joins back to the text, whatever the text is', () => {
    const samples = [
      '',
      'plain',
      '# Title\n\n**bold** and `code` and ${params.a}\n- a\n  - b\n> q\n1. x',
      '```yaml\nkey: ${x}\n# not a heading\n```\nafter',
      'a ${unclosed and ${ok.x} — café שלום \u{1F600}\r\n\tend\n',
    ];
    for (const text of samples) {
      expect(
        highlightPrompt(text)
          .map((segment) => segment.text)
          .join('')
      ).toBe(text);
    }
  });

  it('marks the start of a heading, a quote and a list item', () => {
    expect(kinds('# Title')).toEqual([
      ['mark', '#'],
      ['text', ' Title'],
    ]);
    expect(kinds('> quote')).toEqual([
      ['mark', '>'],
      ['text', ' quote'],
    ]);
    expect(kinds('  - item')).toEqual([
      ['text', '  '],
      ['mark', '-'],
      ['text', ' item'],
    ]);
    expect(kinds('12. step')).toEqual([
      ['mark', '12.'],
      ['text', ' step'],
    ]);
  });

  it('does not take a dash, a colon or a number inside a line as a mark', () => {
    expect(kinds('a - b: 42 # c')).toEqual([['text', 'a - b: 42 # c']]);
  });

  it('marks inline back ticks and emphasis, and leaves their text plain', () => {
    expect(kinds('use `run` and **this**')).toEqual([
      ['text', 'use '],
      ['mark', '`'],
      ['text', 'run'],
      ['mark', '`'],
      ['text', ' and '],
      ['mark', '**'],
      ['text', 'this'],
      ['mark', '**'],
    ]);
  });

  it('finds references and keeps them whole', () => {
    const segments = highlightPrompt('x ${steps.a.outputs.b}, ${bad ref}');
    expect(segments.filter((s) => s.kind === 'ref')).toHaveLength(2);
    expect(segments.find((s) => s.kind === 'ref')?.token?.kind).toBe(
      'canonical'
    );
  });

  it('does not mark a Markdown sign inside a reference', () => {
    const segments = highlightPrompt('- ${params.a_b} `x`');
    expect(segments.map((s) => s.kind)).toEqual([
      'mark',
      'text',
      'ref',
      'text',
      'mark',
      'text',
      'mark',
    ]);
  });

  it('leaves a code fence as one mark and its lines as text', () => {
    expect(kinds('```\n# not a heading\n- not a list\n```')).toEqual([
      ['mark', '```'],
      ['text', '\n# not a heading\n- not a list\n'],
      ['mark', '```'],
    ]);
  });

  it('is fast on a very long prompt', () => {
    const text = '- line with `code`, **bold** and ${params.a}\n'.repeat(400);
    const start = performance.now();
    const segments = highlightPrompt(text);
    expect(segments.length).toBeGreaterThan(400);
    expect(performance.now() - start).toBeLessThan(500);
  });
});
