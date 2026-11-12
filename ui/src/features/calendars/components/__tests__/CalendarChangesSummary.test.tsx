// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { CalendarWeekday } from '@/api/v1/schema';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { CalendarChangesSummary } from '../CalendarChangesSummary';

describe('CalendarChangesSummary', () => {
  it('names changed weekdays in the locale', () => {
    render(
      <CalendarChangesSummary
        changes={{
          weekdaysAdded: [CalendarWeekday.SAT],
          weekdaysRemoved: [CalendarWeekday.MON],
          dates: [],
        }}
        dagCount={2}
        locale="ja"
      />
    );

    expect(screen.getByRole('status')).toHaveTextContent('+土 -月');
  });
});
