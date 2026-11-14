// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { memo, useState, type ReactElement } from 'react';
import { NodeStatus, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { PanelMessage } from '@/components/ui/panel-message';
import { RelativeTime } from '@/components/ui/relative-time';
import { StatusDot } from '@/components/ui/status-dot';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { formatExactCount } from '@/lib/formatCompactCount';
import { sameContent } from '../data/sameContent';
import { formatSpanMs } from './format';
import {
  useReadAtEnd,
  useStepEventsPage,
  type ContinuousTarget,
} from './useContinuousData';

type StepEvent = components['schemas']['StepEvent'];
type Window = { from: number; to: number } | null;

/** The events that one request asks for. */
export const EVENTS_PAGE = 50;

/** The status whose token colours an event kind. */
const KIND_STATUS: Record<StepEvent['kind'], NodeStatus> = {
  started: NodeStatus.NotStarted,
  stopped: NodeStatus.Aborted,
  failed: NodeStatus.Failed,
  recovered: NodeStatus.Success,
};

const KIND_LABEL: Record<StepEvent['kind'], string> = {
  started: 'Started',
  stopped: 'Stopped',
  failed: 'Failed',
  recovered: 'Recovered',
};

// A poll hands out a new copy of each event: the row renders only for a change.
const EventRow = memo(
  function EventRow({ event }: { event: StepEvent }): ReactElement {
    const { ts, locale } = useI18n();
    const kind = ts(KIND_LABEL[event.kind]);
    const facts: string[] = [];
    if (event.exitCode !== undefined && event.kind === 'failed') {
      facts.push(ts('exit {code}', { code: event.exitCode }));
    }
    if (event.failedCount !== undefined && event.failedCount > 0) {
      facts.push(
        ts('{count} failed', {
          count: formatExactCount(event.failedCount, locale),
        })
      );
    }
    if (event.outageSeconds !== undefined) {
      facts.push(
        ts('outage {length}', {
          length: formatSpanMs(event.outageSeconds * 1000),
        })
      );
    }
    return (
      <li
        data-testid="continuous-event"
        data-kind={event.kind}
        className="flex min-w-0 items-baseline gap-2 py-1 text-xs"
      >
        <StatusDot
          kind="step"
          status={KIND_STATUS[event.kind]}
          label={kind}
          decorative
          className="self-center"
        />
        <span className="w-20 shrink-0 font-medium text-foreground">
          {kind}
        </span>
        <RelativeTime
          timestamp={event.time}
          absolute={formatAbsoluteTime(event.time) ?? undefined}
          className="shrink-0 text-muted-foreground"
        />
        {facts.length > 0 && (
          <span className="shrink-0 tabular-nums text-muted-foreground">
            {facts.join(' · ')}
          </span>
        )}
        {event.message && (
          <span
            className="min-w-0 truncate font-mono text-muted-foreground"
            title={event.message}
          >
            {event.message}
          </span>
        )}
      </li>
    );
  },
  (prev, next) => sameContent(prev.event, next.event)
);

/**
 * One page of events and, below it, the next pages that the user asked for.
 * Each page is its own query, so a page that polls does not move the others.
 */
function EventsPage({
  target,
  window,
  before,
  poll,
}: {
  target: ContinuousTarget;
  window: Window;
  before?: string;
  poll: boolean;
}): ReactElement {
  const { ts } = useI18n();
  const [more, setMore] = useState(false);
  const { data, error, mutate } = useStepEventsPage(
    target,
    { before, window, limit: EVENTS_PAGE },
    poll
  );
  useReadAtEnd(poll, mutate);
  if (error && !data) {
    return (
      <PanelMessage tone="error" align="start" className="p-0">
        {ts('Could not load {thing}', { thing: ts('the events of this step') })}
      </PanelMessage>
    );
  }
  if (!data) {
    return (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('Loading events...')}
      </PanelMessage>
    );
  }
  if (data.events.length === 0 && !before) {
    return (
      <PanelMessage tone="muted" align="start" className="p-0">
        {ts('No events yet')}
      </PanelMessage>
    );
  }
  return (
    <>
      <ul className="divide-y divide-border">
        {data.events.map((event) => (
          <EventRow key={`${event.time}:${event.kind}`} event={event} />
        ))}
      </ul>
      {data.nextCursor &&
        (more ? (
          <EventsPage
            target={target}
            window={window}
            before={data.nextCursor}
            poll={false}
          />
        ) : (
          <Button
            type="button"
            variant="ghost"
            size="xs"
            data-testid="continuous-events-more"
            onClick={() => setMore(true)}
          >
            {ts('Load more')}
          </Button>
        ))}
    </>
  );
}

/**
 * The events of a continuous step, newest first, with the cursor of the API.
 * `window` limits the list to the times of a bucket of the chart.
 */
export function EventsList({
  target,
  window,
  poll,
}: {
  target: ContinuousTarget;
  window: Window;
  poll: boolean;
}): ReactElement {
  // A new window starts a new list: the key drops the pages of the old one.
  const key = window ? `${window.from}-${window.to}` : 'all';
  return (
    <div data-testid="continuous-events">
      <EventsPage key={key} target={target} window={window} poll={poll} />
    </div>
  );
}
