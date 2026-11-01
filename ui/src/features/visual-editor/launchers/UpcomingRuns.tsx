// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, type ReactElement } from 'react';
import { UpcomingRunsHold, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { ScheduleCalendarLinks } from '@/features/calendars/components/ScheduleCalendarLinks';
import dayjs from '@/lib/dayjs';
import { formatFireTime, getScheduleLabel } from '@/lib/dagSchedule';

type Schedule = components['schemas']['Schedule'];
type UpcomingRun = components['schemas']['UpcomingRun'];
type Translate = ReturnType<typeof useI18n>['ts'];

/** Upcoming runs shown before the list is expanded. */
const COLLAPSED_UPCOMING_RUNS = 5;

/**
 * The next times the start schedules fire, as the server computes them. A
 * time reads in the zone of its schedule; the zone heads the list when all
 * runs share it. A run placed by a business calendar says why.
 */
export function UpcomingRuns({
  runs,
  hold,
  schedules,
}: {
  runs: UpcomingRun[];
  /** Why none of the runs will start, if they will not. */
  hold?: UpcomingRunsHold;
  /** The schedules that runs index. */
  schedules: Schedule[];
}): ReactElement {
  const { ts, locale } = useI18n();
  const dayjsLocale = locale === 'zh-CN' ? 'zh-cn' : locale;
  const [expanded, setExpanded] = useState(false);
  const zones = new Set(runs.map((run) => run.timezone));
  const sharedZone = zones.size === 1 ? runs[0]?.timezone : undefined;
  const visibleRuns = expanded ? runs : runs.slice(0, COLLAPSED_UPCOMING_RUNS);

  return (
    <section className="space-y-1" aria-label={ts('Upcoming runs')}>
      <h3 className="text-sm font-medium text-foreground">
        {ts('Upcoming runs')}
        {sharedZone && (
          <span className="ml-1 font-normal text-muted-foreground">
            ({sharedZone})
          </span>
        )}
      </h3>
      <ScheduleCalendarLinks schedules={schedules} />
      {hold && (
        <p className="text-sm text-warning">
          {ts(
            hold === UpcomingRunsHold.suspended
              ? 'This DAG is suspended. None of these runs will start until it is resumed.'
              : 'Scheduler is paused. No scheduled runs will start.'
          )}
        </p>
      )}
      {runs.length === 0 ? (
        <p className="text-sm italic text-muted-foreground">
          {ts('No upcoming run')}
        </p>
      ) : (
        <ol className="space-y-0.5 whitespace-normal break-words text-sm text-muted-foreground tabular-nums">
          {visibleRuns.map((run) => {
            const schedule = schedules[run.scheduleIndex];
            const notes = upcomingRunNotes(run, dayjsLocale, ts);
            return (
              <li key={`${run.time}-${run.scheduleIndex}`}>
                {formatFireTime(run.time, 'ddd YYYY-MM-DD HH:mm', dayjsLocale)}
                {!sharedZone && ` ${run.timezone}`}
                {schedules.length > 1 && schedule && (
                  <span className="text-muted-foreground/80">
                    {' · '}
                    {scheduleSummary(schedule)}
                  </span>
                )}
                {notes.length > 0 && (
                  <span className="text-foreground/80">
                    {' · '}
                    {notes.join('; ')}
                  </span>
                )}
              </li>
            );
          })}
        </ol>
      )}
      {runs.length > COLLAPSED_UPCOMING_RUNS && (
        <Button
          variant="link"
          size="xs"
          className="px-0"
          onClick={() => setExpanded((value) => !value)}
        >
          {ts(expanded ? 'Show less' : 'Show more')}
        </Button>
      )}
    </section>
  );
}

// upcomingRunNotes explains where a business calendar put a run: the
// business day it matched, and the holidays it skipped to get there.
function upcomingRunNotes(
  run: UpcomingRun,
  locale: string,
  ts: Translate
): string[] {
  const notes: string[] = [];
  const businessDay = run.businessDay ?? 0;
  if (businessDay === -1) {
    notes.push(ts('last business day of the month'));
  } else if (businessDay < -1) {
    notes.push(
      ts("business day {n} from the month's end", { n: -businessDay })
    );
  } else if (businessDay > 0) {
    notes.push(ts('business day {n} of the month', { n: businessDay }));
  }
  if (run.extraBusinessDay) {
    notes.push(ts('extra business day'));
  }
  for (const holiday of run.skippedHolidays ?? []) {
    const day = dayjs(holiday.date).locale(locale).format('ddd YYYY-MM-DD');
    notes.push(
      ts('skips {day}', {
        day: holiday.name ? `${day} (${holiday.name})` : day,
      })
    );
  }
  return notes;
}

function scheduleSummary(schedule: Schedule): string {
  const label = getScheduleLabel(schedule);
  return schedule.profile ? `${label} (${schedule.profile})` : label;
}
