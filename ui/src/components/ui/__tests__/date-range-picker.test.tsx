// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { describe, expect, it, vi } from 'vitest';
import { DateRangePicker } from '../date-range-picker';

function renderPicker(onFromDateChange = vi.fn()) {
  render(
    <DateRangePicker
      fromDate="2026-09-01T00:00"
      toDate="2026-09-30T23:59"
      onFromDateChange={onFromDateChange}
      onToDateChange={vi.fn()}
      fromLabel="From"
      toLabel="To"
    />
  );
  return screen.getAllByPlaceholderText('YYYY-MM-DD HH:mm:ss')[0]!;
}

describe('DateRangePicker', () => {
  it('reports an emptied field as a cleared bound', () => {
    const onFromDateChange = vi.fn();
    const input = renderPicker(onFromDateChange);

    fireEvent.change(input, { target: { value: '' } });

    expect(onFromDateChange).toHaveBeenCalledWith('');
  });

  // A half-typed date is not a bound, so it must not be reported as one, and
  // must not be mistaken for a clear either.
  it('reports nothing while a date is partially typed', () => {
    const onFromDateChange = vi.fn();
    const input = renderPicker(onFromDateChange);

    fireEvent.change(input, { target: { value: '2026-09-1' } });

    expect(onFromDateChange).not.toHaveBeenCalled();
  });

  it('reports a whole date once it parses', () => {
    const onFromDateChange = vi.fn();
    const input = renderPicker(onFromDateChange);

    fireEvent.change(input, { target: { value: '2026-08-15 09:30:00' } });

    expect(onFromDateChange).toHaveBeenCalledWith('2026-08-15T09:30');
  });
});
