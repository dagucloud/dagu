// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DAGSlotStatus } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { BackfillRangePicker } from '../BackfillRangePicker';

vi.mock('@/hooks/api', () => ({ useQuery: vi.fn() }));

const useQueryMock = useQuery as unknown as ReturnType<typeof vi.fn>;

function renderPicker(slots: { slot: string; status: DAGSlotStatus }[]) {
  useQueryMock.mockReturnValue({ data: { slots }, mutate: vi.fn() });
  const onChange = vi.fn();
  render(
    <BackfillRangePicker
      fileName="etl"
      remoteNode="local"
      onChange={onChange}
    />
  );
  return onChange;
}

describe('BackfillRangePicker', () => {
  beforeEach(() => useQueryMock.mockReset());

  it('picks a range back from now', () => {
    const onChange = renderPicker([]);

    fireEvent.click(screen.getByRole('radio', { name: 'Last 30 days' }));
    const target = onChange.mock.calls[0]?.[0] as {
      kind: string;
      from: string;
    };
    expect(target.kind).toBe('range');
    const days = (Date.now() - new Date(target.from).getTime()) / 86_400_000;
    expect(Math.round(days)).toBe(30);
  });

  // The range starts after the last slot that succeeded, so it holds the
  // slots since then and not that one.
  it('starts the range after the last success', () => {
    const onChange = renderPicker([
      { slot: '2026-11-01T02:00:00Z', status: DAGSlotStatus.succeeded },
      { slot: '2026-11-02T02:00:00Z', status: DAGSlotStatus.succeeded },
      { slot: '2026-11-03T02:00:00Z', status: DAGSlotStatus.failed },
    ]);

    fireEvent.click(
      screen.getByRole('radio', { name: /Since the last success/ })
    );
    expect(onChange).toHaveBeenLastCalledWith({
      kind: 'range',
      from: '2026-11-02T02:01:00.000Z',
    });
  });

  it('cannot start after a success the slots do not have', () => {
    renderPicker([
      { slot: '2026-11-03T02:00:00Z', status: DAGSlotStatus.failed },
    ]);

    expect(
      screen.getByRole('radio', { name: /Since the last success/ })
    ).toBeDisabled();
    expect(
      screen.getByText('No slot succeeded in the last 90 days.')
    ).toBeInTheDocument();
  });

  it('takes typed fire times, with the end optional', () => {
    const onChange = renderPicker([]);

    fireEvent.click(screen.getByRole('radio', { name: 'Fire times' }));
    expect(onChange).toHaveBeenLastCalledWith(null);
    fireEvent.change(screen.getByLabelText('First fire time'), {
      target: { value: '2026-10-01T00:00' },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      kind: 'range',
      from: '2026-10-01T00:00',
    });
    fireEvent.change(screen.getByLabelText('Last fire time'), {
      target: { value: '2026-10-31T23:59' },
    });
    expect(onChange).toHaveBeenLastCalledWith({
      kind: 'range',
      from: '2026-10-01T00:00',
      to: '2026-10-31T23:59',
    });
  });

  // A failed read says so and offers a retry, and the other presets stay.
  it('reports a failed read of the slots', () => {
    const mutate = vi.fn();
    useQueryMock.mockReturnValue({
      data: undefined,
      error: { message: 'slots unavailable' },
      mutate,
    });
    render(
      <BackfillRangePicker
        fileName="etl"
        remoteNode="local"
        onChange={vi.fn()}
      />
    );

    expect(screen.getByRole('alert')).toHaveTextContent(
      'Could not read the last success: slots unavailable'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(mutate).toHaveBeenCalled();
    expect(screen.getByRole('radio', { name: 'Last 30 days' })).toBeEnabled();
  });
});
