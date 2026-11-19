// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it } from 'vitest';
import { BackfillStatus } from '@/api/v1/schema';
import { DISMISSED_BACKFILLS_KEY } from '../../hooks/useBackfillNotice';
import { DAGBackfillChip } from '../DAGBackfillChip';

function renderChip(status: BackfillStatus, failed = 0) {
  return render(
    <MemoryRouter>
      <DAGBackfillChip
        backfill={{ id: 'bf123456', status, slots: 28, done: 12, failed }}
      />
    </MemoryRouter>
  );
}

describe('DAGBackfillChip', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('links an open backfill with its progress', () => {
    renderChip(BackfillStatus.running, 1);

    expect(
      screen.getByRole('link', { name: 'Backfill 12/28, 1 failed' })
    ).toHaveAttribute('href', '/backfills/bf123456?remoteNode=local');
    expect(screen.queryByRole('button', { name: 'Dismiss' })).toBeNull();
  });

  // The workflow page's notice shares the dismissal, so dismissing either
  // hides both.
  it('hides a failed backfill once dismissed', () => {
    const { unmount } = renderChip(BackfillStatus.failed, 3);
    expect(
      screen.getByRole('link', { name: 'Backfill failed: 3 slots' })
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(screen.queryByRole('link')).toBeNull();
    expect(localStorage.getItem(DISMISSED_BACKFILLS_KEY)).toContain('bf123456');

    unmount();
    renderChip(BackfillStatus.failed, 3);
    expect(screen.queryByRole('link')).toBeNull();
  });

  // The DAG list keeps its card and table views mounted, each with a chip.
  it('hides every mounted chip of a dismissed backfill', () => {
    const backfill = {
      id: 'bf123456',
      status: BackfillStatus.failed,
      slots: 28,
      done: 12,
      failed: 3,
    };
    render(
      <MemoryRouter>
        <DAGBackfillChip backfill={backfill} />
        <DAGBackfillChip backfill={backfill} />
      </MemoryRouter>
    );

    fireEvent.click(screen.getAllByRole('button', { name: 'Dismiss' })[0]!);
    expect(screen.queryAllByRole('link')).toHaveLength(0);
  });
});
