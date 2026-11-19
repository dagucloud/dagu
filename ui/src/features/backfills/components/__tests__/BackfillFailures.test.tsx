// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import {
  BackfillOnFailure,
  BackfillRerun,
  BackfillStatus,
} from '@/api/v1/schema';
import type { Backfill } from '../../lib/backfillState';
import { BackfillFailures } from '../BackfillFailures';

const backfill: Backfill = {
  id: 'bf123456',
  dag: 'etl',
  status: BackfillStatus.failed,
  submittedAt: '2026-11-01T00:00:00Z',
  rerun: BackfillRerun.none,
  maxConcurrency: 1,
  onFailure: BackfillOnFailure.continue,
  slotCount: 3,
  counts: { failed: 3 },
};

describe('BackfillFailures', () => {
  // Failed slots are grouped by how their runs failed, each group linked to
  // the run of its earliest slot.
  it('lists failures grouped by step and error', () => {
    render(
      <MemoryRouter>
        <BackfillFailures
          remoteNode="edge"
          backfill={{
            ...backfill,
            failures: [
              {
                step: 'load',
                error: 'connection refused',
                slots: 2,
                slot: '2026-10-02T02:00:00Z',
                runId: 'run-2',
              },
              {
                error: 'disk full',
                slots: 1,
                slot: '2026-10-01T02:00:00Z',
                runId: 'run-1',
              },
            ],
          }}
        />
      </MemoryRouter>
    );

    const groups = within(
      screen.getByRole('region', { name: 'Failures' })
    ).getAllByRole('listitem');
    expect(groups).toHaveLength(2);
    expect(groups[0]).toHaveTextContent('2 slots');
    expect(groups[0]).toHaveTextContent('load');
    expect(groups[0]).toHaveTextContent('connection refused');
    expect(screen.getAllByRole('link')[0]).toHaveAttribute(
      'href',
      '/dag-runs/etl/run-2?remoteNode=edge'
    );
    expect(groups[1]).toHaveTextContent('1 slot');
  });

  it('shows nothing without failures', () => {
    const { container } = render(
      <MemoryRouter>
        <BackfillFailures backfill={backfill} remoteNode="edge" />
      </MemoryRouter>
    );
    expect(container).toBeEmptyDOMElement();
  });
});
