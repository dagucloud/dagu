import React, { useEffect, useState } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { buildDAGPageURL } from '@/features/dag-runs/lib/dagRunUrls';
import { components, NodeStatus, Stream } from '../../../../api/v1/schema';
import ExecutionLog from './ExecutionLog';
import LogSideModal from './LogSideModal';
import ForeachBodyStepLog from './ForeachBodyStepLog';
import StepLog from './StepLog';
import {
  type ForeachLogTarget,
  isSubDAGRun,
} from '../../hooks/useStepLogQuery';

type LogViewerProps = {
  isOpen: boolean;
  onClose: () => void;
  logType: 'execution' | 'step';
  dagName: string;
  /** Workflow file that addresses the full-page log when the run names none */
  fileName?: string;
  dagRunId: string;
  stepName?: string;
  isInModal?: boolean;
  dagRun?: components['schemas']['DAGRunDetails'];
  stream?: Stream;
  node?: components['schemas']['Node'];
  /** Shows a body step log of a foreach item; stepName is then the body step */
  foreach?: ForeachLogTarget;
  /** Status of the foreach body step when the viewer opened */
  bodyStepStatus?: NodeStatus;
};

/**
 * LogViewer is a wrapper component that displays logs in a side modal
 * It can show either execution logs or step logs based on the logType prop
 */
const LogViewer: React.FC<LogViewerProps> = ({
  isOpen,
  onClose,
  logType,
  dagName,
  fileName,
  dagRunId,
  stepName,
  isInModal = true,
  dagRun,
  stream = Stream.stdout,
  node,
  foreach,
  bodyStepStatus,
}) => {
  // Determine the title based on the log type
  const stepTitle = foreach
    ? `${foreach.stepName} › #${foreach.item} › ${stepName}`
    : stepName;
  const title =
    logType === 'execution'
      ? `Execution Log: ${dagName}`
      : `Step log: ${stepTitle}`;

  const remoteNode = useRemoteNode();
  // A step log can switch streams in place; the new-tab link follows it.
  const [shownStream, setShownStream] = useState(stream);
  useEffect(() => {
    setShownStream(stream);
  }, [stream]);
  const sourceFile = dagRun?.sourceFileName || fileName;
  const subRun = isSubDAGRun(dagRun);
  const isStepLog = logType === 'step';
  // The full-page log has no route for a foreach body step.
  const newTabUrl =
    sourceFile && !foreach
      ? buildDAGPageURL({
          fileName: sourceFile,
          remoteNode,
          tab: isStepLog ? 'log' : 'dagRun-log',
          rootDAGRunId: subRun ? dagRun?.rootDAGRunId : dagRunId,
          rootDAGRunName: subRun ? dagRun?.rootDAGRunName : undefined,
          subDAGRunId: subRun ? dagRun?.dagRunId : undefined,
          step: isStepLog ? stepName : undefined,
          stream:
            isStepLog && shownStream !== Stream.stdout
              ? shownStream
              : undefined,
        })
      : undefined;

  return (
    <LogSideModal
      isOpen={isOpen}
      onClose={onClose}
      title={title}
      isInModal={isInModal}
      newTabUrl={newTabUrl}
    >
      <div className="h-full">
        {logType === 'execution' ? (
          <ExecutionLog name={dagName} dagRunId={dagRunId} dagRun={dagRun} />
        ) : (
          stepName &&
          (foreach ? (
            <ForeachBodyStepLog
              dagName={dagName}
              dagRunId={dagRunId}
              dagRun={dagRun}
              bodyStepName={stepName}
              foreach={foreach}
              stream={stream}
              initialStatus={bodyStepStatus}
            />
          ) : (
            <StepLog
              dagName={dagName}
              dagRunId={dagRunId}
              stepName={stepName}
              dagRun={dagRun}
              stream={stream}
              node={node}
              onStreamShown={setShownStream}
            />
          ))
        )}
      </div>
    </LogSideModal>
  );
};

export default LogViewer;
