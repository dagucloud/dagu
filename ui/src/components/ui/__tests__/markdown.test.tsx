// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { Markdown } from '../markdown';

describe('Markdown', () => {
  it('renders inline and fenced code with theme-aware contrast styles', () => {
    const { container } = render(
      <Markdown content={'Use `dagu start`.\n\n```yaml\nsteps: []\n```'} />
    );

    const pre = container.querySelector('pre');
    const blockCode = pre?.querySelector('code');
    const inlineCode = Array.from(container.querySelectorAll('code')).find(
      (code) => code.textContent === 'dagu start'
    );

    expect(pre?.getAttribute('style')).toContain(
      'background-color: var(--muted)'
    );
    expect(pre?.getAttribute('style')).toContain('color: var(--foreground)');
    expect(blockCode?.getAttribute('style')).toContain(
      'background-color: transparent'
    );
    expect(blockCode?.getAttribute('style')).toContain(
      'color: var(--foreground)'
    );
    expect(inlineCode?.getAttribute('style')).toContain(
      'background-color: var(--muted)'
    );
    expect(inlineCode?.getAttribute('style')).toContain(
      'color: var(--foreground)'
    );
  });
});
