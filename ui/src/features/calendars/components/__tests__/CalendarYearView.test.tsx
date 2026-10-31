// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarDayStatus } from '@/api/v1/schema';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CalendarYearView } from '../CalendarYearView';

const preview2027 = {
  errors: [],
  warnings: [],
  year: 2027,
  today: '2027-01-04',
  days: [
    { date: '2027-01-01', status: CalendarDayStatus.holiday, changed: false },
  ],
  changes: { weekdaysAdded: [], weekdaysRemoved: [], dates: [] },
};

describe('CalendarYearView', () => {
  // The dates of the previous year must not appear under the next one
  // while it loads or when it fails.
  it('shows only the dates of the year it names', () => {
    const onRetry = vi.fn();
    render(
      <CalendarYearView
        preview={preview2027}
        year={2028}
        failed
        onRetry={onRetry}
        onYearChange={vi.fn()}
        locale="en"
      />
    );

    expect(screen.queryByRole('group', { name: 'January' })).toBeNull();
    expect(screen.getByRole('alert')).toHaveTextContent(
      "Couldn't preview the calendar."
    );
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onRetry).toHaveBeenCalled();
  });
});
