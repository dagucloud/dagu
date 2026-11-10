// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { useState, type ReactElement } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ScheduleKind } from '@/api/v1/schema';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import type { ScheduleView } from '../../doc';
import { CronLauncherPanel } from '../LauncherPanel';

// UTC+14 without daylight saving: unlikely to be the zone of the test host.
const SCHEDULER_TZ = 'Etc/GMT-14';

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ tz: SCHEDULER_TZ }),
}));

function renderPanel(
  expressions: string[],
  onChange: (expressions: string[]) => void = () => undefined
) {
  const schedule: ScheduleView = { present: true, expressions, others: 0 };
  render(
    <CronLauncherPanel
      schedule={schedule}
      editable
      onChange={onChange}
      onClose={() => undefined}
    />
  );
}

/** A panel that keeps the schedule it writes, as the editor does. */
function StatefulPanel({ initial }: { initial: string[] }): ReactElement {
  const [expressions, setExpressions] = useState(initial);
  return (
    <CronLauncherPanel
      schedule={{ present: true, expressions, others: 0 }}
      editable
      onChange={setExpressions}
      onClose={() => undefined}
    />
  );
}

describe('CronLauncherPanel', () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it('refuses an edit to an expression of another row', () => {
    const onChange = vi.fn();
    renderPanel(['0 * * * *', '@daily'], onChange);
    const second = screen.getAllByLabelText('Cron expression')[1]!;
    fireEvent.change(second, { target: { value: '0 * * * *' } });
    fireEvent.blur(second);
    expect(onChange).not.toHaveBeenCalled();
    expect(second).toHaveValue('@daily');
  });

  // Enter of an input method confirms its text, not the expression.
  it('ignores Enter and Escape while an input method composes', () => {
    const onChange = vi.fn();
    renderPanel(['@daily'], onChange);
    const row = screen.getByLabelText('Cron expression');
    fireEvent.change(row, { target: { value: '@hourly' } });
    fireEvent.keyDown(row, { key: 'Enter', isComposing: true });
    fireEvent.keyDown(row, { key: 'Escape', isComposing: true });
    expect(onChange).not.toHaveBeenCalled();
    expect(row).toHaveValue('@hourly');
    fireEvent.keyDown(row, { key: 'Enter' });
    expect(onChange).toHaveBeenLastCalledWith(['@hourly']);

    const added = screen.getByLabelText('New cron expression');
    fireEvent.change(added, { target: { value: '@weekly' } });
    fireEvent.keyDown(added, { key: 'Enter', isComposing: true });
    expect(onChange).toHaveBeenCalledTimes(1);
    fireEvent.keyDown(added, { key: 'Enter' });
    expect(onChange).toHaveBeenLastCalledWith(['@daily', '@weekly']);
  });

  // The server knows what the browser cannot, such as the scheduler's own
  // rules, so its runs replace the preview of a saved expression. A draft
  // still previews at once.
  it('lists the server runs and previews only the expression being typed', () => {
    render(
      <CronLauncherPanel
        schedule={{ present: true, expressions: ['0 9 * * *'], others: 0 }}
        editable
        upcomingRuns={[
          { time: '2027-01-04T09:00:00Z', scheduleIndex: 0, timezone: 'UTC' },
        ]}
        schedules={[{ expression: '0 9 * * *', kind: ScheduleKind.cron }]}
        onChange={() => undefined}
        onClose={() => undefined}
      />
    );

    expect(screen.getByText('Mon 2027-01-04 09:00')).toBeInTheDocument();
    expect(screen.queryByTestId('ve-launcher-next-runs')).toBeNull();

    fireEvent.change(screen.getByLabelText('Cron expression'), {
      target: { value: '0 10 * * *' },
    });
    expect(screen.getByTestId('ve-launcher-next-runs')).toBeInTheDocument();
  });

  // The scheduler fires in its own time zone, whatever the zone of the
  // browser.
  it('previews the next runs in the time zone of the scheduler', () => {
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date('2026-10-30T00:00:00Z'));
    renderPanel(['0 9 * * *']);
    const runs = [
      '2026-10-30T19:00:00Z',
      '2026-10-31T19:00:00Z',
      '2026-11-01T19:00:00Z',
    ].map((iso) => formatAbsoluteTime(iso, { seconds: false }));
    expect(screen.getByTestId('ve-launcher-next-runs')).toHaveTextContent(
      `Next runs: ${runs.join(', ')}`
    );
  });

  // The form is the way to a schedule without cron syntax: its controls
  // write the expression that the Add button saves.
  it('builds each kind of schedule in the form', () => {
    const onChange = vi.fn();
    renderPanel([], onChange);
    const frequency = () => screen.getByLabelText('Frequency');
    const added = screen.getByLabelText('New cron expression');

    fireEvent.change(frequency(), { target: { value: 'minutes' } });
    fireEvent.change(screen.getByLabelText('Minutes between runs'), {
      target: { value: '10' },
    });
    expect(added).toHaveValue('*/10 * * * *');

    fireEvent.change(frequency(), { target: { value: 'hourly' } });
    fireEvent.change(screen.getByLabelText('Minute of the hour'), {
      target: { value: '30' },
    });
    expect(added).toHaveValue('30 * * * *');

    fireEvent.change(frequency(), { target: { value: 'daily' } });
    fireEvent.change(screen.getByLabelText('Time of day'), {
      target: { value: '18:30' },
    });
    expect(added).toHaveValue('30 18 * * *');

    fireEvent.change(frequency(), { target: { value: 'weekly' } });
    fireEvent.click(screen.getByRole('button', { name: 'Wednesday' }));
    fireEvent.click(screen.getByRole('button', { name: 'Friday' }));
    expect(added).toHaveValue('30 18 * * 1,3,5');
    expect(screen.getByTestId('ve-schedule-summary')).toHaveTextContent(
      'Every week on Mon, Wed, Fri at 18:30'
    );

    fireEvent.change(frequency(), { target: { value: 'monthly' } });
    fireEvent.change(screen.getByLabelText('Day of the month'), {
      target: { value: '31' },
    });
    expect(added).toHaveValue('30 18 31 * *');
    expect(
      screen.getByText('A month without day 31 has no run.')
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        `Times are in the time zone of the scheduler (${SCHEDULER_TZ}).`
      )
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Add' }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith(['30 18 31 * *']);
  });

  it('keeps the last day of a weekly schedule', () => {
    renderPanel([]);
    fireEvent.change(screen.getByLabelText('Frequency'), {
      target: { value: 'weekly' },
    });
    expect(screen.getByRole('button', { name: 'Monday' })).toBeDisabled();
  });

  it('shows in the form an expression typed in the field', () => {
    renderPanel([]);
    fireEvent.change(screen.getByLabelText('New cron expression'), {
      target: { value: '0 9 * * MON-FRI' },
    });
    expect(screen.getByLabelText('Frequency')).toHaveValue('weekly');
    expect(screen.getByLabelText('Time of day')).toHaveValue('09:00');
    expect(screen.getByRole('button', { name: 'Friday' })).toHaveAttribute(
      'aria-pressed',
      'true'
    );
    expect(screen.getByRole('button', { name: 'Saturday' })).toHaveAttribute(
      'aria-pressed',
      'false'
    );
  });

  // Opening the form of a row reads the expression and writes nothing. An
  // expression outside the form stays until a frequency replaces it.
  it('leaves an expression unchanged until the form is edited', () => {
    const onChange = vi.fn();
    renderPanel(['CRON_TZ=Asia/Tokyo 0 9 * * *', '0 9 * * MON-FRI'], onChange);
    const rows = within(screen.getByLabelText('Cron expressions')).getAllByRole(
      'listitem'
    );
    const custom = within(rows[0]!);
    const weekdays = within(rows[1]!);

    fireEvent.click(custom.getByRole('button', { name: 'Edit with the form' }));
    expect(custom.getByLabelText('Frequency')).toHaveDisplayValue(
      'Custom expression'
    );
    expect(custom.queryByTestId('ve-schedule-summary')).toBeNull();

    fireEvent.click(
      weekdays.getByRole('button', { name: 'Edit with the form' })
    );
    expect(weekdays.getByLabelText('Frequency')).toHaveValue('weekly');
    expect(weekdays.getByLabelText('Cron expression')).toHaveValue(
      '0 9 * * MON-FRI'
    );
    expect(onChange).not.toHaveBeenCalled();

    fireEvent.click(weekdays.getByRole('button', { name: 'Saturday' }));
    expect(onChange).toHaveBeenCalledExactlyOnceWith([
      'CRON_TZ=Asia/Tokyo 0 9 * * *',
      '0 9 * * 1-6',
    ]);
  });

  // An edit in the form of a row is saved at once, and the form stays open
  // for the next one.
  it('keeps the form of a row open across its edits', () => {
    render(<StatefulPanel initial={['0 9 * * *']} />);
    const row = within(screen.getByLabelText('Cron expressions'));
    fireEvent.click(row.getByRole('button', { name: 'Edit with the form' }));
    fireEvent.change(row.getByLabelText('Time of day'), {
      target: { value: '10:15' },
    });
    expect(row.getByLabelText('Cron expression')).toHaveValue('15 10 * * *');
    fireEvent.change(row.getByLabelText('Frequency'), {
      target: { value: 'monthly' },
    });
    expect(row.getByLabelText('Cron expression')).toHaveValue('15 10 1 * *');
    expect(row.getByTestId('ve-schedule-summary')).toHaveTextContent(
      'Every month on day 1 at 10:15'
    );
  });

  // The open form belongs to its expression, not to its place in the list.
  it('does not open the form of the row that follows a removed row', () => {
    render(<StatefulPanel initial={['0 9 * * *', '0 18 * * *']} />);
    const list = within(screen.getByLabelText('Cron expressions'));
    fireEvent.click(
      list.getAllByRole('button', { name: 'Edit with the form' })[0]!
    );
    expect(list.getByTestId('ve-schedule-builder')).toBeInTheDocument();

    fireEvent.click(
      list.getAllByRole('button', { name: 'Remove this expression' })[0]!
    );
    expect(list.getByLabelText('Cron expression')).toHaveValue('0 18 * * *');
    expect(list.queryByTestId('ve-schedule-builder')).toBeNull();
  });
});
