// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { components } from '@/api/v1/schema';
import DAGHeader from '../DAGHeader';

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ basePath: '' }),
}));

vi.mock('../../common', () => ({ DAGActions: () => null }));

vi.mock('../DAGPinButton', () => ({
  default: ({ pinned }: { pinned: boolean }) => (
    <span data-testid="pin-button">{pinned ? 'pinned' : 'not pinned'}</span>
  ),
}));

const description = 'Loads nightly sales into the warehouse.';

function renderHeader(
  currentDAGRun?: components['schemas']['DAGRunDetails'],
  pinned?: boolean
) {
  return render(
    <MemoryRouter>
      <DAGHeader
        dag={{ name: 'etl', description }}
        currentDAGRun={currentDAGRun}
        fileName="etl"
        refreshFn={vi.fn()}
        formatDuration={() => '--'}
        pinned={pinned}
      />
    </MemoryRouter>
  );
}

describe('DAGHeader', () => {
  it('shows the DAG description under its name', () => {
    renderHeader();

    expect(screen.getByText(description)).toHaveAttribute('title', description);
  });

  it('does not describe a sub DAG run with the parent description', () => {
    renderHeader({
      name: 'load-partition',
    } as components['schemas']['DAGRunDetails']);

    expect(
      screen.getByRole('heading', { name: 'load-partition' })
    ).toBeInTheDocument();
    expect(screen.queryByText(description)).not.toBeInTheDocument();
  });

  it('shows whether the DAG is pinned', () => {
    renderHeader(undefined, true);

    expect(screen.getByTestId('pin-button')).toHaveTextContent('pinned');
  });

  it('offers no pin button for a sub DAG run', () => {
    renderHeader(
      { name: 'load-partition' } as components['schemas']['DAGRunDetails'],
      true
    );

    expect(screen.queryByTestId('pin-button')).not.toBeInTheDocument();
  });

  // Remote nodes running an older version do not report pins.
  it('offers no pin button when the pin state is unknown', () => {
    renderHeader();

    expect(screen.queryByTestId('pin-button')).not.toBeInTheDocument();
  });
});
