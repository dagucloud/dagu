// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, renderHook, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import dayjs from '@/lib/dayjs';
import type { ApiDAGRunDetails } from '../../contracts';
import { useTestRunNotice } from '../useTestRunNotice';

const details = (extra: Partial<ApiDAGRunDetails>) =>
  ({ dagRunId: 'run-1', ...extra }) as ApiDAGRunDetails;

function notice(run: ApiDAGRunDetails | null) {
  return renderHook(() => useTestRunNotice(run)).result.current;
}

describe('useTestRunNotice', () => {
  it('is absent for a run that stays and for no run', () => {
    expect(notice(details({}))).toBeNull();
    expect(notice(null)).toBeNull();
  });

  it('is an info notice with a flask for a test run', () => {
    const found = notice(details({ ephemeral: true }));
    expect(found).toMatchObject({ id: 'test-run', tone: 'info' });
    render(<>{found?.message}</>);
    expect(document.querySelector('svg.lucide-flask-conical')).not.toBeNull();
  });

  it('says that it disappears ten minutes after it ends while it runs', () => {
    render(
      <>{notice(details({ ephemeral: true, finishedAt: '-' }))?.message}</>
    );
    expect(
      screen.getByText(
        'Test run, not kept: it disappears 10 minutes after it ends'
      )
    ).toBeInTheDocument();
  });

  it('gives the time at which an ended test run disappears', () => {
    const finishedAt = '2026-10-26T10:00:00Z';
    render(<>{notice(details({ ephemeral: true, finishedAt }))?.message}</>);
    const at = dayjs(Date.parse('2026-10-26T10:10:00Z')).format('HH:mm:ss');
    expect(
      screen.getByText(`Test run, not kept: it disappears at ${at}`)
    ).toBeInTheDocument();
  });

  it('reads the old label of a run that has no flag', () => {
    expect(notice(details({ labels: ['dagu.ephemeral=true'] }))?.id).toBe(
      'test-run'
    );
  });
});
