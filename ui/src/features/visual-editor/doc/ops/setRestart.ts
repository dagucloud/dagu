// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Pair, Scalar, isMap, isScalar } from 'yaml';
import type { Document, YAMLMap } from 'yaml';
import type { DagEditOp, EditResult } from '../../contracts';
import { DAG_KEY_ORDER } from '../format';
import { editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import { parseAllDocumentsAsServer } from '../serverYaml';
import {
  isServicePolicy,
  parseRestartPolicy,
  readRestart,
  type RestartPolicy,
  type RestartView,
} from './restart';

/** The restart keys of the DAG that the Always on launcher shows and sets. */
export interface RestartSettings extends RestartView {
  /** `no` when the key is absent, empty or not a known value. */
  policy: RestartPolicy;
  /** The `restart_wait_sec` of the file, when it is an integer. */
  waitSec: number | null;
  /** The `hist_retention_runs` of the file, when it is an integer. */
  histRetentionRuns: number | null;
}

const RESTART_KEYS = [
  'restart_policy',
  'restart_wait_sec',
  'hist_retention_runs',
] as const;

function readPolicy(node: unknown): RestartPolicy {
  const policy =
    isScalar(node) && typeof node.value === 'string'
      ? parseRestartPolicy(node.value)
      : null;
  return policy ?? 'no';
}

function readInteger(node: unknown): number | null {
  return isScalar(node) &&
    typeof node.value === 'number' &&
    Number.isInteger(node.value)
    ? node.value
    : null;
}

/** Reads the restart keys of document 0. Never throws. */
export function readRestartSettings(text: string): RestartSettings {
  const settings: RestartSettings = {
    ...readRestart(text),
    policy: 'no',
    waitSec: null,
    histRetentionRuns: null,
  };
  try {
    const doc = parseAllDocumentsAsServer(text)[0];
    if (doc && doc.errors.length === 0 && isMap(doc.contents)) {
      settings.policy = readPolicy(doc.contents.get('restart_policy', true));
      settings.waitSec = readInteger(
        doc.contents.get('restart_wait_sec', true)
      );
      settings.histRetentionRuns = readInteger(
        doc.contents.get('hist_retention_runs', true)
      );
    }
  } catch {
    // The settings say no policy.
  }
  return settings;
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
  // A new value of an anchored key would also change its aliases.
  if (
    RESTART_KEYS.some((key) => {
      const node = root.get(key, true);
      return isScalar(node) && !!node.anchor;
    })
  ) {
    return {
      ok: false,
      code: 'unsupportedForm',
      message: 'A restart key has an anchor. Edit it in the YAML.',
    };
  }
  const { policy, waitSec, histRetentionRuns } = op;
  const before = readRestartSettings(text);
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
    // The server takes one retention: a count of runs replaces the days.
    root.delete('hist_retention_days');
    setKey(root, 'hist_retention_runs', histRetentionRuns);
  }
  return;
}

/**
 * Sets the keys `restart_policy`, `restart_wait_sec` and `hist_retention_runs`
 * of the DAG. A field that the operation leaves out does not change, and
 * `null` removes the key. The policy `no` removes `restart_policy`, and a
 * count of runs to keep removes `hist_retention_days`. A service policy fails
 * when the DAG has a schedule or a retry_policy and is not a service yet, and
 * any edit fails when a restart key has an anchor. The other lines of the
 * text do not change.
 */
export function opSetRestartPolicy(
  text: string,
  op: Extract<DagEditOp, { kind: 'setRestartPolicy' }>
): EditResult {
  return editDocument(text, (doc) => setRestart(doc, text, op));
}
