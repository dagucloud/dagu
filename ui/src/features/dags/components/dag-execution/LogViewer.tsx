import React from 'react';
import { components, Stream } from '../../../../api/v1/schema';
import ExecutionLog from './ExecutionLog';
import LogSideModal from './LogSideModal';
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
          <ExecutionLog
            name={dagName}
            dagRunId={dagRunId}
            dagRun={dagRun}
          />
        ) : (
          stepName && (
            <StepLog
              dagName={dagName}
              dagRunId={dagRunId}
              stepName={stepName}
              dagRun={dagRun}
              stream={stream}
              node={node}
              foreach={foreach}
            />
          )
        )}
      </div>
    </LogSideModal>
  );
};

export default LogViewer;
