// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  JSON_PARSE_LIMIT,
  JsonValue,
  jsonPieces,
  tryPrettyJson,
} from '../JsonValue';

describe('tryPrettyJson', () => {
  it('indents an object with two spaces, as JSON.stringify does', () => {
    const value = '{"orders":42,"ok":true,"none":null,"name":"a b"}';
    const result = tryPrettyJson(value);
    expect(result.isJson).toBe(true);
    expect(result.pretty).toBe(JSON.stringify(JSON.parse(value), null, 2));
  });

  it('indents an array and nested containers', () => {
    const value = '[{"a":[1,2,{"b":{}}],"c":[]},"x"]';
    const result = tryPrettyJson(value);
    expect(result.isJson).toBe(true);
    expect(result.pretty).toBe(JSON.stringify(JSON.parse(value), null, 2));
  });

  it('keeps colons and commas inside strings', () => {
    const value = '{"k:1":"a,b: {c} \\"q\\""}';
    expect(tryPrettyJson(value).pretty).toBe(
      JSON.stringify(JSON.parse(value), null, 2)
    );
  });

  it('keeps all the digits of a long integer', () => {
    expect(tryPrettyJson('{"id":12345678901234567890}').pretty).toBe(
      '{\n  "id": 12345678901234567890\n}'
    );
  });

  it('gives the same text for a text that is already pretty', () => {
    const pretty = '{\n  "a": 1,\n  "b": [\n    2\n  ]\n}';
    expect(tryPrettyJson(pretty)).toEqual({
      pretty,
      isJson: true,
      tooLarge: false,
    });
  });

  it('does not expand a scalar, a bare string or a numeric string', () => {
    for (const value of ['42', '"abc"', 'true', 'null', 'plain']) {
      expect(tryPrettyJson(value)).toEqual({
        pretty: value,
        isJson: false,
        tooLarge: false,
      });
      expect(tryPrettyJson(value, { decoded: true }).isJson).toBe(false);
    }
  });

  it('keeps an invalid JSON text raw', () => {
    for (const value of ['{"a": 1', '[1,]', '{a:1}', '']) {
      expect(tryPrettyJson(value, { decoded: true })).toEqual({
        pretty: value,
        isJson: false,
        tooLarge: false,
      });
    }
  });

  it('parses a text of more than 200 KB never', () => {
    const big = '[' + '1,'.repeat(JSON_PARSE_LIMIT) + '1]';
    const result = tryPrettyJson(big);
    expect(result).toEqual({ pretty: big, isJson: false, tooLarge: true });
  });

  // The indent grows with the depth: a short, deep value would expand to
  // millions of characters.
  it('keeps a value raw when its prettified text would be too long', () => {
    const deep = '['.repeat(2000) + '0' + ']'.repeat(2000);
    const result = tryPrettyJson(deep);
    expect(result).toEqual({ pretty: deep, isJson: false, tooLarge: true });
  });
});

describe('jsonPieces', () => {
  it('colours keys, strings, numbers and keywords apart', () => {
    const kinds = jsonPieces('{\n  "a": "x",\n  "b": 1,\n  "c": null\n}')
      .filter((p) => p.kind !== 'plain' && p.kind !== 'punct')
      .map((p) => `${p.kind}:${p.text}`);
    expect(kinds).toEqual([
      'key:"a"',
      'string:"x"',
      'key:"b"',
      'number:1',
      'key:"c"',
      'keyword:null',
    ]);
  });
});

describe('JsonValue', () => {
  afterEach(() => vi.restoreAllMocks());

  const clipboard = () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    });
    return writeText;
  };
  const text = () => screen.getByTestId('json-value').querySelector('pre')!;

  it('shows a JSON object prettified', () => {
    render(<JsonValue value='{"orders":42}' />);
    expect(text().textContent).toBe('{\n  "orders": 42\n}');
    expect(screen.getByTestId('json-value')).toHaveAttribute(
      'data-json',
      'true'
    );
  });

  it('copies the prettified text', () => {
    const writeText = clipboard();
    render(<JsonValue value='{"orders":42}' />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy value' }));
    expect(writeText).toHaveBeenCalledWith('{\n  "orders": 42\n}');
  });

  // The copy control shows its result: a Check, with the copied label.
  it('swaps the copy button to a Check after a copy', async () => {
    clipboard();
    render(<JsonValue value='{"orders":42}' />);
    fireEvent.click(screen.getByRole('button', { name: 'Copy value' }));
    expect(
      await screen.findByRole('button', { name: 'Copied' })
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Copy value' })).toBeNull();
    await waitFor(
      () =>
        expect(
          screen.getByRole('button', { name: 'Copy value' })
        ).toBeInTheDocument(),
      { timeout: 3000 }
    );
  });

  it('shows the original single-line value with the Raw button', () => {
    render(<JsonValue value='{"orders":42}' />);
    const raw = screen.getByRole('button', { name: 'Raw' });
    expect(raw).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(raw);
    expect(text().textContent).toBe('{"orders":42}');
    expect(raw).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(raw);
    expect(text().textContent).toBe('{\n  "orders": 42\n}');
  });

  // The buttons float on the top right corner of the box of the value, and
  // the text keeps room on its right so that it never goes under them.
  it('puts the Raw and Copy buttons on the top right corner of the box', () => {
    render(<JsonValue value='{"orders":42}' />);
    const box = screen.getByTestId('json-value-box');
    const corner = screen.getByTestId('json-value-corner');
    expect(box).toHaveClass('relative');
    expect(corner).toHaveClass('absolute', 'top-1.5', 'right-1.5');
    expect(box).toContainElement(corner);
    expect(corner).toContainElement(
      screen.getByRole('button', { name: 'Raw' })
    );
    expect(corner).toContainElement(
      screen.getByRole('button', { name: 'Copy value' })
    );
    // Not in the text, and not below it.
    expect(text()).not.toContainElement(corner);
    expect(text().className).toContain('pr-16');
    // Raw comes before Copy, in the same group.
    const names = within(corner)
      .getAllByRole('button')
      .map((b) => b.getAttribute('aria-label') ?? b.textContent);
    expect(names).toEqual(['Raw', 'Copy value']);
  });

  it('puts the Copy button alone on the corner of a scalar', () => {
    render(<JsonValue value="42" />);
    const corner = screen.getByTestId('json-value-corner');
    expect(within(corner).getAllByRole('button')).toHaveLength(1);
    expect(text().className).toContain('pr-8');
  });

  it('keeps the corner in place when the value is raw or unfolded', () => {
    const value = JSON.stringify(Array.from({ length: 30 }, (_, i) => i));
    render(<JsonValue value={value} />);
    fireEvent.click(screen.getByRole('button', { name: 'Show more' }));
    fireEvent.click(screen.getByRole('button', { name: 'Raw' }));
    expect(screen.getByTestId('json-value-corner')).toHaveClass(
      'absolute',
      'top-1.5',
      'right-1.5'
    );
  });

  it('shows a scalar as it is, with no Raw button', () => {
    const writeText = clipboard();
    render(<JsonValue value="42" />);
    expect(text().textContent).toBe('42');
    expect(screen.queryByRole('button', { name: 'Raw' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Copy value' }));
    expect(writeText).toHaveBeenCalledWith('42');
  });

  it('folds a long value and opens it with Show more', () => {
    const value = JSON.stringify(Array.from({ length: 30 }, (_, i) => i));
    render(<JsonValue value={value} />);
    const more = screen.getByRole('button', { name: 'Show more' });
    expect(text().className).toContain('max-h-24');
    fireEvent.click(more);
    expect(text().className).toContain('max-h-64');
    expect(text().className).toContain('overflow-auto');
    expect(screen.getByRole('button', { name: 'Show less' })).toBeVisible();
  });

  it('says that a value of more than 200 KB is not formatted', () => {
    const big = '[' + '1,'.repeat(JSON_PARSE_LIMIT) + '1]';
    render(<JsonValue value={big} />);
    expect(
      screen.getByText('Too large to format, shown as it is')
    ).toBeVisible();
    expect(screen.getByTestId('json-value')).toHaveAttribute(
      'data-json',
      'false'
    );
  });
});
