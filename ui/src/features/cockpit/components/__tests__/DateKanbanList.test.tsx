// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { type Config, ConfigContext } from '@/contexts/ConfigContext';
import { PeekContext } from '@/features/peek/PeekContext';
import { DateKanbanList } from '../DateKanbanList';
import { useInfiniteKanban } from '../../hooks/useInfiniteKanban';

vi.mock('../../hooks/useInfiniteKanban', () => ({
  useInfiniteKanban: vi.fn(),
}));

vi.mock('../DateKanbanSection', () => ({
  DateKanbanSection: ({
    date,
    onCardClick,
  }: {
    date: string;
    onCardClick: (run: unknown, event: React.MouseEvent) => void;
  }) => (
    <div>
      <span>{date}</span>
      <button
        type="button"
        onClick={(event) =>
          onCardClick(
            {
              dagRunId: 'run-with-artifacts',
              name: 'artifact-dag',
              artifactsAvailable: true,
            },
            event
          )
        }
      >
        Open run {date}
      </button>
    </div>
  ),
}));

vi.mock('../ArtifactListModal', () => ({
  ArtifactListModal: () => null,
}));

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">{`${location.pathname}${location.search}`}</div>
  );
}

function renderList(props: React.ComponentProps<typeof DateKanbanList>) {
  return render(
    <MemoryRouter initialEntries={['/cockpit']}>
      <DateKanbanList {...props} />
      <LocationProbe />
    </MemoryRouter>
  );
}

const useInfiniteKanbanMock = useInfiniteKanban as unknown as {
  mockReturnValue: (value: unknown) => void;
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe('DateKanbanList', () => {
  it('renders the fallback button and loads one older day on demand', () => {
    const loadNextDate = vi.fn();
    useInfiniteKanbanMock.mockReturnValue({
      loadedDates: ['2026-03-21', '2026-03-20'],
      todayStr: '2026-03-21',
      hasMore: true,
      loadNextDate,
    });

    renderList({ selectedWorkspace: 'ops' });

    fireEvent.click(screen.getByRole('button', { name: 'Load older day' }));

    expect(loadNextDate).toHaveBeenCalledTimes(1);
  });

  it('disables demand loading while the template selector is open', () => {
    const loadNextDate = vi.fn();
    useInfiniteKanbanMock.mockReturnValue({
      loadedDates: ['2026-03-21', '2026-03-20'],
      todayStr: '2026-03-21',
      hasMore: true,
      loadNextDate,
    });

    renderList({ selectedWorkspace: 'ops', suspendLoadMore: true });

    expect(
      screen.getByRole('button', { name: 'Load older day' })
    ).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Load older day' }));
    expect(loadNextDate).not.toHaveBeenCalled();
  });

  it('opens an artifact run on the Artifacts tab of its workflow page', () => {
    useInfiniteKanbanMock.mockReturnValue({
      loadedDates: ['2026-03-21'],
      todayStr: '2026-03-21',
      hasMore: false,
      loadNextDate: vi.fn(),
    });

    renderList({ selectedWorkspace: 'ops' });

    fireEvent.click(
      screen.getByRole('button', { name: 'Open run 2026-03-21' })
    );

    expect(screen.getByTestId('location')).toHaveTextContent(
      '/dags/artifact-dag?remoteNode=local&run=run-with-artifacts&dock=artifacts'
    );
  });

  it('opens a Ctrl-clicked run in a new tab', () => {
    const openMock = vi.spyOn(window, 'open').mockImplementation(() => null);
    useInfiniteKanbanMock.mockReturnValue({
      loadedDates: ['2026-03-21'],
      todayStr: '2026-03-21',
      hasMore: false,
      loadNextDate: vi.fn(),
    });

    render(
      <MemoryRouter initialEntries={['/cockpit']}>
        <ConfigContext.Provider value={{ basePath: '' } as Config}>
          <DateKanbanList selectedWorkspace="ops" />
        </ConfigContext.Provider>
        <LocationProbe />
      </MemoryRouter>
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Open run 2026-03-21' }),
      { ctrlKey: true }
    );

    expect(openMock).toHaveBeenCalledWith(
      '/dags/artifact-dag?remoteNode=local&run=run-with-artifacts&dock=artifacts',
      '_blank',
      'noopener'
    );
    expect(screen.getByTestId('location')).toHaveTextContent('/cockpit');
  });

  it('shows a card in the peek, set to open its Artifacts tab', () => {
    const peek = { open: vi.fn(), isOpen: false };
    useInfiniteKanbanMock.mockReturnValue({
      loadedDates: ['2026-03-21'],
      todayStr: '2026-03-21',
      hasMore: false,
      loadNextDate: vi.fn(),
    });

    render(
      <MemoryRouter initialEntries={['/cockpit']}>
        <PeekContext.Provider value={peek}>
          <DateKanbanList selectedWorkspace="ops" />
        </PeekContext.Provider>
        <LocationProbe />
      </MemoryRouter>
    );
    fireEvent.click(
      screen.getByRole('button', { name: 'Open run 2026-03-21' })
    );

    expect(peek.open).toHaveBeenCalledWith({
      dag: 'artifact-dag',
      run: 'run-with-artifacts',
      dock: 'artifacts',
    });
    expect(screen.getByTestId('location')).toHaveTextContent(/^\/cockpit$/);
  });
});
