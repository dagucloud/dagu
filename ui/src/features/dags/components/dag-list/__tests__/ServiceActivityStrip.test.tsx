// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import { I18nProvider } from '@/i18n/I18nProvider';
import ServiceActivityStrip from '../ServiceActivityStrip';

type Activity = components['schemas']['DAGListActivity'];

// The detail of the strip reads the availability only while it is open.
const h = vi.hoisted(() => ({
  asked: [] as Array<{ path: string; init: unknown }>,
  uptime: undefined as number | undefined,
}));
vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: vi.fn() }),
  useQuery: (path: string, init: unknown) => {
    h.asked.push({ path, init });
    return {
      data:
        init === null
          ? undefined
          : {
              windows: [
                {
                  window: '7d',
                  count: 100,
                  failures: 1,
                  availability: 0.99,
                  ...(h.uptime === undefined ? {} : { uptime: h.uptime }),
                },
              ],
              streak: 5,
              lastFailureAt: '2026-10-29T10:00:00Z',
              totalCount: 100,
            },
    };
  },
}));

const NOW = Date.parse('2026-10-29T12:30:00Z');
const HOUR = 3_600_000;

function bucket(start: number, count: number, failures = 0, upMs?: number) {
  return {
    ...(upMs === undefined ? {} : { upMs }),
    time: new Date(start).toISOString(),
    count,
    failures,
    minMs: 1,
    maxMs: 5,
    p50Ms: 2,
    p95Ms: 4,
  };
}

function renderStrip(
  activity: Activity | undefined,
  fluid = false,
  fileName?: string
) {
  return render(
    <UserPreferencesProvider>
      <I18nProvider>
        <ServiceActivityStrip
          activity={activity}
          fluid={fluid}
          fileName={fileName}
        />
      </I18nProvider>
    </UserPreferencesProvider>
  );
}

beforeEach(() => {
  h.asked.length = 0;
  h.uptime = undefined;
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(NOW);
});
afterEach(() => {
  vi.useRealTimers();
});

describe('ServiceActivityStrip', () => {
  const activity: Activity = {
    runs: [],
    truncated: false,
    resolutionSeconds: 3600,
    buckets: [
      bucket(NOW - 3 * HOUR, 1_200, 0),
      bucket(NOW - HOUR, 800, 20),
      bucket(NOW - 1000, 10, 10),
    ],
  };

  it('draws one cell for each slot, never one for each run', () => {
    renderStrip(activity);
    const strip = screen.getByTestId('dag-activity');
    expect(strip.children).toHaveLength(24);
    expect(strip.querySelectorAll('[data-run-id]')).toHaveLength(0);
    expect(strip).toHaveAttribute('data-runs', '2010');
  });

  it('colours the cells by the share of failed runs', () => {
    renderStrip(activity);
    const tones = [...screen.getByTestId('dag-activity').children].map((cell) =>
      cell.getAttribute('data-tone')
    );
    expect(tones.slice(-4)).toEqual(['success', 'empty', 'warning', 'error']);
    expect(tones.filter((tone) => tone === 'empty')).toHaveLength(21);
  });

  it('gives the time, the compact count and the failures in each tooltip', () => {
    renderStrip(activity);
    const cells = [...screen.getByTestId('dag-activity').children];
    expect(cells[20]).toHaveAttribute(
      'title',
      expect.stringMatching(
        /\d{4}-\d{2}-\d{2} \d{2}:\d{2} · Runs 1\.2k · Failed 0/
      )
    );
    expect(cells[22]).toHaveAttribute(
      'title',
      expect.stringContaining('Runs 800 · Failed 20')
    );
    expect(cells[21]).toHaveAttribute(
      'title',
      expect.stringContaining('No runs')
    );
  });

  // A service that was up all day is green on every cell, also where no run
  // started, and the live run (a bucket with up time only) shows.
  it('colours green each cell where the service was up', () => {
    const up: Activity = {
      runs: [],
      truncated: false,
      resolutionSeconds: 3600,
      buckets: [
        bucket(NOW - 4 * HOUR, 1, 0, HOUR),
        bucket(NOW - 3 * HOUR, 0, 0, HOUR),
        bucket(NOW - 2 * HOUR, 0, 0, HOUR),
        bucket(NOW - HOUR, 0, 1, HOUR),
        bucket(NOW - 1000, 0, 0, 1_800_000),
      ],
    };
    renderStrip(up);
    const strip = screen.getByTestId('dag-activity');
    const cells = [...strip.children];
    expect(
      cells.slice(-5).map((cell) => cell.getAttribute('data-tone'))
    ).toEqual(['success', 'success', 'success', 'error', 'success']);
    expect(
      cells.filter((cell) => cell.getAttribute('data-tone') === 'empty')
    ).toHaveLength(19);
    // The up time does not count as a run.
    expect(strip).toHaveAttribute('data-runs', '1');
    expect(cells[20]).toHaveAttribute('title', expect.stringMatching(/ · Up$/));
    expect(cells[19]).toHaveAttribute(
      'title',
      expect.stringContaining('Runs 1 · Failed 0')
    );
  });

  it('names the real window, the counts and the success rate', () => {
    renderStrip(activity);
    expect(screen.getByTestId('dag-activity')).toHaveAttribute(
      'aria-label',
      'Runs in the last 24 hours: 2,010, failed 30, success rate 98.50 %'
    );
  });

  // The history gives the uptime of the window: it replaces the success rate
  // beside the count, and the name of the strip gives both.
  it('shows the uptime beside the count when the history has it', () => {
    renderStrip({ ...activity, uptime: 0.9912 });
    expect(screen.getByTestId('dag-service-uptime')).toHaveTextContent(
      'Uptime 99.12 %'
    );
    expect(screen.queryByTestId('dag-service-availability')).toBeNull();
    expect(screen.getByTestId('dag-activity')).toHaveAttribute(
      'aria-label',
      'Runs in the last 24 hours: 2,010, failed 30, success rate 98.50 %, uptime 99.12 %'
    );
  });

  it('shows an uptime of 0 as a figure, not as a dash', () => {
    renderStrip({ ...activity, uptime: 0 });
    expect(screen.getByTestId('dag-service-uptime')).toHaveTextContent(
      'Uptime 0.00 %'
    );
  });

  it('shows the compact count and the availability next to the strip', () => {
    renderStrip(activity);
    expect(screen.getByTestId('compact-count')).toHaveTextContent('2k');
    expect(screen.getByTestId('compact-count')).toHaveAttribute(
      'aria-label',
      '2,010'
    );
    expect(screen.getByTestId('dag-service-availability')).toHaveTextContent(
      'Success rate 98.50 %'
    );
  });

  it('shows an empty strip and a dash for a service DAG with no history', () => {
    renderStrip({ runs: [], truncated: false });
    const strip = screen.getByTestId('dag-activity');
    expect(strip.children).toHaveLength(60);
    expect(
      [...strip.children].every((c) => c.getAttribute('data-tone') === 'empty')
    ).toBe(true);
    expect(screen.getByTestId('dag-service-availability')).toHaveTextContent(
      '-'
    );
  });

  it('keeps the fixed width of ActivityBars, or fills the cell', () => {
    const { unmount } = renderStrip(activity);
    expect(screen.getByTestId('dag-activity')).toHaveStyle({ width: '196px' });
    unmount();
    renderStrip(activity, true);
    expect(screen.getByTestId('dag-activity').style.width).toBe('');
    expect(screen.getByTestId('dag-activity').className).toContain('flex-1');
  });

  it('draws nothing but a placeholder while the activity is not there', () => {
    renderStrip(undefined);
    expect(screen.queryByTestId('dag-activity')).toBeNull();
  });
});

// The detail opens on a click, as a touch screen has no hover, or on the
// focus and the keys of the button. It asks for the availability once, when
// it opens.
describe('ServiceActivityStrip detail', () => {
  const activity: Activity = {
    runs: [],
    truncated: false,
    resolutionSeconds: 3600,
    buckets: [
      bucket(NOW - 10 * HOUR, 1_200, 0),
      // Five empty slots follow: the hours 08 to 12 of the day.
      bucket(NOW - 5 * HOUR, 800, 20),
    ],
  };
  // The tooltip holds the content twice: the visible one and the one that a
  // screen reader takes.
  const details = () => screen.queryAllByTestId('dag-service-detail');
  const availabilityAsks = () =>
    h.asked.filter(
      (ask) =>
        ask.path === '/dags/{fileName}/run-history/availability' &&
        ask.init !== null
    );

  it('is a plain strip without a file name', () => {
    renderStrip(activity);
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('asks for nothing while it is closed', () => {
    renderStrip(activity, true, 'svc.yaml');
    expect(
      screen.getByRole('button', { name: /^Runs in the last 24 hours/ })
    ).toHaveAttribute('aria-expanded', 'false');
    expect(details()).toHaveLength(0);
    expect(availabilityAsks()).toHaveLength(0);
  });

  it('shows the figures after a click and asks once', () => {
    renderStrip(activity, true, 'svc.yaml');
    fireEvent.click(
      screen.getByRole('button', { name: /^Runs in the last 24 hours/ })
    );
    const detail = details()[0]!;
    // The history has no uptime for the window: a dash, not 0 %.
    expect(detail).toHaveTextContent('Uptime 7d-');
    expect(detail).toHaveTextContent('Success rate 7d99.00 %');
    expect(detail).toHaveTextContent('Runs in the last 24 hours2k');
    expect(detail).toHaveTextContent('Last failure');
    expect(detail).not.toHaveTextContent('Last failureNone');
    expect(detail).toHaveTextContent('Longest gap5h');
    expect(availabilityAsks()[0]!.init).toMatchObject({
      params: { path: { fileName: 'svc.yaml' } },
    });
  });

  it('shows the uptime of 7 days when the history has it', () => {
    h.uptime = 0.8765;
    renderStrip(activity, true, 'svc.yaml');
    fireEvent.click(
      screen.getByRole('button', { name: /^Runs in the last 24 hours/ })
    );
    const detail = details()[0]!;
    expect(detail).toHaveTextContent('Uptime 7d87.65 %');
    expect(detail).toHaveTextContent('Success rate 7d99.00 %');
  });

  it('closes with Escape and opens again', () => {
    renderStrip(activity, true, 'svc.yaml');
    const button = screen.getByRole('button', {
      name: /^Runs in the last 24 hours/,
    });
    fireEvent.click(button);
    expect(details().length).toBeGreaterThan(0);
    fireEvent.keyDown(details()[0]!, { key: 'Escape' });
    expect(details()).toHaveLength(0);
    fireEvent.click(button);
    expect(details().length).toBeGreaterThan(0);
  });

  // A click toggles the detail. A key that clicks the button (detail 0)
  // keeps the detail that the focus opened.
  it('toggles on a click and stays open on a key', () => {
    renderStrip(activity, true, 'svc.yaml');
    const button = screen.getByRole('button', {
      name: /^Runs in the last 24 hours/,
    });
    fireEvent.click(button, { detail: 1 });
    expect(details().length).toBeGreaterThan(0);
    fireEvent.click(button, { detail: 0 });
    expect(details().length).toBeGreaterThan(0);
    fireEvent.click(button, { detail: 1 });
    expect(details()).toHaveLength(0);
  });

  it('does not let the click reach the row', () => {
    const onRow = vi.fn();
    render(
      <UserPreferencesProvider>
        <I18nProvider>
          <div onClick={onRow}>
            <ServiceActivityStrip activity={activity} fileName="svc.yaml" />
          </div>
        </I18nProvider>
      </UserPreferencesProvider>
    );
    fireEvent.click(
      screen.getByRole('button', { name: /^Runs in the last 24 hours/ })
    );
    expect(onRow).not.toHaveBeenCalled();
  });
});
