// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Badge } from '../badge';

describe('Badge', () => {
  it('has the default size', () => {
    render(<Badge>Default</Badge>);
    expect(screen.getByText('Default')).toHaveClass('h-5', 'px-1.5');
  });

  it('has a small size for dense lists', () => {
    render(<Badge size="sm">Small</Badge>);
    const badge = screen.getByText('Small');
    expect(badge).toHaveClass('h-4', 'px-1', 'text-[10px]', 'leading-4');
    expect(badge).not.toHaveClass('h-5');
    expect(badge).not.toHaveClass('px-1.5');
    expect(badge).not.toHaveClass('text-[11px]');
  });
});
