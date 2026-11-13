// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import type { ScheduleView } from '../../doc';
import { CronLauncherPanel } from '../LauncherPanel';

// The panel reads the time zone of the scheduler.
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC' }),
}));

function renderPanel(schedule: ScheduleView, onChange = vi.fn()) {
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <CronLauncherPanel
          schedule={schedule}
          editable
          onChange={onChange}
          onClose={vi.fn()}
        />
      </I18nProvider>
    </UserPreferencesProvider>
  );
  return onChange;
}

const REMOVE = /remove (all )?cron expressions/i;

describe('CronLauncherPanel removal', () => {
  // An empty list removes only the cron expressions, so with none left the
  // button would send an edit that does nothing.
  it('shows no remove button when only other entries exist', () => {
    renderPanel({ present: true, expressions: [], others: 2 });
    expect(screen.queryByRole('button', { name: REMOVE })).toBeNull();
    expect(
      screen.queryByRole('button', { name: /remove schedule/i })
    ).toBeNull();
  });

  it('removes the cron expressions and says what stays in the YAML', async () => {
    const onChange = renderPanel({
      present: true,
      expressions: ['0 * * * *'],
      others: 1,
    });
    await userEvent.click(screen.getByRole('button', { name: REMOVE }));
    expect(onChange).toHaveBeenCalledWith([]);
    expect(
      screen.getByText(
        /other schedule entries \(stop, restart, one-off\) stay in the yaml/i
      )
    ).toBeTruthy();
  });
});
