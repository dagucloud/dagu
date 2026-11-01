// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { SimplifiedToggle } from '../SimplifiedToggle';

describe('SimplifiedToggle', () => {
  it('is an icon button named Simplified view that hides the details when pressed', async () => {
    const onChange = vi.fn();
    render(
      <SimplifiedToggle simplified={false} onSimplifiedChange={onChange} />
    );
    const button = screen.getByRole('button', { name: 'Simplified view' });
    expect(button).toHaveAttribute('aria-pressed', 'false');
    expect(button).toHaveAttribute('title', 'Simplified view');
    expect(button).toHaveTextContent('');
    expect(button.querySelector('svg')).not.toBeNull();
    await userEvent.click(button);
    expect(onChange).toHaveBeenCalledWith(true);
  });

  it('shows the button pressed and highlighted when the cards are simplified', async () => {
    const onChange = vi.fn();
    render(<SimplifiedToggle simplified onSimplifiedChange={onChange} />);
    const button = screen.getByRole('button', { name: 'Simplified view' });
    expect(button).toHaveAttribute('aria-pressed', 'true');
    expect(button.className).toContain('bg-muted');
    await userEvent.click(button);
    expect(onChange).toHaveBeenCalledWith(false);
  });
});
