// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useEffect, useMemo, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { AppBarContext } from '@/contexts/AppBarContext';
import { resolveLegacyLink } from './legacyLinks';

export type LegacyRedirectState =
  | 'pending'
  | 'settled'
  | { needsHistory: number };

/**
 * Called by the page before it renders a body. While the result is a
 * redirect or a run page, it returns 'pending' and the page renders nothing.
 * A run page rule without dagRunName stays 'pending' until dagName is known.
 */
export function useLegacyRedirect(
  fileName: string,
  tab: string | undefined,
  dagName: string | null
): LegacyRedirectState {
  const location = useLocation();
  const navigate = useNavigate();
  const appBarRemoteNode = useContext(AppBarContext).selectedRemoteNode?.trim();

  const result = useMemo(() => {
    const search = new URLSearchParams(location.search);
    // The app bar node applies when the URL names none, as the page does.
    if (
      !search.get('remoteNode')?.trim() &&
      appBarRemoteNode &&
      appBarRemoteNode !== 'local'
    ) {
      search.set('remoteNode', appBarRemoteNode);
    }
    return resolveLegacyLink(fileName, tab, search, { dagName });
  }, [appBarRemoteNode, dagName, fileName, location.search, tab]);

  const target =
    result.kind === 'redirect' || result.kind === 'runPage' ? result.to : null;
  // One navigation for each location, also when effects run twice.
  const navigated = useRef<string | null>(null);
  useEffect(() => {
    if (!fileName || target === null) return;
    const key = `${location.key}\n${target}`;
    if (navigated.current === key) return;
    navigated.current = key;
    navigate(target, { replace: true });
  }, [fileName, location.key, navigate, target]);

  const idx = result.kind === 'needsHistory' ? result.idx : null;
  return useMemo<LegacyRedirectState>(() => {
    if (!fileName) return 'settled';
    if (target !== null || result.kind === 'waitDagName') return 'pending';
    return idx !== null ? { needsHistory: idx } : 'settled';
  }, [fileName, idx, result.kind, target]);
}
