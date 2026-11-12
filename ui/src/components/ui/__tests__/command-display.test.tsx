// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { CommandDisplay } from '../command-display';

describe('CommandDisplay', () => {
  it('gives assistive tech the full command when the text is shortened', () => {
    const command = 'rsync -avz --delete ./build/ deploy@example.com:/srv/app';
    render(
      <CommandDisplay commands={[{ command, args: [] }]} maxLength={20} />
    );

    expect(screen.getByText(command)).toHaveClass('sr-only');
  });

  // The table cell can clip even a short command, so its tooltip must be
  // reachable from the keyboard.
  it('makes the tooltip trigger focusable for short commands', () => {
    const { container } = render(
      <CommandDisplay commands={[{ command: 'make', args: ['test'] }]} />
    );

    expect(container.querySelector('[tabindex="0"]')).toHaveTextContent(
      'make test'
    );
  });
});
