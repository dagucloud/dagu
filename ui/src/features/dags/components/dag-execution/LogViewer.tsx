import React from 'react';
import { components, NodeStatus, Stream } from '../../../../api/v1/schema';
import ExecutionLog from './ExecutionLog';
import LogSideModal from './LogSideModal';
import ForeachBodyStepLog from './ForeachBodyStepLog';
import StepLog from './StepLog';
import type { ForeachLogTarget } from '../../hooks/useStepLogQuery';

type LogViewerProps = {
  isOpen: boolean;
  onClose: () => void;
  logType: 'execution' | 'step';
  dagName: string;
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
      : `Step Log (${stream}): ${stepTitle}`;

  return (
    <LogSideModal
      isOpen={isOpen}
      onClose={onClose}
      title={title}
      isInModal={isInModal}
      dagName={dagName}
      dagRunId={dagRunId}
      stepName={foreach ? undefined : stepName}
      logType={logType}
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
            />
          ))
        )}
      </div>
    </LogSideModal>
  );
};

export default LogViewer;
