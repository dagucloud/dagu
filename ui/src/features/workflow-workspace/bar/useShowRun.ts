// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useRef } from 'react';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/**
 * Returns a stable function that selects a run in the workspace: it writes
 * ?run and keeps the step and the node view, like a pick of an older run
 * does: a run that starts from the bar stays on the step. The function
 * reads the URL when it is called, not when it was made, because a start
 * request finishes some time after the click. `runName` names the new run in
 * the URL when the name of the file does not give its DAG name.
 */
export function useShowRun(): (runId: string, runName?: string | null) => void {
  const [, setUrl] = useWorkspaceUrl();
  const latest = useRef(setUrl);
  useEffect(() => {
    latest.current = setUrl;
  });
  return useCallback((runId: string, runName?: string | null) => {
    latest.current(runName ? { run: runId, runName } : { run: runId });
  }, []);
}
