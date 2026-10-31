// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useState,
  useSyncExternalStore,
} from 'react';

/**
 * The shared clock. The page keeps ONE timer for each period (1 s, 1 min, ...),
 * however many components read it. A timer starts with the first subscriber
 * and stops with the last. All timers pause while the tab is hidden; when the
 * tab shows again, each subscriber is told once, so it catches up.
 *
 * This file is the only place of the UI that calls `setInterval` for a clock
 * (a test guards that). A component reads the clock with `useClock`, which
 * re-renders only when the derived value changes, or with `useClockNow`.
 */

type Listener = () => void;

interface Channel {
  listeners: Set<Listener>;
  timer: ReturnType<typeof setInterval> | null;
}

const channels = new Map<number, Channel>();
let watching = false;

function tabHidden(): boolean {
  return typeof document !== 'undefined' && document.hidden;
}

function notify(channel: Channel): void {
  for (const listener of [...channel.listeners]) listener();
}

function run(period: number, channel: Channel): void {
  if (channel.timer !== null || tabHidden()) return;
  channel.timer = setInterval(() => notify(channel), period);
}

function pause(channel: Channel): void {
  if (channel.timer === null) return;
  clearInterval(channel.timer);
  channel.timer = null;
}

function onVisibility(): void {
  if (tabHidden()) {
    for (const channel of channels.values()) pause(channel);
    return;
  }
  for (const [period, channel] of channels) {
    run(period, channel);
    notify(channel);
  }
}

/**
 * Calls `listener` every `period` ms, on the timer that all subscribers of
 * the same period share. Returns the function that stops it.
 */
export function subscribeClock(period: number, listener: Listener): () => void {
  let channel = channels.get(period);
  if (!channel) {
    channel = { listeners: new Set(), timer: null };
    channels.set(period, channel);
  }
  channel.listeners.add(listener);
  run(period, channel);
  if (!watching && typeof document !== 'undefined') {
    watching = true;
    document.addEventListener('visibilitychange', onVisibility);
  }
  const joined = channel;
  return () => {
    joined.listeners.delete(listener);
    if (joined.listeners.size > 0) return;
    pause(joined);
    if (channels.get(period) === joined) channels.delete(period);
    if (channels.size === 0 && watching) {
      watching = false;
      document.removeEventListener('visibilitychange', onVisibility);
    }
  };
}

const noSubscription = () => () => {};

/**
 * A value derived from the time, read again every `period` ms. The component
 * re-renders only when the derived value changes, so a text that reads "3m
 * ago" renders once a minute on a clock of 1 s. `derive` must return a
 * primitive (a string, a number or a boolean). With a null `period` nothing
 * ticks, and the value is read at each render.
 */
export function useClock<T extends string | number | boolean | null>(
  period: number | null,
  derive: (now: number) => T
): T {
  const subscribe = useCallback(
    (onChange: Listener) =>
      period === null ? noSubscription() : subscribeClock(period, onChange),
    [period]
  );
  const read = () => derive(Date.now());
  return useSyncExternalStore(subscribe, read, read);
}

/**
 * The time of the last tick of the clock of `period` ms, in ms since the
 * epoch. With a null `period` it stays at its last value. It renders at each
 * tick: prefer `useClock` when a derived value can say what changed.
 */
export function useClockNow(period: number | null): number {
  const [now, setNow] = useState(() => Date.now());
  // A clock that was stopped restarts with the true time before the first
  // paint: the view never shows the old time.
  useLayoutEffect(() => {
    if (period !== null) setNow(Date.now());
  }, [period]);
  useEffect(() => {
    if (period === null) return;
    return subscribeClock(period, () => setNow(Date.now()));
  }, [period]);
  return now;
}
