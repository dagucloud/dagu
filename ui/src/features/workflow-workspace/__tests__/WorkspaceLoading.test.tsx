// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { WorkspaceLoading } from '../WorkspaceLoading';

// Until the data is there, the first row is the bar with the name of the
// workflow and the loader is in the middle: nothing moves when the page loads.
describe('WorkspaceLoading', () => {
  it('shows the name in a bar of the height of the real bar, and the loader below', () => {
    render(<WorkspaceLoading fileName="graph-etl" />);
    const frame = screen.getByRole('status', { name: 'Loading...' });
    const name = screen.getByRole('heading', { name: 'graph-etl' });
    expect(name).toHaveClass('text-base', 'font-semibold');
    expect(name.parentElement).toHaveClass('h-10', 'border-b', 'px-3');
    // The loader sits in the middle of the rest of the area.
    const body = frame.lastElementChild;
    expect(body).toHaveClass('flex-1', 'items-center', 'justify-center');
  });

  it('fills its area, with no padding of its own', () => {
    render(<WorkspaceLoading fileName="etl" />);
    const frame = screen.getByRole('status', { name: 'Loading...' });
    expect(frame).toHaveClass('h-full', 'w-full');
    expect(frame).not.toHaveClass('p-4');
  });
});
