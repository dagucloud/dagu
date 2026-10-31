// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useCanWriteCalendars } from '@/contexts/AuthContext';
import { useQuery } from '@/hooks/api';
import CalendarsPage from '..';

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: vi.fn() }),
  useQuery: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteCalendars: vi.fn(),
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;
const canWriteMock = useCanWriteCalendars as unknown as ReturnType<
  typeof vi.fn
>;

function renderPage() {
  render(
    <MemoryRouter>
      <AppBarContext.Provider
        value={{ setTitle: vi.fn(), selectedRemoteNode: 'local' } as never}
      >
        <CalendarsPage />
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('CalendarsPage', () => {
  beforeEach(() => {
    canWriteMock.mockReturnValue(true);
    useQueryMock.mockReturnValue({
      data: {
        calendars: [
          {
            name: 'broken',
            builtin: false,
            error: 'calendar "broken": field \'holidays\': must be a list',
            dagCount: 1,
          },
          {
            name: 'jp-banking',
            builtin: false,
            description: 'Bank days',
            through: '2026-11-15',
            warning: 'calendar "jp-banking" lists holidays through 2026-11-15',
            dagCount: 3,
          },
          {
            name: 'weekdays',
            builtin: true,
            description: 'Monday to Friday, no holidays',
            dagCount: 0,
          },
        ],
      },
      error: undefined,
      isLoading: false,
      mutate: vi.fn(),
    } as never);
  });

  it('shows what needs attention and how many DAGs each calendar serves', () => {
    renderPage();

    const broken = screen.getByRole('row', { name: /broken/ });
    expect(within(broken).getByText('Invalid')).toBeInTheDocument();
    expect(within(broken).getByText(/must be a list/)).toBeInTheDocument();

    const banking = screen.getByRole('row', { name: /jp-banking/ });
    expect(
      within(banking).getByRole('link', { name: 'jp-banking' })
    ).toHaveAttribute('href', '/calendars/jp-banking');
    expect(within(banking).getByText('Holidays run out')).toBeInTheDocument();
    expect(within(banking).getByText('3')).toBeInTheDocument();

    expect(
      within(screen.getByRole('row', { name: /weekdays/ })).getByText(
        'Built in'
      )
    ).toBeInTheDocument();
  });

  it('offers a new calendar to users who can write calendars', () => {
    renderPage();
    expect(
      screen.getByRole('button', { name: 'New calendar' })
    ).toBeInTheDocument();
  });

  it('lets other users browse without creating', () => {
    canWriteMock.mockReturnValue(false);
    renderPage();
    expect(screen.queryByRole('button', { name: 'New calendar' })).toBeNull();
  });
});
