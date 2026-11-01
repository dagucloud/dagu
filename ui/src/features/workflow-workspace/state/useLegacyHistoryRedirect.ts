// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useMemo, useRef } from 'react';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import type { ApiDAGRunDetails } from '../contracts';
import { buildWorkspaceUrl, parseWorkspaceUrl } from './workspaceUrl';

const LEGACY_TABS = new Set(['history', 'executions']);
const CARRY_KEYS = ['remoteNode', 'workspace'] as const;

/**
 * The run of a legacy History index. The History grid shows the runs oldest
 * first, so index n is column n of the reversed newest-first list. An index
 * that is not a column gives the newest run.
 */
export function runIdAtHistoryIndex(
  runs: readonly ApiDAGRunDetails[],
  idx: string
): string | null {
  const n = Number(idx);
  const column = Number.isInteger(n) && n >= 0 && n < runs.length ? n : -1;
  if (column < 0) return runs[0]?.dagRunId ?? null;
  return runs[runs.length - 1 - column]?.dagRunId ?? null;
}

/**
 * The workflow URL of a legacy history link, with ?run; remoteNode and
 * workspace stay. A run opens the Run panel on its History tab (?dock), the
 * grid that the old History page was: the panel reads ?run at mount only,
 * and this link adds it after the mount.
 */
export function legacyHistoryTarget(
  fileName: string,
  search: URLSearchParams,
  runId: string | null
): string {
  const carried = new URLSearchParams();
  for (const key of CARRY_KEYS) {
    const value = search.get(key);
    if (value) carried.set(key, value);
  }
  return buildWorkspaceUrl(fileName, {
    ...parseWorkspaceUrl(undefined, carried),
    run: runId,
    dock: runId ? 'history' : null,
  });
}

/**
 * Resolves a legacy /history?idx=n (or /executions?idx=n) link without
 * ?run. ?dagRunId wins over idx at once; else the hook waits for the first
 * history load. It navigates once, with replace. Returns true while a
 * redirect is due.
 */
export function useLegacyHistoryRedirect(history: {
  runs: readonly ApiDAGRunDetails[];
  isLoading: boolean;
}): boolean {
  const { fileName = '', tab } = useParams<{
    fileName: string;
    tab?: string;
  }>();
  const location = useLocation();
  const navigate = useNavigate();

  const legacy = useMemo(() => {
    const search = new URLSearchParams(location.search);
    const idx = search.get('idx');
    if (!fileName || !tab || !LEGACY_TABS.has(tab)) return null;
    if (idx === null || idx === '' || search.get('run')) return null;
    return { search, idx, dagRunId: search.get('dagRunId') || null };
  }, [fileName, location.search, tab]);

  const ready = legacy !== null && (!!legacy.dagRunId || !history.isLoading);
  const target = useMemo(() => {
    if (!legacy || !ready) return null;
    const runId =
      legacy.dagRunId ?? runIdAtHistoryIndex(history.runs, legacy.idx);
    return legacyHistoryTarget(fileName, legacy.search, runId);
  }, [fileName, history.runs, legacy, ready]);

  // One navigation for each location, also when effects run twice.
  const navigated = useRef<string | null>(null);
  useEffect(() => {
    if (target === null || navigated.current === location.key) return;
    navigated.current = location.key;
    navigate(target, { replace: true });
  }, [location.key, navigate, target]);

  return legacy !== null;
}
