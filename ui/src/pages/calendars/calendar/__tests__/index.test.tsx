// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import CalendarPage from '..';

type CalendarDetails = components['schemas']['CalendarDetails'];

const mocks = vi.hoisted(() => ({
  put: vi.fn(),
  del: vi.fn(),
  showError: vi.fn(),
  preview: { current: null as unknown },
  editorProps: { current: null as null | Record<string, unknown> },
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PUT: mocks.put, DELETE: mocks.del }),
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
    failed: false,
    retry: vi.fn(),
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

function renderPage(
  details: CalendarDetails,
  search = '',
  appBarNode = 'local'
) {
  useQueryMock.mockImplementation((path: string) =>
    path === '/calendars/{name}'
      ? { data: details, error: undefined, mutate: vi.fn() }
      : { data: { calendars: [] } }
  );
  const setTitle = vi.fn();
  const tree = (node: string) => (
    <MemoryRouter initialEntries={[`/calendars/${details.name}${search}`]}>
      <AppBarContext.Provider
        value={{ setTitle, selectedRemoteNode: node } as never}
      >
        <Routes>
          <Route path="/calendars" element={<p>Calendar list</p>} />
          <Route path="/calendars/:name" element={<CalendarPage />} />
        </Routes>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  const view = render(tree(appBarNode));
  return { selectNode: (node: string) => view.rerender(tree(node)) };
}

function lastCalendarQueryNode(): unknown {
  const calls = useQueryMock.mock.calls.filter(
    (call: unknown[]) => call[0] === '/calendars/{name}'
  );
  return calls[calls.length - 1]?.[1]?.params?.query?.remoteNode;
}

describe('CalendarPage', () => {
  beforeEach(() => {
    useQueryMock.mockClear();
    mocks.put.mockReset();
    mocks.del.mockReset();
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
      '/dags/payroll?remoteNode=local'
    );
    expect(screen.getByText('(via jp-all)')).toBeInTheDocument();
  });

  // A link from a DAG on another node opens that node's calendar, and the
  // links onward stay on it.
  it('works on the remote node its link names', () => {
    renderPage(
      calendar({
        usedBy: [
          { fileName: 'payroll', name: 'payroll', workspace: 'finance' },
        ],
        includes: ['jp-national'],
      }),
      '?remoteNode=production'
    );

    expect(useQueryMock).toHaveBeenCalledWith(
      '/calendars/{name}',
      {
        params: { path: { name: 'ops' }, query: { remoteNode: 'production' } },
      },
      expect.anything()
    );
    expect(screen.getByRole('link', { name: 'payroll' })).toHaveAttribute(
      'href',
      '/dags/payroll?remoteNode=production&workspace=finance'
    );
    expect(screen.getByRole('link', { name: 'jp-national' })).toHaveAttribute(
      'href',
      '/calendars/jp-national?remoteNode=production'
    );
  });

  // A link that names the local node keeps it, although the app bar picks
  // another node.
  it('keeps the local node its link names', () => {
    renderPage(
      calendar({
        usedBy: [{ fileName: 'payroll', name: 'payroll' }],
        includes: ['jp-national'],
      }),
      '?remoteNode=local',
      'production'
    );

    expect(lastCalendarQueryNode()).toBe('local');
    expect(screen.getByRole('link', { name: 'payroll' })).toHaveAttribute(
      'href',
      '/dags/payroll?remoteNode=local'
    );
    expect(screen.getByRole('link', { name: 'jp-national' })).toHaveAttribute(
      'href',
      '/calendars/jp-national?remoteNode=local'
    );
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

  // Picking a node in the app bar is newer than the node the link named.
  it('follows a node picked in the app bar after opening', () => {
    const { selectNode } = renderPage(calendar(), '?remoteNode=production');
    expect(lastCalendarQueryNode()).toBe('production');

    act(() => selectNode('staging'));

    expect(lastCalendarQueryNode()).toBe('staging');
  });

  it('reports a save that could not reach the server', async () => {
    mocks.put.mockRejectedValue(new TypeError('Failed to fetch'));
    renderPage(calendar());

    fireEvent.change(await screen.findByLabelText('Calendar file'), {
      target: { value: 'holidays: [2027-01-11]\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(mocks.showError).toHaveBeenCalledWith(
        'Failed to save the calendar.'
      )
    );
    expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
  });

  // A save that finishes after the user picked another node belongs to the
  // calendar it was sent for, not to the one the editor shows now.
  it('ignores a save that finishes on another node', async () => {
    let finishSave: (response: unknown) => void = () => {};
    mocks.put.mockReturnValue(
      new Promise((resolve) => {
        finishSave = resolve;
      })
    );
    const { selectNode } = renderPage(calendar());

    fireEvent.change(await screen.findByLabelText('Calendar file'), {
      target: { value: 'holidays: [2027-01-11]\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    act(() => selectNode('staging'));
    await act(async () => finishSave({ data: { errors: [], warnings: [] } }));

    expect(screen.getByLabelText('Calendar file')).toHaveValue(
      'holidays: []\n'
    );
    expect(screen.getByRole('button', { name: 'Revert' })).toBeDisabled();
  });

  // Coming back to the same node and calendar is a new visit: a save sent
  // on the earlier visit must not replace the saved copy of a later save.
  it('ignores a save from an earlier visit of the same calendar', async () => {
    let finishFirstSave: (response: unknown) => void = () => {};
    mocks.put
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finishFirstSave = resolve;
        })
      )
      .mockResolvedValueOnce({ data: { errors: [], warnings: [] } });
    const { selectNode } = renderPage(calendar());

    fireEvent.change(await screen.findByLabelText('Calendar file'), {
      target: { value: 'holidays: [2027-01-11]\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    act(() => selectNode('staging'));
    act(() => selectNode('local'));
    fireEvent.change(await screen.findByLabelText('Calendar file'), {
      target: { value: 'holidays: [2027-01-12]\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(mocks.put).toHaveBeenCalledTimes(2));
    await act(async () =>
      finishFirstSave({ data: { errors: [], warnings: [] } })
    );

    expect(screen.getByLabelText('Calendar file')).toHaveValue(
      'holidays: [2027-01-12]\n'
    );
    expect(screen.getByRole('button', { name: 'Revert' })).toBeDisabled();
  });

  // A save started while the calendar is being deleted would also make the
  // page ignore the delete's response.
  it('does not save while a delete is pending', async () => {
    let finishDelete: (response: unknown) => void = () => {};
    mocks.del.mockReturnValue(
      new Promise((resolve) => {
        finishDelete = resolve;
      })
    );
    renderPage(calendar());

    fireEvent.change(await screen.findByLabelText('Calendar file'), {
      target: { value: 'holidays: [2027-01-11]\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', {
        name: 'Delete',
      })
    );
    fireEvent.keyDown(document, { key: 's', metaKey: true });
    await act(async () => finishDelete({ data: {} }));

    expect(mocks.put).not.toHaveBeenCalled();
    expect(await screen.findByText('Calendar list')).toBeInTheDocument();
  });

  it('sends one save while it runs', async () => {
    let finishSave: (response: unknown) => void = () => {};
    mocks.put.mockReturnValue(
      new Promise((resolve) => {
        finishSave = resolve;
      })
    );
    renderPage(calendar());

    fireEvent.change(await screen.findByLabelText('Calendar file'), {
      target: { value: 'holidays: [2027-01-11]\n' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    fireEvent.keyDown(document, { key: 's', metaKey: true });
    await act(async () => finishSave({ data: { errors: [], warnings: [] } }));

    expect(mocks.put).toHaveBeenCalledTimes(1);
  });

  it('sends one delete while it runs', async () => {
    let finishDelete: (response: unknown) => void = () => {};
    mocks.del.mockReturnValue(
      new Promise((resolve) => {
        finishDelete = resolve;
      })
    );
    renderPage(calendar());

    fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
    const confirm = within(screen.getByRole('dialog')).getByRole('button', {
      name: 'Delete',
    });
    fireEvent.click(confirm);
    fireEvent.click(confirm);
    await act(async () => finishDelete({ data: {} }));

    expect(mocks.del).toHaveBeenCalledTimes(1);
    expect(mocks.showError).not.toHaveBeenCalled();
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
