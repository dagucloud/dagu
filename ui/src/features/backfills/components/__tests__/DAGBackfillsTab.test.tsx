// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { DAGBackfillsTab } from '../DAGBackfillsTab';

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({
    data: undefined,
    error: { message: 'backfills unavailable' },
    mutate: vi.fn(),
  }),
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));
vi.mock('../DAGSlotsView', () => ({
  DAGSlotsView: () => <section aria-label="Slots" />,
}));

describe('DAGBackfillsTab', () => {
  it('offers the slots when the backfills fail to load', () => {
    render(
      <MemoryRouter>
        <DAGBackfillsTab dagName="etl" fileName="etl" canExecute />
      </MemoryRouter>
    );

    expect(screen.getByRole('region', { name: 'Slots' })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent(
      'backfills unavailable'
    );
  });
});
