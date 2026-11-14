// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { ScheduleKind } from '@/api/v1/schema';
import { summarizeSchedules } from '../scheduleSummary';

// Monday 2026-10-26 10:07:30 UTC.
const NOW = new Date('2026-10-26T10:07:30Z');
const UTC = { timeZone: 'UTC' };

function cron(expression: string, profile?: string) {
  return { kind: ScheduleKind.cron, expression, profile };
}

describe('summarizeSchedules', () => {
  it('gives the next run and the wording of one schedule', () => {
    const summary = summarizeSchedules([cron('*/15 * * * *')], NOW, UTC);
    expect(summary.lead?.wording).toEqual({
      key: 'every {n} min',
      values: { n: 15 },
    });
    expect(summary.nextRun?.toISOString()).toBe('2026-10-26T10:15:00.000Z');
    expect(summary.extra).toBe(0);
    expect(summary.entries).toHaveLength(1);
  });

  it('sorts three schedules by next run and leads with the soonest', () => {
    const summary = summarizeSchedules(
      [
        cron('0 9 * * *'),
        { kind: ScheduleKind.at, at: '2026-10-26T10:10:00Z' },
        cron('*/30 * * * *'),
      ],
      NOW,
      UTC
    );
    expect(summary.entries.map((entry) => entry.expression)).toEqual([
      '2026-10-26T10:10:00Z',
      '*/30 * * * *',
      '0 9 * * *',
    ]);
    expect(summary.lead?.kind).toBe('at');
    expect(summary.lead?.wording).toEqual({ key: 'once' });
    expect(summary.extra).toBe(2);
  });

  it('describes a weekday cron and reads the next weekday', () => {
    // Friday evening: the next weekday run is on Monday.
    const friday = new Date('2026-10-23T18:00:00Z');
    const summary = summarizeSchedules([cron('30 9 * * 1-5')], friday, UTC);
    expect(summary.lead?.wording).toEqual({
      key: 'weekdays at {time}',
      values: { time: '09:30' },
    });
    expect(summary.nextRun?.toISOString()).toBe('2026-10-26T09:30:00.000Z');
  });

  it('describes hourly and daily schedules and keeps other ones raw', () => {
    const wording = (expression: string) =>
      summarizeSchedules([cron(expression)], NOW, UTC).lead?.wording;
    expect(wording('0 * * * *')).toEqual({ key: 'hourly' });
    expect(wording('5 * * * *')).toEqual({
      key: 'hourly at :{minute}',
      values: { minute: '05' },
    });
    expect(wording('0 */6 * * *')).toEqual({
      key: 'every {n} h',
      values: { n: 6 },
    });
    expect(wording('0 2 * * *')).toEqual({
      key: 'daily at {time}',
      values: { time: '02:00' },
    });
    expect(wording('@hourly')).toEqual({ key: 'hourly' });
    expect(wording('@every 90m')).toEqual({
      key: 'every {n} min',
      values: { n: 90 },
    });
    expect(wording('0 9 1 * *')).toBeNull();
  });

  it('keeps a step raw when the step does not divide its range', () => {
    const wording = (expression: string) =>
      summarizeSchedules([cron(expression)], NOW, UTC).lead?.wording;
    // :00 and :45 of each hour: the gaps are 45 and 15 minutes.
    expect(wording('*/45 * * * *')).toBeNull();
    // 00, 05, ..., 20 and again 00: the last gap is 4 hours.
    expect(wording('0 */5 * * *')).toBeNull();
  });

  it('reads the zone of the server and the zone of one schedule', () => {
    const daily = summarizeSchedules([cron('0 9 * * *')], NOW, {
      timeZone: 'Asia/Tokyo',
    });
    // 09:00 in Tokyo is 00:00 UTC; the next one is on 2026-10-27.
    expect(daily.nextRun?.toISOString()).toBe('2026-10-27T00:00:00.000Z');

    const prefixed = summarizeSchedules(
      [cron('CRON_TZ=America/New_York 0 9 * * *')],
      NOW,
      UTC
    );
    // 09:00 in New York (EDT, UTC-4) is 13:00 UTC.
    expect(prefixed.nextRun?.toISOString()).toBe('2026-10-26T13:00:00.000Z');
    expect(prefixed.lead?.wording).toEqual({
      key: 'daily at {time}',
      values: { time: '09:00' },
    });
  });

  it('gives no next run for a suspended workflow', () => {
    const summary = summarizeSchedules(
      [cron('*/15 * * * *'), cron('0 * * * *')],
      NOW,
      {
        ...UTC,
        suspended: true,
        serverNextRun: new Date('2026-10-26T10:15:00Z'),
      }
    );
    expect(summary.suspended).toBe(true);
    expect(summary.nextRun).toBeNull();
    expect(summary.lead?.expression).toBe('*/15 * * * *');
    expect(summary.extra).toBe(1);
  });

  it('returns an empty summary without a schedule', () => {
    const summary = summarizeSchedules([], NOW, UTC);
    expect(summary.entries).toEqual([]);
    expect(summary.lead).toBeNull();
    expect(summary.nextRun).toBeNull();
    expect(summary.extra).toBe(0);
  });

  it('lets the server next run name the lead schedule', () => {
    // The server time matches one computed time: that schedule leads.
    const summary = summarizeSchedules(
      [cron('0 12 * * *'), cron('*/20 * * * *')],
      NOW,
      { timeZone: 'UTC', serverNextRun: new Date('2026-10-26T10:20:00Z') }
    );
    expect(summary.lead?.expression).toBe('*/20 * * * *');
    expect(summary.nextRun?.toISOString()).toBe('2026-10-26T10:20:00.000Z');
  });

  it('takes the server next run that no computed time matches', () => {
    // The zone of the server is not the one given: the computed time is
    // hours off, and the server time still gives the next run.
    const summary = summarizeSchedules([cron('0 9 * * *')], NOW, {
      timeZone: 'UTC',
      serverNextRun: new Date('2026-10-27T00:00:00Z'),
    });
    expect(summary.lead?.expression).toBe('0 9 * * *');
    expect(summary.nextRun?.toISOString()).toBe('2026-10-27T00:00:00.000Z');
  });

  it('keeps an overdue one-off that the server still plans', () => {
    const summary = summarizeSchedules(
      [{ kind: ScheduleKind.at, at: '2026-10-26T10:00:00Z' }],
      NOW,
      { ...UTC, serverNextRun: new Date('2026-10-26T10:00:00Z') }
    );
    expect(summary.lead?.kind).toBe('at');
    expect(summary.nextRun?.toISOString()).toBe('2026-10-26T10:00:00.000Z');
  });

  it('gives no preview for an expression that the parser rejects', () => {
    const summary = summarizeSchedules([cron('not a cron')], NOW, UTC);
    expect(summary.lead?.next).toBeNull();
    expect(summary.lead?.wording).toBeNull();
    expect(summary.nextRun).toBeNull();

    // A key of every object is no cron descriptor.
    const inherited = summarizeSchedules([cron('constructor')], NOW, UTC);
    expect(inherited.lead?.next).toBeNull();
    expect(inherited.lead?.wording).toBeNull();
  });
});
