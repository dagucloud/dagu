// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import type { RestartView, ScheduleView } from '../../doc';
import { SlotLauncherPanel } from '../LauncherPanel';

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC' }),
}));

const NO_SCHEDULE: ScheduleView = {
  present: false,
  expressions: [],
  others: 0,
};
const SCHEDULED: ScheduleView = {
  present: true,
  expressions: ['0 * * * *'],
  others: 0,
};
const OFF: RestartView = {
  policy: 'no',
  waitSec: null,
  histRetentionRuns: null,
  schedule: false,
  retryPolicy: false,
};
const ON: RestartView = { ...OFF, policy: 'always', waitSec: 5 };

function renderPanel(
  open: 'schedule' | 'cron' | 'always',
  schedule: ScheduleView,
  restart: RestartView,
  editable = true
) {
  const onSchedule = vi.fn();
  const onRestart = vi.fn();
  const view = (s: ScheduleView, r: RestartView) => (
    <UserPreferencesProvider>
      <I18nProvider>
        <SlotLauncherPanel
          open={open}
          schedule={s}
          restart={r}
          editable={editable}
          onSchedule={onSchedule}
          onRestart={onRestart}
          onClose={vi.fn()}
        />
      </I18nProvider>
    </UserPreferencesProvider>
  );
  const utils = render(view(schedule, restart));
  return {
    onSchedule,
    onRestart,
    rerender: (s: ScheduleView, r: RestartView) => utils.rerender(view(s, r)),
  };
}

const chooser = () =>
  screen.queryByRole('radiogroup', { name: 'Choose how the workflow starts' });
const choice = (name: string) =>
  within(chooser()!).getByRole('radio', { name: new RegExp(`^${name}`) });

describe('SlotLauncherPanel', () => {
  // With neither set, the panel asks and writes nothing.
  it('shows the chooser with a line for each choice and no form', () => {
    const { onSchedule, onRestart } = renderPanel('schedule', NO_SCHEDULE, OFF);
    expect(chooser()).toBeInTheDocument();
    expect(choice('Cron')).toHaveAttribute('aria-checked', 'false');
    expect(choice('Cron')).toHaveTextContent(
      'Start on a schedule of cron expressions.'
    );
    expect(choice('Always on')).toHaveTextContent(
      'Keep one run alive and start a new one when it ends.'
    );
    expect(screen.queryByLabelText('New cron expression')).toBeNull();
    expect(screen.queryByRole('radio', { name: 'Unless stopped' })).toBeNull();
    expect(onSchedule).not.toHaveBeenCalled();
    expect(onRestart).not.toHaveBeenCalled();
  });

  it('opens the form of a choice and still writes nothing', async () => {
    const { onSchedule, onRestart } = renderPanel('schedule', NO_SCHEDULE, OFF);
    await userEvent.click(choice('Cron'));
    expect(choice('Cron')).toHaveAttribute('aria-checked', 'true');
    expect(screen.getByLabelText('New cron expression')).toBeInTheDocument();
    await userEvent.click(choice('Always on'));
    expect(screen.queryByLabelText('New cron expression')).toBeNull();
    expect(
      screen.getByRole('radio', { name: 'Unless stopped' })
    ).toBeInTheDocument();
    expect(onSchedule).not.toHaveBeenCalled();
    expect(onRestart).not.toHaveBeenCalled();
  });

  it('shows only the Cron form, and a switch, once a schedule is set', () => {
    renderPanel('cron', SCHEDULED, OFF);
    expect(chooser()).toBeNull();
    expect(screen.getByLabelText('New cron expression')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Switch to Always on' })
    ).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Switch to Cron' })).toBeNull();
  });

  it('shows only the Always on form, and a switch, once a policy is set', () => {
    renderPanel('always', NO_SCHEDULE, ON);
    expect(chooser()).toBeNull();
    expect(screen.getByRole('radio', { name: 'Always' })).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Switch to Cron' })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Switch to Always on' })
    ).toBeNull();
  });

  it('switches from Cron: removes the expressions, then asks with Always on chosen', async () => {
    const { onSchedule, rerender } = renderPanel('cron', SCHEDULED, OFF);
    await userEvent.click(
      screen.getByRole('button', { name: 'Switch to Always on' })
    );
    expect(onSchedule).toHaveBeenCalledTimes(1);
    expect(onSchedule).toHaveBeenCalledWith([]);
    // The YAML has no schedule now: the chooser is back.
    rerender(NO_SCHEDULE, OFF);
    expect(chooser()).toBeInTheDocument();
    expect(choice('Always on')).toHaveAttribute('aria-checked', 'true');
  });

  it('switches from Always on: removes the policy, then asks with Cron chosen', async () => {
    const { onRestart, rerender } = renderPanel('always', NO_SCHEDULE, ON);
    await userEvent.click(
      screen.getByRole('button', { name: 'Switch to Cron' })
    );
    expect(onRestart).toHaveBeenCalledTimes(1);
    expect(onRestart).toHaveBeenCalledWith({ policy: 'no' });
    rerender(NO_SCHEDULE, OFF);
    expect(chooser()).toBeInTheDocument();
    expect(choice('Cron')).toHaveAttribute('aria-checked', 'true');
  });

  it('offers no switch when the file cannot be edited', () => {
    renderPanel('cron', SCHEDULED, OFF, false);
    expect(screen.queryByRole('button', { name: /^Switch to/ })).toBeNull();
  });

  // The file is invalid: the open node shows its form and the note.
  it('shows the form of the open node for a file with both, with a switch', () => {
    renderPanel('always', SCHEDULED, ON);
    expect(chooser()).toBeNull();
    expect(screen.getByRole('radio', { name: 'Always' })).toBeInTheDocument();
    expect(screen.queryByLabelText('New cron expression')).toBeNull();
    expect(screen.getByRole('note')).toHaveTextContent('A service DAG');
    expect(
      screen.getByRole('button', { name: 'Switch to Cron' })
    ).toBeInTheDocument();
  });
});
