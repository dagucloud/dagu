// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, isScalar, isSeq } from 'yaml';
import { parseAllDocumentsAsServer } from '../serverYaml';

export type RestartPolicy = 'no' | 'always' | 'unless-stopped';

/** What a service DAG cannot define in its file (spec 080). */
export interface RestartView {
  /** True when the schedule has a start, a stop or a restart entry. A service DAG cannot have one. */
  schedule: boolean;
  /** True when the file sets a DAG-level `retry_policy`. A service DAG cannot have one. */
  retryPolicy: boolean;
}

const NO_RESTART: RestartView = {
  schedule: false,
  retryPolicy: false,
};

/** True for a policy that keeps a run alive. */
export function isServicePolicy(policy: RestartPolicy): boolean {
  return policy !== 'no';
}

/**
 * The policy that a value names, as the loader reads it: the case and the
 * spaces do not count, and an empty value is `no`. Null for any other value.
 */
export function parseRestartPolicy(value: string): RestartPolicy | null {
  const policy = value.trim().toLowerCase();
  if (policy === '') return 'no';
  return policy === 'no' || policy === 'always' || policy === 'unless-stopped'
    ? policy
    : null;
}

/** True for a key with no value: absent, `null`, or empty. */
function isEmpty(node: unknown): boolean {
  return (
    node === undefined ||
    node === null ||
    (isScalar(node) && (node.value === null || node.value === ''))
  );
}

/** The keys of the map form of a schedule. */
const SCHEDULE_KINDS = ['start', 'stop', 'restart'];

/** The entries of a value: a string, an entry object, or a list of them. */
function entryCount(node: unknown): number {
  if (isSeq(node)) return node.items.length;
  if (isMap(node)) return node.items.length > 0 ? 1 : 0;
  return isScalar(node) &&
    typeof node.value === 'string' &&
    node.value.trim() !== ''
    ? 1
    : 0;
}

/**
 * The entries of a schedule: a string, a list, or the start, stop and
 * restart of the map form, each of them in any form.
 */
function scheduleEntries(node: unknown): number {
  if (!isMap(node)) return entryCount(node);
  return SCHEDULE_KINDS.reduce(
    (count, key) => count + entryCount(node.get(key, true)),
    0
  );
}

/** Reads the schedule and retry policy of document 0. Never throws. */
export function readRestart(text: string): RestartView {
  try {
    const doc = parseAllDocumentsAsServer(text)[0];
    if (!doc || doc.errors.length > 0 || !isMap(doc.contents)) {
      return NO_RESTART;
    }
    const root = doc.contents;
    return {
      schedule: scheduleEntries(root.get('schedule', true)) > 0,
      retryPolicy: !isEmpty(root.get('retry_policy', true)),
    };
  } catch {
    return NO_RESTART;
  }
}
