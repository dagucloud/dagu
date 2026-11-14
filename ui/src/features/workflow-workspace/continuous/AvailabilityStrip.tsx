// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ReactElement, ReactNode } from 'react';
import type { components } from '@/api/v1/schema';
import { CompactCount } from '@/components/ui/compact-count';
import { RelativeTime } from '@/components/ui/relative-time';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { formatExactCount } from '@/lib/formatCompactCount';
import { formatAvailability } from './format';

type StepAvailability = components['schemas']['StepAvailability'];

const WINDOWS = ['24h', '7d', '30d', '365d'] as const;

function Fact({
  label,
  testId,
  children,
}: {
  label: string;
  testId: string;
  children: ReactNode;
}): ReactElement {
  return (
    <div data-testid={testId} className="min-w-0">
      <dt className="truncate text-[11px] text-muted-foreground">{label}</dt>
      <dd className="truncate text-sm tabular-nums text-foreground">
        {children}
      </dd>
    </div>
  );
}

/**
 * The availability of the four windows, the current streak, the last failure
 * and the count of executions of a continuous step. `since` is the start of
 * the streak when no sample failed: the start of the step.
 */
export function AvailabilityStrip({
  data,
  since,
}: {
  data: StepAvailability;
  since?: string;
}): ReactElement {
  const { ts, locale } = useI18n();
  const streakSince = data.lastFailureAt ?? since;
  return (
    <dl
      data-testid="continuous-availability"
      className="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-4 lg:grid-cols-8"
    >
      {WINDOWS.map((window) => {
        const entry = data.windows.find((w) => w.window === window);
        return (
          <Fact
            key={window}
            testId={`continuous-availability-${window}`}
            label={ts('Availability {window}', { window })}
          >
            <span
              title={
                entry
                  ? ts('{failures} failed of {count} executions', {
                      failures: formatExactCount(entry.failures, locale),
                      count: formatExactCount(entry.count, locale),
                    })
                  : undefined
              }
            >
              {formatAvailability(entry?.availability, locale)}
            </span>
          </Fact>
        );
      })}
      <Fact testId="continuous-streak" label={ts('Current streak')}>
        <CompactCount value={data.streak} />
        {streakSince && (
          <span className="text-[11px] text-muted-foreground">
            {' '}
            {ts('since {time}', {
              time: formatAbsoluteTime(streakSince, { seconds: false }) ?? '',
            })}
          </span>
        )}
      </Fact>
      <Fact testId="continuous-last-failure" label={ts('Last failure')}>
        {data.lastFailureAt ? (
          <RelativeTime
            timestamp={data.lastFailureAt}
            absolute={formatAbsoluteTime(data.lastFailureAt) ?? undefined}
          />
        ) : (
          ts('None')
        )}
      </Fact>
      <Fact testId="continuous-total" label={ts('Executions')}>
        <CompactCount value={data.totalCount} />
      </Fact>
    </dl>
  );
}
