// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { ScheduleKind } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import DAGAttributes from '../DAGAttributes';

afterEach(() => {
  cleanup();
});

describe('DAGAttributes', () => {
  it('renders the next scheduled run when present', () => {
    const nextRun = '2026-04-03T12:00:00Z';

    render(
      <DAGAttributes
        dag={{
          name: 'scheduled-dag',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
          nextRun,
        }}
      />
    );

    expect(screen.getByText('Next run:')).toBeInTheDocument();
    expect(
      screen.getByText(
        `${dayjs(nextRun).format('YYYY-MM-DD HH:mm:ss')} (${dayjs(nextRun).fromNow()})`
      )
    ).toBeInTheDocument();
  });

  it('renders no upcoming run when the next run is unavailable', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'scheduled-dag',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
        }}
      />
    );

    expect(screen.getByText('Next run:')).toBeInTheDocument();
    expect(screen.getByText('No upcoming run')).toBeInTheDocument();
  });

  it('localizes the relative next run time', () => {
    const nextRun = '2026-04-03T12:00:00Z';
    localStorage.setItem('user_preferences', JSON.stringify({ locale: 'ja' }));

    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <DAGAttributes
            dag={{
              name: 'scheduled-dag',
              schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
              nextRun,
            }}
          />
        </I18nProvider>
      </UserPreferencesProvider>
    );

    expect(
      screen.getByText(
        `${dayjs(nextRun).format('YYYY-MM-DD HH:mm:ss')} (${dayjs(nextRun).locale('ja').fromNow()})`
      )
    ).toBeInTheDocument();
  });
});
