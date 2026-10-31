// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import {
  SubRunOpenProvider,
  SubRunStackModal,
  type SubRunStackEntry,
} from '@/features/dags/components/common/SubRunStackModal';
import { RootDAGRunContext } from '@/features/dags/contexts/RootDAGRunContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { WorkspaceDataContext } from '../data/context';
import type { WorkspaceRunValue } from '../data/types';
import { useWorkspaceUrl } from '../state/workspaceUrl';

const noop = () => undefined;

/**
 * Hosts the sub-run stack and the RootDAGRunContext of the selected run for
 * the whole workspace, as DAGStatus and the page do in classic mode. Without
 * WorkspaceDataProvider it renders the children unchanged.
 */
export function WorkspaceRunHosts({ children }: { children: ReactNode }) {
  const data = useContext(WorkspaceDataContext);
  if (!data) return <>{children}</>;
  return <RunHosts run={data.run}>{children}</RunHosts>;
}

function RunHosts({
  run,
  children,
}: {
  run: WorkspaceRunValue;
  children: ReactNode;
}) {
  const runId = run.ref?.runId ?? null;
  const [stack, setStack] = useState<SubRunStackEntry[]>([]);
  const [stackRunId, setStackRunId] = useState(runId);
  // A child run belongs to the run it was opened from.
  if (stackRunId !== runId) {
    setStackRunId(runId);
    setStack([]);
  }

  const open = useCallback((entry: SubRunStackEntry) => {
    setStack((prev) => [...prev, entry]);
  }, []);
  useLinkedSubRun(run, setStack);

  const details = run.details;
  // The URL owns the selection, so consumers cannot change it.
  const root = useMemo(
    () => ({ data: details ?? undefined, setData: noop }),
    [details]
  );

  return (
    <RootDAGRunContext.Provider value={root}>
      <SubRunOpenProvider value={open}>
        {children}
        {details && stack.length > 0 && (
          <SubRunStackModal
            rootName={details.rootDAGRunName || details.name}
            rootDAGRunId={details.rootDAGRunId || details.dagRunId}
            rootLabel={details.name}
            stack={stack}
            onChange={setStack}
          />
        )}
      </SubRunOpenProvider>
    </RootDAGRunContext.Provider>
  );
}

/**
 * A link names a sub-run of the run with ?subRun, and one of its steps with
 * ?step. Once the run has loaded, the sub-run opens on the stack and the
 * link leaves the URL.
 */
function useLinkedSubRun(
  run: WorkspaceRunValue,
  show: (stack: SubRunStackEntry[]) => void
) {
  const [url, setUrl] = useWorkspaceUrl();
  const remoteNode = useRemoteNode();
  const linked = url.subRun;
  const ref =
    run.details && run.details.dagRunId === run.ref?.runId ? run.ref : null;
  // The entry needs the name of the sub-run, which the link does not hold.
  const { data, error } = useQuery(
    '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}',
    whenEnabled(!!linked && !!ref, {
      params: {
        path: {
          name: ref?.name ?? '',
          dagRunId: ref?.runId ?? '',
          subDAGRunId: linked?.id ?? '',
        },
        query: { remoteNode },
      },
    })
  );
  const child =
    data?.dagRunDetails?.dagRunId === linked?.id ? data?.dagRunDetails : null;
  const settled = !!child || !!error || run.notFound;
  useEffect(() => {
    if (!linked || !settled) return;
    if (child) {
      show([
        {
          name: child.name,
          dagRunId: child.dagRunId,
          step: linked.step ?? undefined,
        },
      ]);
    }
    setUrl({ subRun: null }, { replace: true });
  }, [child, linked, setUrl, settled, show]);
}
