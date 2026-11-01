// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { YamlSnippet } from '../YamlSnippet';

const source = 'action: http.request\nwith:\n  method: GET\n  url: ""\n';

describe('YamlSnippet', () => {
  it('shows exactly the source text', () => {
    render(<YamlSnippet data-testid="y" source={source} required={['url']} />);
    expect(screen.getByTestId('y').textContent).toBe(source);
  });

  it('colours keys and strings with light and dark classes', () => {
    render(<YamlSnippet data-testid="y" source={'a: "x"\nb: 1\nc: true'} />);
    const pre = screen.getByTestId('y');
    const byText = (text: string) =>
      Array.from(pre.querySelectorAll('span')).find(
        (span) => span.children.length === 0 && span.textContent === text
      )!;
    // The tokens of the Monaco themes: key amber, string green, number
    // violet, keyword amber.
    expect(byText('a').className).toContain('text-[#9a5d0b]');
    expect(byText('a').className).toContain('dark:text-[#e2a566]');
    expect(byText('"x"').className).toContain('text-[#14753a]');
    expect(byText('"x"').className).toContain('dark:text-[#8fd9a8]');
    expect(byText('1').className).toContain('text-[#7c3aed]');
    expect(byText('true').className).toContain('text-[#9a5d0b]');
  });

  it('marks the lines of the required keys', () => {
    render(
      <YamlSnippet
        data-testid="y"
        source={source}
        required={['method', 'url']}
        requiredLabel="Required key"
      />
    );
    const marked = Array.from(
      screen.getByTestId('y').querySelectorAll('[data-required="true"]')
    );
    expect(marked.map((m) => m.textContent)).toEqual([
      '  method: GET',
      '  url: ""',
    ]);
    for (const line of marked) {
      expect(line).toHaveAttribute('title', 'Required key');
      // The star comes from CSS: it adds no text to copy.
      expect(line.className).toContain("after:content-['*']");
    }
  });

  it('marks no line without required keys', () => {
    render(<YamlSnippet data-testid="y" source={source} />);
    expect(screen.getByTestId('y').querySelector('[data-required]')).toBeNull();
  });
});
