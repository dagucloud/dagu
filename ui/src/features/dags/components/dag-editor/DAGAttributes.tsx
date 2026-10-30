/**
 * DAGAttributes component displays the attributes of a DAG.
 *
 * @module features/dags/components/dag-editor
 */
import dayjs from '@/lib/dayjs';
import { Calendar, CheckSquare, Settings, Tag } from 'lucide-react';
import React from 'react';
import { components } from '../../../../api/v1/schema';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  formatFireTime,
  getScheduleKey,
  getScheduleLabel,
  parseNextRun,
} from '../../../../lib/dagSchedule';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';

type Schedule = components['schemas']['Schedule'];
type UpcomingRun = components['schemas']['UpcomingRun'];
type Translate = ReturnType<typeof useI18n>['ts'];

/** Upcoming runs shown before the list is expanded. */
const COLLAPSED_UPCOMING_RUNS = 5;

/**
 * Props for the DAGAttributes component
 */
type Props = {
  /** DAG details to display */
  dag: components['schemas']['DAGDetails'];
};

/**
 * DAGAttributes displays the configuration of a DAG, such as its schedule,
 * parameters and run limits.
 */
function DAGAttributes({ dag }: Props) {
  const { locale } = useI18n();
  const dayjsLocale = toDayjsLocale(locale);
  const nextRun = parseNextRun(dag.nextRun);
  const upcomingRuns = dag.upcomingRuns ?? [];

  return (
    <div>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        {/* Schedule */}
        <div className="space-y-1 md:col-span-2">
          <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
            <Calendar className="h-4 w-4" />
            <span><I18nText text={"Schedule"} /></span>
          </div>

          {!dag.schedule?.length ? (
            <div className="text-sm text-muted-foreground italic">
              <I18nText text={"No schedule defined"} />
            </div>
          ) : (
            <div className="space-y-2">
              <div className="flex flex-wrap gap-2">
                {dag.schedule?.map((schedule, index) => (
                  <Badge
                    key={getScheduleKey(schedule, index)}
                    variant="outline"
                    title={
                      schedule.kind === 'at'
                        ? schedule.at || undefined
                        : schedule.expression || undefined
                    }
                    className="max-w-full justify-start bg-primary/10 px-2.5 py-1 text-primary border-primary/30 whitespace-nowrap normal-case tracking-normal"
                  >
                    {getScheduleLabel(schedule)}
                  </Badge>
                ))}
              </div>

              {(nextRun || upcomingRuns.length === 0) && (
                <div className="text-sm text-muted-foreground">
                  <span className="font-medium text-foreground"><I18nText text={"Next run:"} /></span>{' '}
                  {nextRun
                    ? `${dayjs(nextRun).format('YYYY-MM-DD HH:mm:ss')} (${dayjs(nextRun).locale(dayjsLocale).fromNow()})`
                    : <I18nText text="No upcoming run" />}
                </div>
              )}

              {upcomingRuns.length > 0 && (
                <UpcomingRuns
                  runs={upcomingRuns}
                  schedules={dag.schedule ?? []}
                  locale={dayjsLocale}
                />
              )}
            </div>
          )}
        </div>

        {/* Parameters */}
        {dag.params && dag.params.length > 0 && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Tag className="h-4 w-4" />
              <span><I18nText text={"Parameters"} /></span>
            </div>

            <div className="flex flex-wrap gap-2">
              {dag.params.map((param) => (
                <Badge
                  key={param}
                  variant="outline"
                  className="bg-muted text-foreground/90 px-2.5 py-1"
                >
                  {param}
                </Badge>
              ))}
            </div>
          </div>
        )}

        {/* Max Active Runs */}
        {dag.maxActiveSteps && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <Settings className="h-4 w-4" />
              <span><I18nText text={"Max Active Runs"} /></span>
            </div>

            <div className="font-medium text-foreground">
              {dag.maxActiveSteps}
            </div>
          </div>
        )}

        {/* Preconditions */}
        {dag.preconditions && dag.preconditions.length > 0 && (
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-medium text-muted-foreground mb-2">
              <CheckSquare className="h-4 w-4" />
              <span><I18nText text={"Preconditions"} /></span>
            </div>

            <div className="space-y-2">
              {dag.preconditions.map((c, index) => (
                <div
                  key={index}
                  className="flex items-center gap-2 text-xs bg-muted rounded-md p-2"
                >
                  <span className="font-medium text-foreground/90">
                    {c.condition}
                  </span>
                  <span className="text-muted-foreground">=&gt;</span>
                  <span className="text-foreground/90">{c.expected}</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

/**
 * UpcomingRuns lists the next fire times of the start schedules in the
 * wall-clock time of the zone each schedule runs in.
 */
function UpcomingRuns({
  runs,
  schedules,
  locale,
}: {
  runs: UpcomingRun[];
  schedules: Schedule[];
  locale: string;
}) {
  const { ts } = useI18n();
  const [expanded, setExpanded] = React.useState(false);
  const zones = new Set(runs.map((run) => run.timezone));
  const sharedZone = zones.size === 1 ? runs[0]?.timezone : undefined;
  const visibleRuns = expanded ? runs : runs.slice(0, COLLAPSED_UPCOMING_RUNS);

  return (
    <div className="space-y-1">
      <div className="text-sm font-medium text-foreground">
        <I18nText text="Upcoming runs" />
        {sharedZone && (
          <span className="ml-1 font-normal text-muted-foreground">
            ({sharedZone})
          </span>
        )}
      </div>
      <ol className="space-y-0.5 text-sm text-muted-foreground tabular-nums">
        {visibleRuns.map((run) => {
          const schedule = schedules[run.scheduleIndex];
          const notes = upcomingRunNotes(run, locale, ts);
          return (
            <li key={run.time}>
              {formatFireTime(run.time, 'ddd YYYY-MM-DD HH:mm', locale)}
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
      {runs.length > COLLAPSED_UPCOMING_RUNS && (
        <Button
          variant="link"
          size="xs"
          className="px-0"
          onClick={() => setExpanded((value) => !value)}
        >
          <I18nText text={expanded ? 'Show less' : 'Show more'} />
        </Button>
      )}
    </div>
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

function toDayjsLocale(locale: string): string {
  return locale === 'zh-CN' ? 'zh-cn' : locale;
}

export default DAGAttributes;
