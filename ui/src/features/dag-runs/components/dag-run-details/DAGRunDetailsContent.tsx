import React from 'react';
import { components } from '../../../../api/v1/schema';
import { useRemoteNode } from '../../../../contexts/RemoteNodeContext';
import { DAGStatus } from '../../../../features/dags/components';
import type { StatusTab } from '../../../../features/dags/components/DAGStatus';
import { cn } from '../../../../lib/utils';
import { DAGRunContext } from '../../contexts/DAGRunContext';
import { useBoundedDAGRunDetails } from '../../hooks/useBoundedDAGRunDetails';
import DAGRunHeader from './DAGRunHeader';

type DAGRunDetailsContentProps = {
  name: string;
  dagRun: components['schemas']['DAGRunDetails'];
  refreshFn: () => void;
  dagRunId?: string;
  initialTab?: StatusTab;
  fillHeight?: boolean;
};

const DAGRunDetailsContent: React.FC<DAGRunDetailsContentProps> = ({
  name,
  dagRun,
  refreshFn,
  dagRunId = 'latest',
  initialTab = 'status',
  fillHeight = false,
}) => {
  const remoteNode = useRemoteNode();
  const isSubDAGRun =
    dagRun.rootDAGRunId !== dagRun.dagRunId &&
    Boolean(dagRun.rootDAGRunId && dagRun.rootDAGRunName);
  const { data: rootDAGRun, refresh: refreshRootDAGRun } =
    useBoundedDAGRunDetails({
      target: isSubDAGRun
        ? {
            remoteNode,
            name: dagRun.rootDAGRunName,
            dagRunId: dagRun.rootDAGRunId,
          }
        : null,
      enabled: isSubDAGRun,
      pollIntervalMs: isSubDAGRun ? 2000 : 0,
    });
  const refresh = React.useCallback(() => {
    refreshFn();
    void refreshRootDAGRun();
  }, [refreshFn, refreshRootDAGRun]);

  return (
    <DAGRunContext.Provider
      value={{
        refresh,
        name: name || '',
        dagRunId: dagRunId || '',
        rootStatus: rootDAGRun?.status,
      }}
    >
      <div
        className={cn('flex w-full flex-col', fillHeight && 'h-full min-h-0')}
      >
        {/* Display breadcrumbs and DAG-run details in the header */}
        <DAGRunHeader
          dagRun={dagRun}
          rootDAGRun={rootDAGRun ?? undefined}
          refreshFn={refresh}
        />

        <div className={cn('flex-1', fillHeight && 'min-h-0')}>
          <DAGStatus
            dagRun={dagRun}
            fileName={name || ''}
            initialTab={initialTab}
            fillHeight={fillHeight}
          />
        </div>
      </div>
    </DAGRunContext.Provider>
  );
};

export default DAGRunDetailsContent;
