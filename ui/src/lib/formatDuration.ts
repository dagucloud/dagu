// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** Formats milliseconds as "1d 2h 3m 4s", omitting zero units. */
export function formatDurationMs(ms: number): string {
  const seconds = Math.floor(ms / 1000);
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const secs = seconds % 60;
  const parts: string[] = [];
  if (days > 0) parts.push(`${days}d`);
  if (hours > 0) parts.push(`${hours}h`);
  if (minutes > 0) parts.push(`${minutes}m`);
  // Only show seconds if no larger units or if seconds > 0
  if (secs > 0 || parts.length === 0) parts.push(`${secs}s`);
  return parts.join(' ');
}

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

/**
 * Formats a duration that counts on screen: to the second within an hour,
 * to the minute within a day, then to the hour. A far-off time stays short
 * and changes only when its last unit does.
 */
export function formatRoughDurationMs(ms: number): string {
  const step = ms >= DAY_MS ? HOUR_MS : ms >= HOUR_MS ? MINUTE_MS : 1000;
  return formatDurationMs(Math.floor(ms / step) * step);
}
