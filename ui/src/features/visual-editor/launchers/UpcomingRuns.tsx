// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, type ReactElement } from 'react';
import type { components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { useI18n } from '@/i18n/I18nProvider';
import { formatFireTime, getScheduleLabel } from '@/lib/dagSchedule';

type Schedule = components['schemas']['Schedule'];
type UpcomingRun = components['schemas']['UpcomingRun'];

/** Upcoming runs shown before the list is expanded. */
const COLLAPSED_UPCOMING_RUNS = 5;

/**
 * The next times the start schedules fire, as the server computes them. A
 * time reads in the zone of its schedule; the zone heads the list when all
 * runs share it.
 */
export function UpcomingRuns({
  runs,
  schedules,
}: {
  runs: UpcomingRun[];
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
      {runs.length === 0 ? (
        <p className="text-sm italic text-muted-foreground">
          {ts('No upcoming run')}
        </p>
      ) : (
        <ol className="space-y-0.5 whitespace-normal break-words text-sm text-muted-foreground tabular-nums">
          {visibleRuns.map((run) => {
            const schedule = schedules[run.scheduleIndex];
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

function scheduleSummary(schedule: Schedule): string {
  const label = getScheduleLabel(schedule);
  return schedule.profile ? `${label} (${schedule.profile})` : label;
}
