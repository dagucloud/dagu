// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
import { PeekContext, type PeekController } from '@/features/peek/PeekContext';
import { formatDurationMs } from '@/lib/formatDuration';
import type { ActivityRun } from '../../lib/activityModel';
import { ActivityLanes } from '../ActivityLanes';

const FROM = 1_790_000_000 - (1_790_000_000 % 900);
const TO = FROM + 96 * 900;

// Loaded runs, newest first, as the API lists them.
const runs: ActivityRun[] = [
  {
    name: 'billing',
    dagRunId: 'billing-3',
    status: Status.Failed,
    startedAt: FROM + 1800,
  },
  {
    name: 'billing',
    dagRunId: 'billing-2',
    status: Status.Failed,
    startedAt: FROM + 1500,
  },
  {
    name: 'report',
    dagRunId: 'report-2',
    status: Status.Success,
    startedAt: FROM + 900,
  },
  {
    name: 'billing',
    dagRunId: 'billing-1',
    status: Status.Success,
    startedAt: FROM + 600,
  },
  {
    name: 'report',
    dagRunId: 'report-1',
    status: Status.Success,
    startedAt: FROM,
  },
];

// A run still going from before the window, as the page appends it.
const backfill: ActivityRun = {
  name: 'backfill',
  dagRunId: 'backfill-1',
  status: Status.Running,
  startedAt: FROM - 3600,
};

function LocationProbe() {
  const location = useLocation();
  return (
    <div data-testid="location">{`${location.pathname}${location.search}`}</div>
  );
}

// jsdom lays nothing out, so pointer tests give a lane an explicit width.
function stubLaneWidth(lane: HTMLElement, width: number) {
  lane.getBoundingClientRect = () =>
    ({
      left: 0,
      top: 0,
      right: width,
      bottom: 36,
      width,
      height: 36,
      x: 0,
      y: 0,
      toJSON: () => ({}),
    }) as DOMRect;
}

function renderLanes({
  laneRuns = runs,
  hasMore = false,
  peek = null,
}: {
  laneRuns?: ActivityRun[];
  hasMore?: boolean;
  /** A peek host above the lanes. */
  peek?: PeekController | null;
} = {}) {
  return render(
    <MemoryRouter initialEntries={['/']}>
      <Routes>
        <Route
          path="/"
          element={
            <ActivityLanes
              runs={laneRuns}
              active={[]}
              window={{ from: FROM, to: TO }}
              hasMore={hasMore}
              range={{ from: FROM, to: TO }}
              nowSeconds={TO - 60}
              remoteNode="local"
              onZoom={vi.fn()}
            />
          }
        />
        <Route path="*" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
    {
      wrapper: ({ children }) => (
        <PeekContext.Provider value={peek}>{children}</PeekContext.Provider>
      ),
    }
  );
}

// A plain open goes to the run on its workflow page.
function expectRunPage(name: string, dagRunId: string) {
  expect(screen.getByTestId('location')).toHaveTextContent(
    `/dags/${name}?remoteNode=local&run=${dagRunId}`
  );
}

describe('ActivityLanes', () => {
  it('lists failing workflows first with their failure share', () => {
    renderLanes();

    const lanes = within(
      screen.getByRole('list', { name: 'Runs by workflow' })
    ).getAllByRole('listitem');
    expect(lanes[0]).toHaveTextContent('billing');
    expect(lanes[0]).toHaveTextContent('2 of 3 failed');
    expect(lanes[1]).toHaveTextContent('report');
    expect(lanes[1]).toHaveTextContent('2 runs');
  });

  // Entering a lane selects its newest run; arrows step through the rest.
  it('moves between a lane’s runs with the keyboard and opens one', () => {
    renderLanes();
    const lane = screen.getByRole('group', { name: /^report:/ });

    lane.focus();
    fireEvent.keyDown(lane, { key: 'ArrowRight' });
    expect(screen.getByRole('tooltip')).toHaveTextContent('report');
    fireEvent.keyDown(lane, { key: 'Enter' });

    expectRunPage('report', 'report-2');
  });

  it('shows the run in the peek under a peek host', () => {
    const peek = { open: vi.fn(), isOpen: false };
    renderLanes({ peek });
    const lane = screen.getByRole('group', { name: /^report:/ });

    lane.focus();
    fireEvent.keyDown(lane, { key: 'ArrowRight' });
    fireEvent.keyDown(lane, { key: 'Enter' });

    expect(peek.open).toHaveBeenCalledWith({ dag: 'report', run: 'report-2' });
    expect(screen.getByRole('group', { name: /^report:/ })).toBeVisible();
  });

  it('opens a long run from anywhere along its bar', () => {
    renderLanes({
      laneRuns: [
        {
          name: 'nightly',
          dagRunId: 'nightly-1',
          status: Status.Success,
          startedAt: FROM,
          finishedAt: FROM + 48 * 900,
        },
      ],
    });
    const lane = screen.getByRole('group', { name: /^nightly:/ });
    stubLaneWidth(lane, 960);

    fireEvent.pointerDown(lane, { button: 0, clientX: 240 });
    fireEvent.pointerUp(lane, { clientX: 240 });

    expectRunPage('nightly', 'nightly-1');
  });

  // A mark is drawn at least a few pixels wide, so a click inside a short
  // run's mark picks it over the longer run drawn beneath it.
  it.each([
    ['a queued run', 'etl-3', 481],
    ['a short finished run', 'etl-2', 241.5],
  ])('opens %s drawn over a longer run', (_, dagRunId, clientX) => {
    renderLanes({
      laneRuns: [
        {
          name: 'etl',
          dagRunId: 'etl-3',
          status: Status.Queued,
          startedAt: FROM + 48 * 900,
        },
        {
          name: 'etl',
          dagRunId: 'etl-2',
          status: Status.Success,
          startedAt: FROM + 24 * 900,
          finishedAt: FROM + 24 * 900 + 10,
        },
        {
          name: 'etl',
          dagRunId: 'etl-1',
          status: Status.Running,
          startedAt: FROM,
        },
      ],
    });
    const lane = screen.getByRole('group', { name: /^etl:/ });
    stubLaneWidth(lane, 960);

    fireEvent.pointerDown(lane, { button: 0, clientX });
    fireEvent.pointerUp(lane, { clientX });

    expectRunPage('etl', dagRunId);
  });

  // Problem runs draw taller, so a failed run under a later run shows above
  // and below it.
  it.each([
    ['a failed run where it shows past a later one', 'deploy-1', 4],
    ['the later run drawn over it', 'deploy-2', 18],
  ])('opens %s', (_, dagRunId, clientY) => {
    renderLanes({
      laneRuns: [
        {
          name: 'deploy',
          dagRunId: 'deploy-2',
          status: Status.Success,
          startedAt: FROM + 12 * 900,
          finishedAt: FROM + 36 * 900,
        },
        {
          name: 'deploy',
          dagRunId: 'deploy-1',
          status: Status.Failed,
          startedAt: FROM,
          finishedAt: FROM + 48 * 900,
        },
      ],
    });
    const lane = screen.getByRole('group', { name: /^deploy:/ });
    stubLaneWidth(lane, 960);

    fireEvent.pointerDown(lane, { button: 0, clientX: 240, clientY });
    fireEvent.pointerUp(lane, { clientX: 240, clientY });

    expectRunPage('deploy', dagRunId);
  });

  it('shows a run that started before the window', () => {
    renderLanes({ laneRuns: [backfill] });
    const lane = screen.getByRole('group', { name: /^backfill:/ });
    stubLaneWidth(lane, 960);

    lane.focus();
    fireEvent.keyDown(lane, { key: 'ArrowRight' });
    expect(screen.getByRole('tooltip')).toHaveTextContent('Running');
    // A running run shows how long it has run so far.
    expect(screen.getByRole('tooltip')).toHaveTextContent(
      formatDurationMs((TO - 60 - (FROM - 3600)) * 1000)
    );

    fireEvent.pointerDown(lane, { button: 0, clientX: 4 });
    fireEvent.pointerUp(lane, { clientX: 4 });
    expectRunPage('backfill', 'backfill-1');
  });

  // Older pages hold only runs created in the window, so a run from before
  // it leaves the unloaded time shaded.
  it('shades the unloaded time when a run began before the window', () => {
    renderLanes({
      laneRuns: [
        {
          name: 'backfill',
          dagRunId: 'backfill-2',
          status: Status.Success,
          startedAt: FROM + 24 * 900,
        },
        backfill,
      ],
      hasMore: true,
    });

    expect(screen.getByTestId('unloaded-runs')).toHaveStyle({ width: '25%' });
  });
});
