// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { WorkflowsModeToggle } from '../WorkflowsModeToggle';

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location">{location.pathname}</div>;
}

function renderToggle(mode: 'workflows' | 'runs', path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <WorkflowsModeToggle mode={mode} />
      <LocationProbe />
    </MemoryRouter>
  );
}

describe('WorkflowsModeToggle', () => {
  it('goes to the runs page from the workflows mode', () => {
    renderToggle('workflows', '/dags');

    fireEvent.click(screen.getByRole('button', { name: 'Runs' }));

    expect(screen.getByTestId('location')).toHaveTextContent('/dag-runs');
  });

  it('goes to the workflows page from the runs mode', () => {
    renderToggle('runs', '/dag-runs');

    fireEvent.click(screen.getByRole('button', { name: 'Workflows' }));

    expect(screen.getByTestId('location')).toHaveTextContent('/dags');
  });

  it('stays on the page when the current mode is clicked', () => {
    renderToggle('runs', '/dag-runs');

    fireEvent.click(screen.getByRole('button', { name: 'Runs' }));

    expect(screen.getByTestId('location')).toHaveTextContent('/dag-runs');
  });
});
