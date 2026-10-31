// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo, useState, type ReactNode } from 'react';
import {
  RunLinksContext,
  type RunLinks,
} from '@/features/visual-editor/references/RunLinks';
import { useClient } from '@/hooks/api';
import { useWorkspaceData } from '../data/context';
import { useRunHistory } from '../data/useRunHistory';
import { useWorkspaceUrl } from '../state/workspaceUrl';

/**
 * Gives the marks of the reused inputs the run that their outputs came from
 * as a link: a click selects the run in the URL, and a run that the server
 * does not have any more is said to be gone. The history, test runs
 * included, tells which runs exist; a run that it does not list (it lists the
 * newest only) is asked from the server.
 */
export function RunLinksHost({ children }: { children: ReactNode }) {
  const { dagData, fileName, remoteNode } = useWorkspaceData('RunLinksHost');
  // The history is read once a mark asks for it.
  const [active, setActive] = useState(false);
  const activate = useCallback(() => setActive(true), []);
  const history = useRunHistory({ enabled: active, includeEphemeral: true });
  const client = useClient();
  const [, setUrl] = useWorkspaceUrl();
  const dagName = dagData.dag?.name ?? fileName;
  const runs = history.runs;
  const links = useMemo<RunLinks>(
    () => ({
      activate,
      select: (runId) => setUrl({ run: runId }),
      resolve: (runId) =>
        runs.find((r) => r.dagRunId.toLowerCase() === runId.toLowerCase())
          ?.dagRunId ?? null,
      exists: async (runId) => {
        const { response } = await client.GET('/dag-runs/{name}/{dagRunId}', {
          params: {
            path: { name: dagName, dagRunId: runId },
            query: { remoteNode },
          },
        });
        // Only a clear "not found" says that the run is gone.
        return response.status !== 404;
      },
      scope: `${remoteNode}\u0000${dagName}`,
    }),
    [activate, client, dagName, remoteNode, runs, setUrl]
  );
  return (
    <RunLinksContext.Provider value={links}>
      {children}
    </RunLinksContext.Provider>
  );
}
