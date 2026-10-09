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

const description = 'Loads nightly sales into the warehouse.';

function renderHeader(currentDAGRun?: components['schemas']['DAGRunDetails']) {
  return render(
    <MemoryRouter>
      <DAGHeader
        dag={{ name: 'etl', description }}
        currentDAGRun={currentDAGRun}
        fileName="etl"
        refreshFn={vi.fn()}
        formatDuration={() => '--'}
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
});
