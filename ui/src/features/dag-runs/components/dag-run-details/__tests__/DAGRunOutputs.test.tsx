// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StatusLabel } from '@/api/v1/schema';
import dayjs from '@/lib/dayjs';
import DAGRunOutputs from '../DAGRunOutputs';

const h = vi.hoisted(() => ({
  useQuery: vi.fn(),
}));

vi.mock('@/hooks/api', () => ({ useQuery: h.useQuery }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-a',
}));

const COMPLETED_AT = '2026-01-15T02:00:03Z';

function outputs(values: Record<string, string>, attemptId?: string) {
  return {
    metadata: {
      dagName: 'nightly',
      dagRunId: 'run-42',
      attemptId,
      status: StatusLabel.succeeded,
      completedAt: COMPLETED_AT,
    },
    outputs: values,
  };
}

function answer(state: {
  data?: unknown;
  isLoading?: boolean;
  error?: unknown;
}) {
  h.useQuery.mockReturnValue({
    data: undefined,
    isLoading: false,
    error: undefined,
    ...state,
  });
}

function renderOutputs() {
  return render(<DAGRunOutputs dagName="nightly" dagRunId="run-42" />);
}

/** Key and value of each body row, in display order. */
function rows(): string[][] {
  const table = screen.getByRole('table');
  return within(table)
    .getAllByRole('row')
    .slice(1)
    .map((row) =>
      within(row)
        .getAllByRole('cell')
        .slice(0, 2)
        .map((cell) => cell.textContent ?? '')
    );
}

const writeText = vi.fn(() => Promise.resolve());
const originalClipboard = navigator.clipboard;

beforeEach(() => {
  h.useQuery.mockReset();
  writeText.mockClear();
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  });
});

afterEach(() => {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: originalClipboard,
  });
});

// parity:test-run-outputs-untested parity:api-run-outputs
describe('DAGRunOutputs', () => {
  it('reads the outputs of the run on the remote node', () => {
    answer({ data: outputs({ a: '1' }) });
    renderOutputs();

    expect(h.useQuery).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/outputs',
      {
        params: {
          query: { remoteNode: 'node-a' },
          path: { name: 'nightly', dagRunId: 'run-42' },
        },
      },
      { revalidateOnFocus: false }
    );
  });

  it('shows Loading before the first answer', () => {
    answer({ isLoading: true });
    renderOutputs();

    expect(screen.getByText('Loading outputs...')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('keeps the table while a later read loads', () => {
    answer({ isLoading: true, data: outputs({ a: '1' }) });
    renderOutputs();

    expect(screen.queryByText('Loading outputs...')).not.toBeInTheDocument();
    expect(rows()).toEqual([['a', '1']]);
  });

  it('says No outputs available on an error', () => {
    answer({ error: { message: 'not found' } });
    renderOutputs();

    expect(screen.getByText('No outputs available')).toBeInTheDocument();
  });

  it('says No outputs available without data', () => {
    answer({});
    renderOutputs();

    expect(screen.getByText('No outputs available')).toBeInTheDocument();
  });

  // A host that pads the tab (the dock) asks for messages without padding.
  it('pads its messages unless the host pads the tab', () => {
    answer({ data: outputs({}) });
    const { unmount } = renderOutputs();
    expect(screen.getByText('No outputs collected')).toHaveClass('p-4');
    unmount();

    render(
      <DAGRunOutputs dagName="nightly" dagRunId="run-42" padded={false} />
    );
    expect(screen.getByText('No outputs collected')).not.toHaveClass('p-4');
  });

  it('says No outputs collected for an empty set', () => {
    answer({ data: outputs({}) });
    renderOutputs();

    expect(screen.getByText('No outputs collected')).toBeInTheDocument();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it('shows the count, status, completion time and attempt', () => {
    answer({ data: outputs({ a: '1', b: '2' }, 'attempt-7') });
    renderOutputs();

    expect(screen.getByText('Outputs')).toBeInTheDocument();
    expect(screen.getByText('(2 items)')).toBeInTheDocument();
    expect(screen.getByText('Status:')).toBeInTheDocument();
    expect(screen.getByText(/^succeeded$/i)).toBeInTheDocument();
    expect(
      screen.getByText(dayjs(COMPLETED_AT).format('YYYY-MM-DD HH:mm:ss'))
    ).toBeInTheDocument();
    expect(screen.getByText('attempt-7')).toBeInTheDocument();
  });

  // The dock shows the run status and times: no header card there.
  it('leaves out the header card for a host that shows the run itself', () => {
    answer({ data: outputs({ a: '1', b: '2' }, 'attempt-7') });
    render(<DAGRunOutputs dagName="nightly" dagRunId="run-42" bare />);

    expect(screen.queryByText('Status:')).not.toBeInTheDocument();
    expect(screen.queryByText('attempt-7')).not.toBeInTheDocument();
    expect(screen.queryByText('(2 items)')).not.toBeInTheDocument();
    expect(rows()).toEqual([
      ['a', '1'],
      ['b', '2'],
    ]);
  });

  it('counts one item and hides a missing attempt', () => {
    answer({ data: outputs({ a: '1' }) });
    renderOutputs();

    expect(screen.getByText('(1 item)')).toBeInTheDocument();
    expect(screen.queryByText('Attempt:')).not.toBeInTheDocument();
  });

  it('lists Key, Value and Actions columns sorted by key', () => {
    answer({ data: outputs({ beta: 'x', alpha: 'z', gamma: 'y' }) });
    renderOutputs();

    const headers = within(screen.getByRole('table'))
      .getAllByRole('columnheader')
      .map((header) => header.textContent);
    expect(headers).toEqual(['Key', 'Value', 'Actions']);
    expect(rows()).toEqual([
      ['alpha', 'z'],
      ['beta', 'x'],
      ['gamma', 'y'],
    ]);
  });

  it('sorts by key and value in both directions', () => {
    answer({ data: outputs({ beta: 'x', alpha: 'z', gamma: 'y' }) });
    renderOutputs();

    fireEvent.click(screen.getByRole('button', { name: 'Sort by key' }));
    expect(rows().map(([key]) => key)).toEqual(['gamma', 'beta', 'alpha']);

    fireEvent.click(screen.getByRole('button', { name: 'Sort by value' }));
    expect(rows().map(([, value]) => value)).toEqual(['x', 'y', 'z']);

    fireEvent.click(screen.getByRole('button', { name: 'Sort by value' }));
    expect(rows().map(([, value]) => value)).toEqual(['z', 'y', 'x']);
  });

  it('shows each value with JsonValue when prettyValues is set', () => {
    answer({ data: outputs({ cfg: '{"a":1,"b":[2]}' }) });
    render(<DAGRunOutputs dagName="nightly" dagRunId="run-42" prettyValues />);

    const value = screen.getByTestId('json-value');
    expect(value).toHaveAttribute('data-json', 'true');
    expect(value).toHaveTextContent('"a": 1');
    // JsonValue has its own Copy value: the row has no second copy button.
    expect(
      screen.queryByRole('button', { name: 'Copy value for cfg' })
    ).not.toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: 'Copy value' })).toHaveLength(
      1
    );
  });

  it('polls while the answer is empty when pollWhileEmpty is set', () => {
    answer({ data: outputs({}) });
    render(
      <DAGRunOutputs dagName="nightly" dagRunId="run-42" pollWhileEmpty />
    );

    const options = h.useQuery.mock.calls[0]?.[2] as {
      refreshInterval: (latest: unknown) => number;
    };
    expect(options.refreshInterval(undefined)).toBe(2000);
    expect(options.refreshInterval(outputs({}))).toBe(2000);
    expect(options.refreshInterval(outputs({ a: '1' }))).toBe(0);
  });

  it('copies a value from its row', async () => {
    answer({ data: outputs({ token: 'abc' }) });
    renderOutputs();

    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Copy value for token' })
      );
    });

    expect(writeText).toHaveBeenCalledWith('abc');
  });

  it('filters by key or value and says when nothing matches', () => {
    answer({ data: outputs({ region: 'eu', size: 'large' }) });
    renderOutputs();
    const filter = screen.getByPlaceholderText(
      'Filter outputs by key or value...'
    );

    fireEvent.change(filter, { target: { value: 'REG' } });
    expect(rows()).toEqual([['region', 'eu']]);

    fireEvent.change(filter, { target: { value: 'large' } });
    expect(rows()).toEqual([['size', 'large']]);

    fireEvent.change(filter, { target: { value: 'nothing' } });
    expect(
      screen.getByText('No outputs match your filter')
    ).toBeInTheDocument();
    // The header and the count stay.
    expect(screen.getByText('(2 items)')).toBeInTheDocument();
  });
});
