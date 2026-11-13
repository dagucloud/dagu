// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Pair, Scalar, isMap, isScalar } from 'yaml';
import type { Document, YAMLMap } from 'yaml';
import type { DagEditOp, EditResult } from '../../contracts';
import { DAG_KEY_ORDER } from '../format';
import { editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import { parseAllDocumentsAsServer } from '../serverYaml';
import { readSchedule } from './schedule';

export type RestartPolicy = 'no' | 'always' | 'unless-stopped';

/** The keys of the DAG that keep a run alive (spec 080), as the YAML writes them. */
export interface RestartView {
  /** `no` when the key is absent, empty or not a known value. The case and the spaces do not count. */
  policy: RestartPolicy;
  /** The `restart_wait_sec` of the file, when it is an integer. */
  waitSec: number | null;
  /** The `hist_retention_runs` of the file, when it is an integer. */
  histRetentionRuns: number | null;
  /** True when the file has a schedule of any form. A service DAG cannot have one. */
  schedule: boolean;
  /** True when the file has a DAG-level `retry_policy`. A service DAG cannot have one. */
  retryPolicy: boolean;
}

const NO_RESTART: RestartView = {
  policy: 'no',
  waitSec: null,
  histRetentionRuns: null,
  schedule: false,
  retryPolicy: false,
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

function readInteger(node: unknown): number | null {
  return isScalar(node) &&
    typeof node.value === 'number' &&
    Number.isInteger(node.value)
    ? node.value
    : null;
}

/** Reads the restart keys of document 0. Never throws. */
export function readRestart(text: string): RestartView {
  try {
    const doc = parseAllDocumentsAsServer(text)[0];
    if (!doc || doc.errors.length > 0 || !isMap(doc.contents)) {
      return NO_RESTART;
    }
    const root = doc.contents;
    const retry = root.get('retry_policy', true);
    return {
      policy: readPolicy(root.get('restart_policy', true)),
      waitSec: readInteger(root.get('restart_wait_sec', true)),
      histRetentionRuns: readInteger(root.get('hist_retention_runs', true)),
      schedule: readSchedule(text).present,
      retryPolicy: retry !== undefined && retry !== null,
    };
  } catch {
    return NO_RESTART;
  }
}

function isPositiveInteger(value: number): boolean {
  return Number.isInteger(value) && value >= 1;
}

/** Sets a top-level key: a scalar keeps its comment, a new key follows the canonical order. */
function setKey(root: YAMLMap, key: string, value: string | number): void {
  const existing = root.get(key, true);
  if (isScalar(existing)) {
    existing.value = value;
    return;
  }
  if (existing !== undefined) {
    root.set(key, new Scalar(value));
    return;
  }
  const rank = DAG_KEY_ORDER.indexOf(key);
  const at = root.items.findIndex((item) => {
    const other = isScalar(item.key)
      ? DAG_KEY_ORDER.indexOf(String(item.key.value))
      : -1;
    return other > rank;
  });
  const pair = new Pair(new Scalar(key), new Scalar(value));
  if (at < 0) root.items.push(pair);
  else root.items.splice(at, 0, pair);
}

function setRestart(
  doc: Document,
  text: string,
  op: Extract<DagEditOp, { kind: 'setRestartPolicy' }>
): MutateOutcome {
  if (!isMap(doc.contents)) {
    return {
      ok: false,
      code: 'unsupportedForm',
      message: 'The document is not a mapping.',
    };
  }
  const root: YAMLMap = doc.contents;
  const { policy, waitSec, histRetentionRuns } = op;
  const before = readRestart(text);
  if (
    policy !== undefined &&
    isServicePolicy(policy) &&
    !isServicePolicy(before.policy) &&
    (before.schedule || before.retryPolicy)
  ) {
    return {
      ok: false,
      code: 'restartConflict',
      message:
        'A service DAG cannot have a schedule or a retry_policy. Remove it first.',
    };
  }
  for (const value of [waitSec, histRetentionRuns]) {
    if (typeof value === 'number' && !isPositiveInteger(value)) {
      return {
        ok: false,
        code: 'invalidValue',
        message: 'The value must be a positive integer.',
      };
    }
  }
  // The key order of a new key: the policy, the wait, the retention.
  if (policy === 'no') root.delete('restart_policy');
  else if (policy !== undefined) setKey(root, 'restart_policy', policy);
  if (waitSec === null) root.delete('restart_wait_sec');
  else if (waitSec !== undefined) setKey(root, 'restart_wait_sec', waitSec);
  if (histRetentionRuns === null) root.delete('hist_retention_runs');
  else if (histRetentionRuns !== undefined) {
    setKey(root, 'hist_retention_runs', histRetentionRuns);
  }
  return;
}

/**
 * Sets the keys `restart_policy`, `restart_wait_sec` and `hist_retention_runs`
 * of the DAG. A field that the operation leaves out does not change, and
 * `null` removes the key. The policy `no` removes `restart_policy`. A service
 * policy fails when the DAG has a schedule or a retry_policy and is not
 * a service yet. The other lines of the text do not change.
 */
export function opSetRestartPolicy(
  text: string,
  op: Extract<DagEditOp, { kind: 'setRestartPolicy' }>
): EditResult {
  return editDocument(text, (doc) => setRestart(doc, text, op));
}
