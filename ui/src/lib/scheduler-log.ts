// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

export type SchedulerLogLine = {
  timestamp?: string;
  level?: string;
  message: string;
  step?: string;
  details?: string;
  structured: boolean;
};

const TEXT_ATTRIBUTE_RE = /(?:^|\s)([\w.-]+)=("(?:\\.|[^"])*"|\S+)/g;
const HIDDEN_FIELDS = new Set([
  'time',
  'level',
  'msg',
  'name',
  'dag',
  'run-id',
  'attempt-id',
  'worker-id',
  'trace-id',
  'span-id',
  'trace-flags',
  'step',
]);

function decodeValue(value: string): string {
  if (!value.startsWith('"')) return value;
  try {
    return JSON.parse(value) as string;
  } catch {
    return value.slice(1, -1);
  }
}

function parseJSONLine(line: string): SchedulerLogLine | null {
  if (!line.trimStart().startsWith('{')) return null;

  try {
    const value = JSON.parse(line) as Record<string, unknown>;
    if (typeof value.msg !== 'string') return null;

    const details = Object.entries(value)
      .filter(([key]) => !HIDDEN_FIELDS.has(key))
      .map(
        ([key, entry]) =>
          `${key}=${typeof entry === 'string' ? entry : JSON.stringify(entry)}`
      )
      .join(' ');

    return {
      timestamp: typeof value.time === 'string' ? value.time : undefined,
      level:
        typeof value.level === 'string' ? value.level.toUpperCase() : undefined,
      message: value.msg,
      step: typeof value.step === 'string' ? value.step : undefined,
      details: details || undefined,
      structured: true,
    };
  } catch {
    return null;
  }
}

/** Parses Dagu's text or JSON scheduler log format, falling back to plain text. */
export function parseSchedulerLogLine(line: string): SchedulerLogLine {
  const jsonLine = parseJSONLine(line);
  if (jsonLine) return jsonLine;

  const fields = new Map<string, string>();
  const details: string[] = [];
  for (const match of line.matchAll(TEXT_ATTRIBUTE_RE)) {
    const key = match[1];
    const rawValue = match[2];
    if (!key || !rawValue) continue;
    fields.set(key, decodeValue(rawValue));
    if (!HIDDEN_FIELDS.has(key)) details.push(`${key}=${rawValue}`);
  }

  const message = fields.get('msg');
  if (!message || !fields.has('time') || !fields.has('level')) {
    return { message: line, structured: false };
  }

  return {
    timestamp: fields.get('time'),
    level: fields.get('level')?.toUpperCase(),
    message,
    step: fields.get('step'),
    details: details.join(' ') || undefined,
    structured: true,
  };
}
