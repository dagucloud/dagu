// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGRunDetails, HandlerEvent, RunHandler } from './types';

/** Same order as getEventHandlers (features/dags/lib/getEventHandlers.ts). */
export const HANDLER_EVENTS: readonly HandlerEvent[] = [
  'onInit',
  'onWait',
  'onSuccess',
  'onFailure',
  'onAbort',
  'onExit',
];

/** The handler nodes of a run, each with the event that runs it. */
export function runHandlers(details: ApiDAGRunDetails | null): RunHandler[] {
  if (!details) return [];
  const handlers: RunHandler[] = [];
  for (const event of HANDLER_EVENTS) {
    const node = details[event];
    if (node) handlers.push({ event, node });
  }
  return handlers;
}
