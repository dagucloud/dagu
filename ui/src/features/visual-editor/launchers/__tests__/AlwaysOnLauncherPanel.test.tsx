// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import type { RestartView, ScheduleView } from '../../doc';
import { AlwaysOnLauncherPanel, CronLauncherPanel } from '../LauncherPanel';

// The Cron panel reads the time zone of the scheduler.
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: 'UTC' }),
}));

const OFF: RestartView = {
  policy: 'no',
  waitSec: null,
  histRetentionRuns: null,
  schedule: false,
  retryPolicy: false,
};
const ON: RestartView = {
  ...OFF,
  policy: 'always',
  waitSec: 5,
  histRetentionRuns: 20,
};

function renderPanel(restart: RestartView, editable = true) {
  const onChange = vi.fn();
  render(
    <UserPreferencesProvider>
      <I18nProvider>
        <AlwaysOnLauncherPanel
          restart={restart}
          editable={editable}
          onChange={onChange}
          onClose={vi.fn()}
        />
      </I18nProvider>
    </UserPreferencesProvider>
  );
  return onChange;
}

const radio = (name: string) => screen.getByRole('radio', { name });
const wait = () => screen.getByLabelText('Wait before a new run (s)');
const runs = () => screen.getByLabelText('Runs kept in the history');

describe('AlwaysOnLauncherPanel', () => {
  it('shows the three policies and the current one', () => {
    renderPanel(ON);
    expect(radio('Off')).toHaveAttribute('aria-checked', 'false');
    expect(radio('Always')).toHaveAttribute('aria-checked', 'true');
    expect(radio('Unless stopped')).toHaveAttribute('aria-checked', 'false');
    expect(wait()).toHaveValue('5');
    expect(runs()).toHaveValue('20');
  });

  it('shows no field while the policy is off', () => {
    renderPanel(OFF);
    expect(radio('Off')).toHaveAttribute('aria-checked', 'true');
    expect(screen.queryByLabelText('Wait before a new run (s)')).toBeNull();
  });

  // A service needs a wait of 1 s or more: the new policy brings one.
  it('gives a wait of 5 s to a policy that has none', async () => {
    const onChange = renderPanel(OFF);
    await userEvent.click(radio('Unless stopped'));
    expect(onChange).toHaveBeenCalledWith({
      policy: 'unless-stopped',
      waitSec: 5,
    });
  });

  it('keeps the wait of the file when the policy turns on', async () => {
    const onChange = renderPanel({ ...OFF, waitSec: 12 });
    await userEvent.click(radio('Always'));
    expect(onChange).toHaveBeenCalledWith({ policy: 'always' });
  });

  it('changes the policy of a service without a new wait', async () => {
    const onChange = renderPanel(ON);
    await userEvent.click(radio('Unless stopped'));
    expect(onChange).toHaveBeenCalledWith({ policy: 'unless-stopped' });
  });

  it('commits a wait on Enter and a retention on blur', async () => {
    const onChange = renderPanel(ON);
    await userEvent.clear(wait());
    await userEvent.type(wait(), '30{Enter}');
    expect(onChange).toHaveBeenLastCalledWith({ waitSec: 30 });
    await userEvent.clear(runs());
    await userEvent.type(runs(), '50');
    await userEvent.tab();
    expect(onChange).toHaveBeenLastCalledWith({ histRetentionRuns: 50 });
  });

  it('empties the retention to remove it, but not the wait', async () => {
    const onChange = renderPanel(ON);
    await userEvent.clear(runs());
    await userEvent.tab();
    expect(onChange).toHaveBeenLastCalledWith({ histRetentionRuns: null });
    onChange.mockClear();
    await userEvent.clear(wait());
    await userEvent.tab();
    expect(onChange).not.toHaveBeenCalled();
    expect(wait()).toHaveValue('5');
  });

  it('puts back a value that is not a positive integer', async () => {
    const onChange = renderPanel(ON);
    for (const bad of ['0', '-3', '1.5', 'abc']) {
      await userEvent.clear(wait());
      await userEvent.type(wait(), `${bad}{Enter}`);
      expect(wait()).toHaveValue('5');
    }
    expect(onChange).not.toHaveBeenCalled();
  });

  it('removes the policy', async () => {
    const onChange = renderPanel(ON);
    await userEvent.click(
      screen.getByRole('button', { name: 'Remove the restart policy' })
    );
    expect(onChange).toHaveBeenCalledWith({ policy: 'no' });
  });

  // Spec 080: a service has no schedule and no retry_policy. The slot panel
  // takes care of the schedule (it switches), this panel of the retry_policy.
  it('does not turn the policy on while the file has a retry_policy', () => {
    renderPanel({ ...OFF, retryPolicy: true });
    expect(radio('Always')).toBeDisabled();
    expect(screen.getByRole('note')).toHaveTextContent('retry_policy');
  });

  it('keeps the policy changeable when the file also has a schedule', () => {
    renderPanel({ ...ON, schedule: true });
    expect(radio('Unless stopped')).toBeEnabled();
    expect(screen.queryByRole('note')).toBeNull();
  });

  it('is read only when the file cannot be edited', () => {
    renderPanel(ON, false);
    expect(radio('Unless stopped')).toBeDisabled();
    expect(wait()).toHaveAttribute('readonly');
    expect(
      screen.queryByRole('button', { name: 'Remove the restart policy' })
    ).toBeNull();
  });
});

describe('CronLauncherPanel with Always on', () => {
  const schedule: ScheduleView = {
    present: false,
    expressions: [],
    others: 0,
  };

  function renderCron(alwaysOn: boolean) {
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <CronLauncherPanel
            schedule={schedule}
            editable
            alwaysOn={alwaysOn}
            onChange={vi.fn()}
            onClose={vi.fn()}
          />
        </I18nProvider>
      </UserPreferencesProvider>
    );
  }

  it('explains why a schedule is not available and offers no way to add one', () => {
    renderCron(true);
    expect(screen.getByRole('note')).toHaveTextContent(
      'Not available with Always on'
    );
    expect(screen.queryByLabelText('New cron expression')).toBeNull();
  });

  it('is unchanged without the policy', () => {
    renderCron(false);
    expect(screen.queryByRole('note')).toBeNull();
    expect(screen.getByLabelText('New cron expression')).toBeInTheDocument();
  });
});
