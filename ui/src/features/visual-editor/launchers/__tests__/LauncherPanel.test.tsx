// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
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
});
