// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { Kbd } from '@/components/ui/kbd';

describe('Kbd', () => {
  it('renders a kbd element with the key text and the shared box', () => {
    render(<Kbd className="ml-4">Ctrl/Cmd+C</Kbd>);
    const key = screen.getByText('Ctrl/Cmd+C');
    expect(key.tagName).toBe('KBD');
    expect(key).toHaveClass('rounded', 'border', 'bg-muted', 'ml-4');
  });
});
