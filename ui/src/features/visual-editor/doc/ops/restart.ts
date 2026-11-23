// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, isScalar, isSeq } from 'yaml';
import { parseAllDocumentsAsServer } from '../serverYaml';

export type RestartPolicy = 'no' | 'always' | 'unless-stopped';

/** The restart policy of the DAG and what it cannot go with (spec 080). */
export interface RestartView {
  /** `no` when the key is absent, empty or not a known value. The case and the spaces do not count. */
  policy: RestartPolicy;
  /** True when the schedule has a start, a stop or a restart entry. A service DAG cannot have one. */
  schedule: boolean;
  /** True when the file sets a DAG-level `retry_policy`. A service DAG cannot have one. */
  retryPolicy: boolean;
  /**
   * True when the file gives `restart_wait_sec` a wait that the server takes.
   * No value and 0 leave the wait of the base configuration.
   */
  waitGiven: boolean;
}

const NO_RESTART: RestartView = {
  policy: 'no',
  schedule: false,
  retryPolicy: false,
  waitGiven: false,
};

/** True for a policy that keeps a run alive. */
export function isServicePolicy(policy: RestartPolicy): boolean {
  return policy !== 'no';
}

function readPolicy(node: unknown): RestartPolicy {
  const value =
    isScalar(node) && typeof node.value === 'string'
      ? node.value.trim().toLowerCase()
      : '';
  return value === 'always' || value === 'unless-stopped' ? value : 'no';
}

/** True for a key with no value: absent, `null`, or empty. */
function isEmpty(node: unknown): boolean {
  return (
    node === undefined ||
    node === null ||
    (isScalar(node) && (node.value === null || node.value === ''))
  );
}

/** The entries of a schedule: a string, a list, or the start, stop and restart of a map. */
function scheduleEntries(node: unknown): number {
  if (isSeq(node)) return node.items.length;
  if (isMap(node)) {
    return ['start', 'stop', 'restart'].reduce(
      (count, key) => count + scheduleEntries(node.get(key, true)),
      0
    );
  }
  return isScalar(node) &&
    typeof node.value === 'string' &&
    node.value.trim() !== ''
    ? 1
    : 0;
}

/** Reads the restart keys of document 0. Never throws. */
export function readRestart(text: string): RestartView {
  try {
    const doc = parseAllDocumentsAsServer(text)[0];
    if (!doc || doc.errors.length > 0 || !isMap(doc.contents)) {
      return NO_RESTART;
    }
    const root = doc.contents;
    const wait = root.get('restart_wait_sec', true);
    return {
      policy: readPolicy(root.get('restart_policy', true)),
      schedule: scheduleEntries(root.get('schedule', true)) > 0,
      retryPolicy: !isEmpty(root.get('retry_policy', true)),
      waitGiven:
        !isEmpty(wait) && !(isScalar(wait) && Number(wait.value) === 0),
    };
  } catch {
    return NO_RESTART;
  }
}
