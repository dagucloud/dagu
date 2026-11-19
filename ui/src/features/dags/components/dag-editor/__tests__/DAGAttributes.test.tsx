// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { RestartPolicy, ScheduleKind } from '@/api/v1/schema';
import DAGAttributes from '../DAGAttributes';

afterEach(() => {
  cleanup();
});

describe('DAGAttributes', () => {
  // A graph without a start step, such as an agent DAG, draws no launcher:
  // the overview is where its schedule shows.
  it('shows the schedule and its next run', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'scheduled-dag',
          schedule: [{ expression: '0 12 * * *', kind: ScheduleKind.cron }],
          nextRun: '2026-04-03T12:00:00Z',
        }}
      />
    );

    expect(screen.getByText('Schedule')).toBeInTheDocument();
    expect(screen.getByText('0 12 * * *')).toBeInTheDocument();
    expect(screen.getByText('Next run:').parentElement).toHaveTextContent(
      '2026-04-03 12:00:00 +00:00'
    );
  });

  // Two schedules of one expression differ only by their overrides.
  it('shows the profile and overrides of a schedule', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'overrides',
          schedule: [
            {
              expression: '0 5 * * *',
              kind: ScheduleKind.cron,
              profile: 'prod',
              paramOverrides: { mode: 'y', count: '2', region: 'eu' },
            },
          ],
        }}
      />
    );

    const label = screen.getByText('0 5 * * * (prod)');
    expect(screen.getByText('· mode: y, count: 2, +1')).toBeInTheDocument();
    expect(label.closest('[title]')).toHaveAttribute(
      'title',
      '0 5 * * *\nmode: y\ncount: 2\nregion: eu'
    );
  });

  it('warns on a schedule that another one takes precedence over', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'coinciding',
          schedule: [
            {
              expression: '0 * * * *',
              kind: ScheduleKind.cron,
              paramOverrides: { mode: 'x' },
            },
            {
              expression: '0 5 * * *',
              kind: ScheduleKind.cron,
              paramOverrides: { mode: 'y' },
            },
          ],
          scheduleCoincidences: [
            {
              scheduleIndex: 1,
              precedingScheduleIndex: 0,
              time: '2026-11-23T05:00:00+09:00',
              timezone: 'Asia/Tokyo',
            },
          ],
        }}
      />
    );

    const warnings = screen.getAllByRole('img');
    expect(warnings).toHaveLength(1);
    expect(warnings[0]).toHaveAccessibleName(
      'Does not start at Mon 2026-11-23 05:00 Asia/Tokyo: 0 * * * * · mode: x is due then too and takes precedence.'
    );
    expect(screen.getByText('0 5 * * *').closest('[title]')).toHaveAttribute(
      'title',
      expect.stringContaining('Does not start at Mon 2026-11-23 05:00')
    );
  });

  it('renders the restart policy of a service DAG', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'service-dag',
          restartPolicy: RestartPolicy.unless_stopped,
          restartWaitSec: 15,
        }}
      />
    );

    expect(
      screen.getByText('Restart: unless-stopped · 15s')
    ).toBeInTheDocument();
    expect(screen.getByText('Schedule')).toBeInTheDocument();
  });

  it('renders no restart policy for other DAGs', () => {
    render(<DAGAttributes dag={{ name: 'plain-dag' }} />);

    expect(screen.queryByText(/^Restart:/)).not.toBeInTheDocument();
  });

  // The server writes the next run in the zone of the schedule: it shows
  // there, with its offset, whatever the zone of the browser.
  it('shows the next run in the zone of its schedule', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'tokyo',
          schedule: [{ expression: '0 21 * * *', kind: ScheduleKind.cron }],
          nextRun: '2026-04-03T21:00:00+09:00',
        }}
      />
    );

    expect(screen.getByText('Next run:').parentElement).toHaveTextContent(
      '2026-04-03 21:00:00 +09:00'
    );
  });

  it('shows no schedule block without a schedule', () => {
    render(<DAGAttributes dag={{ name: 'manual-dag' }} />);

    expect(screen.queryByText('Schedule')).toBeNull();
    expect(screen.queryByText('Next run:')).toBeNull();
  });

  it('labels the concurrency limit as steps, not runs', () => {
    render(
      <DAGAttributes
        dag={{ name: 'limits', maxActiveRuns: 1, maxActiveSteps: 10 }}
      />
    );

    expect(screen.getByText('Max Active Steps')).toBeInTheDocument();
    expect(screen.getByText('10')).toBeInTheDocument();
    expect(screen.queryByText('Max Active Runs')).toBeNull();
  });

  it('lists the labels and the names of the environment variables', () => {
    render(
      <DAGAttributes
        dag={{
          name: 'labelled',
          labels: ['team=data', 'nightly'],
          tags: ['nightly'],
          env: ['API_TOKEN=s3cret', 'MODE=prod'],
        }}
      />
    );

    expect(screen.getByText('Labels')).toBeInTheDocument();
    expect(screen.getAllByText('nightly')).toHaveLength(1);
    expect(screen.getByText('team=data')).toBeInTheDocument();
    expect(screen.getByText('Environment variables')).toBeInTheDocument();
    expect(screen.getByText('API_TOKEN')).toBeInTheDocument();
    expect(screen.getByText('MODE')).toBeInTheDocument();
    // A value may be a secret: only names show.
    expect(screen.queryByText(/s3cret/)).toBeNull();
  });
});
