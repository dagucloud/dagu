// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import {
  BackfillOnFailure,
  BackfillRerun,
  BackfillSlotState,
  BackfillStatus,
} from '@/api/v1/schema';
import { BackfillProgress } from '../BackfillProgress';

const backfill = {
  id: 'bf123456',
  dag: 'etl',
  status: BackfillStatus.running,
  submittedAt: '2026-11-01T00:00:00Z',
  rerun: BackfillRerun.none,
  maxConcurrency: 1,
  onFailure: BackfillOnFailure.stop,
  slotCount: 3,
  counts: { succeeded: 2, running: 1 },
};

describe('BackfillProgress', () => {
  // The state that the slot list shows can run out as the backfill goes on;
  // its button stays, so that the filter can be cleared.
  it('keeps the selected state in the legend at zero', () => {
    const onSelect = vi.fn();
    render(
      <BackfillProgress
        backfill={backfill}
        selected={BackfillSlotState.pending}
        onSelect={onSelect}
      />
    );

    const pending = screen.getByRole('button', { name: /0\s*Pending/ });
    expect(pending).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(pending);
    expect(onSelect).toHaveBeenCalledWith(null);
  });
});
