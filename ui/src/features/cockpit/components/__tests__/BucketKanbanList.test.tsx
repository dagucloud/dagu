// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import dayjs from '@/lib/dayjs';
import type { KanbanFilters } from '../../hooks/useDateKanbanData';
import { BucketKanbanList } from '../BucketKanbanList';

type CapturedProps = {
  fromStr: string;
  toStr: string;
  isLive: boolean;
  filters?: KanbanFilters;
  onCardClick: (run: unknown, event?: { ctrlKey?: boolean }) => void;
};
const sectionProps: CapturedProps[] = [];

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tzOffsetInSec: 0 }),
}));

vi.mock('../ArtifactListModal', () => ({
  ArtifactListModal: () => null,
}));

vi.mock('../BucketKanbanSection', () => ({
  BucketKanbanSection: (props: CapturedProps) => {
    sectionProps.push(props);
    return (
      <div data-testid="bucket">
        {props.fromStr}_{props.toStr}
      </div>
    );
  },
}));

beforeEach(() => {
  sectionProps.length = 0;
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('BucketKanbanList', () => {
  it('renders one N-day bucket initially and forwards filters', () => {
    render(
      <MemoryRouter>
        <BucketKanbanList
          intervalDays={3}
          filters={{ workspace: 'prod' }}
          resetKey="v1"
        />
      </MemoryRouter>
    );

    expect(screen.getAllByTestId('bucket')).toHaveLength(1);
    const first = sectionProps[0]!;
    expect(first.filters).toEqual({ workspace: 'prod' });
    expect(first.isLive).toBe(true);
    // A 3-day bucket: fromStr is 2 days before toStr.
    expect(dayjs(first.toStr).diff(dayjs(first.fromStr), 'day')).toBe(2);
  });

  it('loads an older bucket on demand, stepped by the interval', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <BucketKanbanList intervalDays={3} filters={{}} resetKey="v1" />
      </MemoryRouter>
    );

    expect(screen.getAllByTestId('bucket')).toHaveLength(1);
    const firstTo = sectionProps[0]!.toStr;

    await user.click(screen.getByRole('button', { name: /load older/i }));

    expect(screen.getAllByTestId('bucket')).toHaveLength(2);
    const older = sectionProps[sectionProps.length - 1]!;
    // The next bucket ends one interval (3 days) before the first bucket.
    expect(dayjs(firstTo).diff(dayjs(older.toStr), 'day')).toBe(3);
    expect(older.isLive).toBe(false);
  });

  it('opens a Ctrl-clicked card in a new tab', () => {
    const openMock = vi.spyOn(window, 'open').mockImplementation(() => null);
    render(
      <MemoryRouter>
        <BucketKanbanList intervalDays={3} filters={{}} resetKey="v1" />
      </MemoryRouter>
    );

    sectionProps[0]!.onCardClick(
      { name: 'etl', dagRunId: 'run-1' },
      { ctrlKey: true }
    );

    expect(openMock).toHaveBeenCalledWith(
      '/dags/etl?remoteNode=local&run=run-1',
      '_blank',
      'noopener'
    );
  });
});
