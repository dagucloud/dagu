// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import CalendarPage from '..';

type CalendarDetails = components['schemas']['CalendarDetails'];

const mocks = vi.hoisted(() => ({
  put: vi.fn(),
  showError: vi.fn(),
  preview: { current: null as unknown },
  editorProps: { current: null as null | Record<string, unknown> },
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PUT: mocks.put, DELETE: vi.fn() }),
  useQuery: vi.fn(),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteCalendars: () => true,
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));
vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: vi.fn() }),
}));
vi.mock('@/features/calendars/hooks/useCalendarSchema', () => ({
  useCalendarSchema: () => ({}),
}));
vi.mock('@/features/calendars/hooks/useCalendarPreview', () => ({
  useCalendarPreview: () => ({
    preview: mocks.preview.current,
    isPreviewing: false,
  }),
}));
vi.mock('@/features/dags/components/dag-editor/DAGEditor', () => ({
  default: (props: { value: string; onChange?: (value: string) => void }) => {
    mocks.editorProps.current = props;
    return (
      <textarea
        aria-label="Calendar file"
        value={props.value}
        onChange={(event) => props.onChange?.(event.target.value)}
      />
    );
  },
}));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

function calendar(overrides: Partial<CalendarDetails> = {}): CalendarDetails {
  return {
    name: 'ops',
    builtin: false,
    spec: 'holidays: []\n',
    errors: [],
    warnings: [],
    includes: [],
    includedBy: [],
    usedBy: [],
    ...overrides,
  };
}

function renderPage(details: CalendarDetails) {
  useQueryMock.mockImplementation((path: string) =>
    path === '/calendars/{name}'
      ? { data: details, error: undefined, mutate: vi.fn() }
      : { data: { calendars: [] } }
  );
  render(
    <MemoryRouter initialEntries={[`/calendars/${details.name}`]}>
      <AppBarContext.Provider
        value={{ setTitle: vi.fn(), selectedRemoteNode: 'local' } as never}
      >
        <Routes>
          <Route path="/calendars/:name" element={<CalendarPage />} />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

describe('CalendarPage', () => {
  beforeEach(() => {
    mocks.put.mockReset();
    mocks.showError.mockReset();
    mocks.preview.current = null;
    mocks.editorProps.current = null;
  });

  it('shows a built-in calendar without an editor or actions', () => {
    renderPage(calendar({ name: 'weekdays', builtin: true, spec: '' }));

    expect(screen.getByText(/This calendar is built in/)).toBeInTheDocument();
    expect(screen.queryByLabelText('Calendar file')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Save' })).toBeNull();
  });

  // Deleting a calendar in use would stop its schedules from firing.
  it('keeps a calendar in use from being deleted', () => {
    renderPage(
      calendar({
        usedBy: [{ fileName: 'payroll', name: 'payroll', via: 'jp-all' }],
      })
    );

    expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled();
    expect(screen.getByRole('link', { name: 'payroll' })).toHaveAttribute(
      'href',
      '/dags/payroll'
    );
    expect(screen.getByText('(via jp-all)')).toBeInTheDocument();
  });

  it('marks each problem of the content on its line', async () => {
    mocks.preview.current = {
      errors: [
        `[2:5] field 'holidays[0]': invalid date "2027-13-01": use YYYY-MM-DD`,
        'a calendar file must be a mapping with keys such as weekdays and holidays',
      ],
      warnings: [],
      year: 2027,
      today: '2027-01-04',
      days: [],
      changes: { weekdaysAdded: [], weekdaysRemoved: [], dates: [] },
    };
    renderPage(calendar());

    await screen.findByLabelText('Calendar file');
    expect(mocks.editorProps.current?.markers).toEqual([
      expect.objectContaining({ startLineNumber: 2, startColumn: 5 }),
    ]);
    expect(screen.getByText(/must be a mapping/)).toBeInTheDocument();
    expect(screen.getByText('2 problems')).toBeInTheDocument();
  });

  it('reports a save the server refused', async () => {
    mocks.put.mockResolvedValue({
      data: {
        errors: ["[1:11] field 'holidays[0]': invalid date"],
        warnings: [],
      },
    });
    renderPage(calendar());

    fireEvent.change(await screen.findByLabelText('Calendar file'), {
      target: { value: 'holidays: [nope]\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(mocks.showError).toHaveBeenCalledWith(
        'The calendar was not saved',
        undefined,
        'Problems',
        ["[1:11] field 'holidays[0]': invalid date"]
      )
    );
    expect(mocks.put).toHaveBeenCalledWith('/calendars/{name}', {
      params: { path: { name: 'ops' }, query: { remoteNode: 'local' } },
      body: { spec: 'holidays: [nope]\n' },
    });
  });
});
