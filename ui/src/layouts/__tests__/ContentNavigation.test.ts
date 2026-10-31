// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { getBreadcrumbItems } from '../ContentNavigation';

describe('getBreadcrumbItems', () => {
  // A calendar is named in schedules by its file name, so the crumb keeps
  // it as written.
  it('shows a calendar by its name', () => {
    expect(getBreadcrumbItems('/calendars/jp-banking')).toEqual([
      { label: 'Home', to: '/home' },
      { label: 'Calendars', to: '/calendars' },
      { label: 'jp-banking', to: '/calendars/jp-banking' },
    ]);
  });
});
