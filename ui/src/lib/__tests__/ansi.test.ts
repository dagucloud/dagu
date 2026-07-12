// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { parseAnsi, stripAnsi } from '../ansi';

const ESC = '\u001B';

describe('stripAnsi', () => {
  it('removes SGR sequences', () => {
    expect(stripAnsi(`${ESC}[31mred${ESC}[0m plain`)).toBe('red plain');
  });

  it('returns plain text unchanged', () => {
    expect(stripAnsi('no escapes here')).toBe('no escapes here');
  });
});

describe('parseAnsi', () => {
  it('returns a single unstyled segment for plain text', () => {
    expect(parseAnsi('hello')).toEqual([{ text: 'hello', style: {} }]);
  });

  it('applies a basic foreground color and resets it', () => {
    const segments = parseAnsi(`${ESC}[31mred${ESC}[0mplain`);
    expect(segments).toHaveLength(2);
    expect(segments[0]?.text).toBe('red');
    expect(segments[0]?.style.color).toBeTruthy();
    expect(segments[1]).toEqual({ text: 'plain', style: {} });
  });

  it('carries style across segments until reset', () => {
    const segments = parseAnsi(`${ESC}[1;32mbold green${ESC}[22m still green`);
    expect(segments[0]?.style.bold).toBe(true);
    expect(segments[0]?.style.color).toBeTruthy();
    expect(segments[1]?.style.bold).toBeUndefined();
    expect(segments[1]?.style.color).toBe(segments[0]?.style.color);
  });

  it('supports 256-color and truecolor sequences', () => {
    const seg256 = parseAnsi(`${ESC}[38;5;196mx`);
    expect(seg256[0]?.style.color).toBeTruthy();

    const segRgb = parseAnsi(`${ESC}[38;2;10;20;30mx`);
    expect(segRgb[0]?.style.color).toBe('rgb(10,20,30)');
  });

  it('drops non-SGR escape sequences without styling', () => {
    // Cursor-up sequence should disappear from the output text.
    const segments = parseAnsi(`before${ESC}[2Aafter`);
    expect(segments.map((s) => s.text).join('')).toBe('beforeafter');
    expect(segments.every((s) => Object.keys(s.style).length === 0)).toBe(true);
  });
});
