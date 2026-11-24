// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { ScheduleKind } from '@/api/v1/schema';
import { I18nProvider } from '@/i18n/I18nProvider';
import type { RestartSettings, ScheduleView } from '../../doc';
import { SlotLauncherPanel } from '../LauncherPanel';

// The Cron form reads the time zone of the scheduler.
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC' }),
}));

const NO_SCHEDULE: ScheduleView = {
  present: false,
  expressions: [],
  others: 0,
  entries: [],
};
const SCHEDULED: ScheduleView = {
  present: true,
  expressions: ['0 * * * *'],
  others: 0,
  entries: [
    {
      expressionIndex: 0,
      schedule: { kind: ScheduleKind.cron, expression: '0 * * * *' },
      params: {},
    },
  ],
};
const OFF: RestartSettings = {
  policy: 'no',
  waitSec: null,
  histRetentionRuns: null,
  histRetentionDays: null,
  schedule: false,
  retryPolicy: false,
};
const ON: RestartSettings = {
  ...OFF,
  policy: 'always',
  waitSec: 5,
  histRetentionRuns: 20,
};

function renderPanel(
  open: 'schedule' | 'cron' | 'always',
  schedule: ScheduleView,
  restart: RestartSettings,
  { editable = true, scheduleRemovable = true } = {}
) {
  const onSchedule = vi.fn();
  const onRestart = vi.fn();
  const view = (s: ScheduleView, r: RestartSettings) => (
    <UserPreferencesProvider>
      <I18nProvider>
        <SlotLauncherPanel
          open={open}
          schedule={s}
          scheduleRemovable={scheduleRemovable}
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
    rerender: (s: ScheduleView, r: RestartSettings) =>
      utils.rerender(view(s, r)),
  };
}

const chooser = () =>
  screen.queryByRole('group', { name: 'Choose how the workflow starts' });
const choice = (name: string) =>
  within(chooser()!).getByRole('button', { name: new RegExp(`^${name}`) });
const policy = (name: string) => screen.getByRole('button', { name });
const wait = () => screen.getByLabelText('Wait before a new run (s)');
const runs = () => screen.getByLabelText('Runs kept in the history');
const DEFAULT_RUNS = 'With no value, the history keeps the latest 100 runs.';
const EDIT_IN_YAML = 'Edit the schedule in the YAML to switch to Always on.';

describe('SlotLauncherPanel', () => {
  // With neither set, the panel asks and writes nothing.
  it('shows the chooser with a line for each choice and no form', () => {
    const { onSchedule, onRestart } = renderPanel('schedule', NO_SCHEDULE, OFF);
    expect(chooser()).toBeInTheDocument();
    expect(choice('Cron')).toHaveAttribute('aria-pressed', 'false');
    expect(choice('Cron')).toHaveTextContent(
      'Start on a schedule of cron expressions.'
    );
    expect(choice('Always on')).toHaveTextContent(
      'Keep one run alive and start a new one when it ends.'
    );
    expect(screen.queryByLabelText('New cron expression')).toBeNull();
    expect(screen.queryByRole('button', { name: 'Unless stopped' })).toBeNull();
    expect(onSchedule).not.toHaveBeenCalled();
    expect(onRestart).not.toHaveBeenCalled();
  });

  it('opens the form of a choice and still writes nothing', async () => {
    const { onSchedule, onRestart } = renderPanel('schedule', NO_SCHEDULE, OFF);
    await userEvent.click(choice('Cron'));
    expect(choice('Cron')).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByLabelText('New cron expression')).toBeInTheDocument();
    await userEvent.click(choice('Always on'));
    expect(screen.queryByLabelText('New cron expression')).toBeNull();
    // The policy is off: the form has no field yet.
    expect(policy('Off')).toHaveAttribute('aria-pressed', 'true');
    expect(policy('Unless stopped')).toHaveAttribute('aria-pressed', 'false');
    expect(screen.queryByLabelText('Wait before a new run (s)')).toBeNull();
    expect(onSchedule).not.toHaveBeenCalled();
    expect(onRestart).not.toHaveBeenCalled();
  });

  it('moves between the choices with the arrow keys', async () => {
    renderPanel('schedule', NO_SCHEDULE, OFF);
    choice('Cron').focus();
    await userEvent.keyboard('{ArrowDown}');
    expect(choice('Always on')).toHaveFocus();
    await userEvent.keyboard('{ArrowUp}');
    expect(choice('Cron')).toHaveFocus();
  });

  it('shows only the Cron form, and a switch, once a schedule is set', () => {
    renderPanel('cron', SCHEDULED, OFF);
    expect(chooser()).toBeNull();
    expect(screen.getByLabelText('New cron expression')).toBeInTheDocument();
    expect(
      screen.getByRole('button', { name: 'Switch to Always on' })
    ).toBeInTheDocument();
    expect(screen.queryByText(EDIT_IN_YAML)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Switch to Cron' })).toBeNull();
  });

  // The switch removes the start expressions only: with a `stop` entry or a
  // one-off, a schedule would stay next to the policy.
  it('offers a note in place of the switch for a schedule that it cannot remove', () => {
    renderPanel('cron', SCHEDULED, OFF, { scheduleRemovable: false });
    expect(
      screen.queryByRole('button', { name: 'Switch to Always on' })
    ).toBeNull();
    expect(screen.getByText(EDIT_IN_YAML)).toBeInTheDocument();
  });

  it('shows only the Always on form, and a switch, once a policy is set', () => {
    renderPanel('always', NO_SCHEDULE, ON);
    expect(chooser()).toBeNull();
    expect(policy('Off')).toHaveAttribute('aria-pressed', 'false');
    expect(policy('Always')).toHaveAttribute('aria-pressed', 'true');
    expect(policy('Unless stopped')).toHaveAttribute('aria-pressed', 'false');
    expect(wait()).toHaveValue('5');
    expect(runs()).toHaveValue('20');
    expect(
      screen.getByRole('button', { name: 'Switch to Cron' })
    ).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Switch to Always on' })
    ).toBeNull();
  });

  it('switches from Cron: removes the expressions, then asks with Always on chosen and focused', async () => {
    const { onSchedule, rerender } = renderPanel('cron', SCHEDULED, OFF);
    await userEvent.click(
      screen.getByRole('button', { name: 'Switch to Always on' })
    );
    expect(onSchedule).toHaveBeenCalledTimes(1);
    expect(onSchedule).toHaveBeenCalledWith([]);
    // The YAML has no schedule now: the chooser is back.
    rerender(NO_SCHEDULE, OFF);
    expect(chooser()).toBeInTheDocument();
    expect(choice('Always on')).toHaveAttribute('aria-pressed', 'true');
    // The focus is on the first control of the Always on form.
    expect(policy('Off')).toHaveFocus();
  });

  it('switches from Always on: removes the policy, then asks with Cron chosen and focused', async () => {
    const { onRestart, rerender } = renderPanel('always', NO_SCHEDULE, ON);
    await userEvent.click(
      screen.getByRole('button', { name: 'Switch to Cron' })
    );
    expect(onRestart).toHaveBeenCalledTimes(1);
    expect(onRestart).toHaveBeenCalledWith({ policy: 'no' });
    rerender(NO_SCHEDULE, OFF);
    expect(chooser()).toBeInTheDocument();
    expect(choice('Cron')).toHaveAttribute('aria-pressed', 'true');
    // The focus is on the first control of the Cron form.
    expect(screen.getByLabelText('Frequency')).toHaveFocus();
  });

  it('is read only when the file cannot be edited', () => {
    renderPanel('always', NO_SCHEDULE, ON, { editable: false });
    expect(policy('Unless stopped')).toBeDisabled();
    expect(wait()).toHaveAttribute('readonly');
    expect(screen.queryByRole('button', { name: /^Switch to/ })).toBeNull();
  });

  // The file is invalid: the open node shows its form and the note.
  it('shows the form of the open node for a file with both, with a switch', () => {
    renderPanel('always', SCHEDULED, { ...ON, schedule: true });
    expect(chooser()).toBeNull();
    // The author can still change the policy.
    expect(policy('Unless stopped')).toBeEnabled();
    expect(screen.queryByLabelText('New cron expression')).toBeNull();
    expect(screen.getByRole('note')).toHaveTextContent('A service DAG');
    expect(
      screen.getByRole('button', { name: 'Switch to Cron' })
    ).toBeInTheDocument();
  });

  // A service needs a wait of 1 s or more: the new policy brings one.
  it('gives a wait of 5 s to a policy that has none', async () => {
    const { onRestart } = renderPanel('schedule', NO_SCHEDULE, OFF);
    await userEvent.click(choice('Always on'));
    await userEvent.click(policy('Unless stopped'));
    expect(onRestart).toHaveBeenCalledTimes(1);
    expect(onRestart).toHaveBeenCalledWith({
      policy: 'unless-stopped',
      waitSec: 5,
    });
  });

  it('keeps the wait of the file when the policy turns on', async () => {
    const { onRestart } = renderPanel('schedule', NO_SCHEDULE, {
      ...OFF,
      waitSec: 12,
    });
    await userEvent.click(choice('Always on'));
    await userEvent.click(policy('Always'));
    expect(onRestart).toHaveBeenCalledWith({ policy: 'always' });
  });

  it('changes the policy of a service and leaves its other keys', async () => {
    const { onRestart } = renderPanel('always', NO_SCHEDULE, ON);
    await userEvent.click(policy('Unless stopped'));
    expect(onRestart).toHaveBeenCalledWith({ policy: 'unless-stopped' });
  });

  it('commits a wait on Enter and a retention on blur', async () => {
    const { onRestart } = renderPanel('always', NO_SCHEDULE, ON);
    await userEvent.clear(wait());
    await userEvent.type(wait(), '30{Enter}');
    expect(onRestart).toHaveBeenLastCalledWith({ waitSec: 30 });
    await userEvent.clear(runs());
    await userEvent.type(runs(), '50');
    await userEvent.tab();
    expect(onRestart).toHaveBeenLastCalledWith({ histRetentionRuns: 50 });
  });

  it('empties the retention to remove it, but not the wait', async () => {
    const { onRestart } = renderPanel('always', NO_SCHEDULE, ON);
    await userEvent.clear(runs());
    await userEvent.tab();
    expect(onRestart).toHaveBeenLastCalledWith({ histRetentionRuns: null });
    onRestart.mockClear();
    await userEvent.clear(wait());
    await userEvent.tab();
    expect(onRestart).not.toHaveBeenCalled();
    expect(wait()).toHaveValue('5');
  });

  it('puts back a value that is not a positive integer', async () => {
    const { onRestart } = renderPanel('always', NO_SCHEDULE, ON);
    for (const bad of ['0', '-3', '1.5', 'abc']) {
      await userEvent.clear(wait());
      await userEvent.type(wait(), `${bad}{Enter}`);
      expect(wait()).toHaveValue('5');
    }
    expect(onRestart).not.toHaveBeenCalled();
  });

  // The 100 runs are the default of a file with no retention in days.
  it('names the default of 100 runs only without a retention in days', () => {
    const service = { ...ON, histRetentionRuns: null };
    const { rerender } = renderPanel('always', NO_SCHEDULE, service);
    expect(screen.getByText(DEFAULT_RUNS)).toBeInTheDocument();
    expect(runs()).toHaveAttribute('placeholder', '100');
    rerender(NO_SCHEDULE, { ...service, histRetentionDays: 7 });
    expect(screen.queryByText(DEFAULT_RUNS)).toBeNull();
    expect(runs()).not.toHaveAttribute('placeholder');
  });

  // Spec 080: a service has no retry_policy.
  it('does not turn the policy on while the file has a retry_policy', async () => {
    renderPanel('schedule', NO_SCHEDULE, { ...OFF, retryPolicy: true });
    await userEvent.click(choice('Always on'));
    expect(policy('Always')).toBeDisabled();
    expect(policy('Unless stopped')).toBeDisabled();
    expect(screen.getByRole('note')).toHaveTextContent('retry_policy');
  });
});
