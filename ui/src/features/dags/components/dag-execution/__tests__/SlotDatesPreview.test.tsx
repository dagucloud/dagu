// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useQuery } from '@/hooks/api';
import { SlotDatesPreview } from '../SlotDatesPreview';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

describe('SlotDatesPreview', () => {
  beforeEach(() => useQueryMock.mockReset());

  it('shows the slot, its window, and each date', () => {
    useQueryMock.mockReturnValue({
      data: {
        scheduledAt: '2026-11-09T17:00:00Z',
        windowStart: '2026-11-08T17:00:00Z',
        defaultSlot: true,
        dates: [
          { name: 'target_day', value: '2026-11-09' },
          { name: 'window_days', list: true, values: ['2026-11-09'] },
        ],
      },
    });
    const onBlocked = vi.fn();
    render(
      <SlotDatesPreview
        fileName="etl"
        remoteNode="local"
        onBlocked={onBlocked}
      />
    );

    expect(screen.getByText(/the latest fire/)).toBeInTheDocument();
    expect(screen.getByText('target_day')).toBeInTheDocument();
    expect(screen.getAllByText('2026-11-09')).toHaveLength(2);
    expect(onBlocked).toHaveBeenLastCalledWith(null);
  });

  it('asks for a run without a profile', () => {
    useQueryMock.mockReturnValue({});
    render(
      <SlotDatesPreview
        fileName="etl"
        remoteNode="local"
        profile=""
        onBlocked={vi.fn()}
      />
    );
    expect(useQueryMock).toHaveBeenCalledWith(
      '/dags/{fileName}/slot-preview',
      {
        params: {
          path: { fileName: 'etl' },
          query: { remoteNode: 'local', profile: '' },
        },
      },
      expect.anything()
    );
  });

  it('reports dates that cannot resolve', async () => {
    useQueryMock.mockReturnValue({
      error: { message: 'dates.since needs the window of a schedule slot' },
    });
    const onBlocked = vi.fn();
    render(
      <SlotDatesPreview
        fileName="etl"
        remoteNode="local"
        scheduledAt="2026-11-09"
        onBlocked={onBlocked}
      />
    );

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'dates.since needs the window of a schedule slot'
    );
    await waitFor(() =>
      expect(onBlocked).toHaveBeenLastCalledWith(
        'dates.since needs the window of a schedule slot'
      )
    );
  });
});
