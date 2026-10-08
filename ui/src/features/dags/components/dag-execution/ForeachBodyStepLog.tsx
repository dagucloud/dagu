// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { components, NodeStatus, Stream } from '../../../../api/v1/schema';
import { isActiveNodeStatus } from '@/lib/status-utils';
import { useForeachItem } from '../../hooks/useForeachQueries';
import type { ForeachLogTarget } from '../../hooks/useStepLogQuery';
import StepLog from './StepLog';

type Props = {
  dagName: string;
  dagRunId: string;
  dagRun?: components['schemas']['DAGRunDetails'];
  bodyStepName: string;
  foreach: ForeachLogTarget;
  stream: Stream;
  /** Status of the body step when the viewer opened. */
  initialStatus?: NodeStatus;
};

/**
 * ForeachBodyStepLog shows a foreach body step's log in the full viewer and
 * keeps it live while that body step runs. No run node describes a body
 * step, so its status is followed through the item record.
 */
function ForeachBodyStepLog({
  dagName,
  dagRunId,
  dagRun,
  bodyStepName,
  foreach,
  stream,
  initialStatus,
}: Props) {
  const [status, setStatus] = React.useState(initialStatus);
  const { data } = useForeachItem(
    { dagName, dagRunId, stepName: foreach.stepName, dagRun },
    foreach.item,
    isActiveNodeStatus(status)
  );
  const recorded = data?.steps.find(
    (step) => step.name === bodyStepName
  )?.status;
  React.useEffect(() => {
    if (recorded !== undefined) {
      setStatus(recorded);
    }
  }, [recorded]);

  return (
    <StepLog
      dagName={dagName}
      dagRunId={dagRunId}
      stepName={bodyStepName}
      dagRun={dagRun}
      stream={stream}
      status={status}
      foreach={foreach}
    />
  );
}

export default ForeachBodyStepLog;
