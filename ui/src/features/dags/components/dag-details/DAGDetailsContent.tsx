import { Tabs } from '@/components/ui/tabs';
import {
  Bell,
  FileCode,
  History,
  PlayCircle,
  ScrollText,
  Webhook,
} from 'lucide-react';
import React, { useState } from 'react';
import { components } from '../../../../api/v1/schema';
import { workspaceNameFromLabels } from '../../../../lib/workspace';
import { DAGStatus } from '../../components';
import { DAGContext } from '../../contexts/DAGContext';
import { LinkTab } from '../common';
import ModalLinkTab from '../common/ModalLinkTab';
import { DAGEditButtons, DAGSpec } from '../dag-editor';
import {
  DAGExecutionHistory,
  ExecutionLog,
  LogViewer,
  StepLog,
} from '../dag-execution';
import { DAGHeader } from './';
import NotificationsTab from './NotificationsTab';
import WebhookTab from './WebhookTab';

type DAGDetailsContentProps = {
  fileName: string;
  filePath?: string;
  dag: components['schemas']['DAGDetails'];
  currentDAGRun?: components['schemas']['DAGRunDetails'];
  refreshFn: () => void;
  formatDuration: (startDate: string, endDate: string) => string;
  activeTab: string;
  onTabChange?: (tab: string) => void;
  dagRunId?: string;
  stepName?: string | null;
  isModal?: boolean;
  navigateToStatusTab?: () => void;
  skipHeader?: boolean;
  localDags?: components['schemas']['LocalDag'][];
  editorHints?: components['schemas']['DAGEditorHints'];
  /** Custom enqueue handler, threaded to DAGHeader → DAGActions */
  onEnqueue?: (
    params: string,
    dagRunId?: string,
    immediate?: boolean
  ) => string | void | Promise<string | void>;
  onRunStarted?: (dagRunId: string) => void | Promise<void>;
  /** When true, forces enqueue mode in DAGContext (used by cockpit) */
  forceEnqueue?: boolean;
  /** When true, automatically opens the start/enqueue modal on mount */
  autoOpenStartModal?: boolean;
};

type LogViewerState = {
  isOpen: boolean;
  logType: 'execution' | 'step';
  stepName?: string;
};

const DAGDetailsContent: React.FC<DAGDetailsContentProps> = ({
  fileName,
  filePath,
  dag,
  currentDAGRun,
  refreshFn,
  formatDuration,
  activeTab,
  onTabChange,
  dagRunId = 'latest',
  stepName = null,
  isModal = false,
  navigateToStatusTab,
  skipHeader = false,
  localDags,
  editorHints,
  onEnqueue,
  onRunStarted,
  forceEnqueue = false,
  autoOpenStartModal = false,
}) => {
  const baseUrl = isModal ? '#' : `/dags/${fileName}`;
  const [logViewer, setLogViewer] = useState<LogViewerState>({
    isOpen: false,
    logType: 'execution',
    stepName: undefined,
  });
  const dagWorkspaceName = React.useMemo(
    () => workspaceNameFromLabels([...(dag.labels ?? []), ...(dag.tags ?? [])]),
    [dag.labels, dag.tags]
  );

  const handleTabClick = (tab: string) => {
    if (onTabChange) {
      onTabChange(tab);
    }

    // Open log viewer when clicking on log tabs
    if (tab === 'dagRun-log') {
      setLogViewer({
        isOpen: true,
        logType: 'execution',
      });
    } else if (tab === 'log' && stepName) {
      setLogViewer({
        isOpen: true,
        logType: 'step',
        stepName,
      });
    }
  };

  const closeLogViewer = () => {
    setLogViewer((prev) => ({ ...prev, isOpen: false }));
  };

  return (
    <DAGContext.Provider
      value={{
        refresh: refreshFn,
        fileName: fileName || '',
        name: dag?.name || '',
        forceEnqueue,
        autoOpenStartModal,
        onEnqueue,
        onRunStarted,
      }}
    >
      <div className="w-full flex flex-col">
        {/* Only render the header if skipHeader is not true */}
        {!skipHeader && (
          <DAGHeader
            dag={dag}
            currentDAGRun={currentDAGRun}
            fileName={fileName || ''}
            filePath={filePath}
            refreshFn={refreshFn}
            formatDuration={formatDuration}
            navigateToStatusTab={navigateToStatusTab}
          />
        )}
        <div className="flex flex-col lg:flex-row justify-between items-center gap-3 lg:gap-0 mb-4 mt-3">
          {/* Desktop Tabs (lg and up) */}
          <div className="hidden lg:block flex-1 min-w-0">
            <Tabs className="whitespace-nowrap">
              {isModal ? (
                <ModalLinkTab
                  label="Latest Run"
                  value="status"
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                  onClick={() => handleTabClick('status')}
                />
              ) : (
                <LinkTab
                  label="Latest Run"
                  value={`${baseUrl}`}
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label="Spec"
                  value="spec"
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                  onClick={() => handleTabClick('spec')}
                />
              ) : (
                <LinkTab
                  label="Spec"
                  value={`${baseUrl}/spec`}
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label="Webhook"
                  value="webhook"
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                  onClick={() => handleTabClick('webhook')}
                />
              ) : (
                <LinkTab
                  label="Webhook"
                  value={`${baseUrl}/webhook`}
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label="Notifications"
                  value="notifications"
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                  onClick={() => handleTabClick('notifications')}
                />
              ) : (
                <LinkTab
                  label="Notifications"
                  value={`${baseUrl}/notifications`}
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label="History"
                  value="history"
                  isActive={activeTab === 'history'}
                  icon={History}
                  onClick={() => handleTabClick('history')}
                />
              ) : (
                <LinkTab
                  label="History"
                  value={`${baseUrl}/history`}
                  isActive={activeTab === 'history'}
                  icon={History}
                />
              )}

              {(activeTab === 'log' || activeTab === 'dagRun-log') &&
                (isModal ? (
                  <ModalLinkTab
                    label="Log"
                    value={activeTab}
                    isActive={true}
                    icon={ScrollText}
                    onClick={() => {}}
                  />
                ) : (
                  <LinkTab
                    label="Log"
                    value={baseUrl}
                    isActive={true}
                    icon={ScrollText}
                  />
                ))}
            </Tabs>
          </div>

          {/* Mobile/Tablet Tabs (sm to lg) */}
          <div className="lg:hidden w-full overflow-x-auto">
            <div className="flex space-x-1 w-full">
              {isModal ? (
                <ModalLinkTab
                  label=""
                  value="status"
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                  onClick={() => handleTabClick('status')}
                  className="flex-1 justify-center"
                  aria-label="Latest Run"
                />
              ) : (
                <LinkTab
                  label=""
                  value={`${baseUrl}`}
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                  className="flex-1 justify-center"
                  aria-label="Latest Run"
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label=""
                  value="spec"
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                  onClick={() => handleTabClick('spec')}
                  className="flex-1 justify-center"
                  aria-label="Spec"
                />
              ) : (
                <LinkTab
                  label=""
                  value={`${baseUrl}/spec`}
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                  className="flex-1 justify-center"
                  aria-label="Spec"
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label=""
                  value="webhook"
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                  onClick={() => handleTabClick('webhook')}
                  className="flex-1 justify-center"
                  aria-label="Webhook"
                />
              ) : (
                <LinkTab
                  label=""
                  value={`${baseUrl}/webhook`}
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                  className="flex-1 justify-center"
                  aria-label="Webhook"
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label=""
                  value="notifications"
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                  onClick={() => handleTabClick('notifications')}
                  className="flex-1 justify-center"
                  aria-label="Notifications"
                />
              ) : (
                <LinkTab
                  label=""
                  value={`${baseUrl}/notifications`}
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                  className="flex-1 justify-center"
                  aria-label="Notifications"
                />
              )}

              {isModal ? (
                <ModalLinkTab
                  label=""
                  value="history"
                  isActive={activeTab === 'history'}
                  icon={History}
                  onClick={() => handleTabClick('history')}
                  className="flex-1 justify-center"
                  aria-label="History"
                />
              ) : (
                <LinkTab
                  label=""
                  value={`${baseUrl}/history`}
                  isActive={activeTab === 'history'}
                  icon={History}
                  className="flex-1 justify-center"
                  aria-label="History"
                />
              )}

              {(activeTab === 'log' || activeTab === 'dagRun-log') &&
                (isModal ? (
                  <ModalLinkTab
                    label=""
                    value={activeTab}
                    isActive={true}
                    icon={ScrollText}
                    onClick={() => {}}
                    className="flex-1 justify-center"
                    aria-label="Log"
                  />
                ) : (
                  <LinkTab
                    label=""
                    value={baseUrl}
                    isActive={true}
                    icon={ScrollText}
                    className="flex-1 justify-center"
                    aria-label="Log"
                  />
                ))}
            </div>
          </div>

          <div className={activeTab === 'spec' ? 'visible' : 'hidden'}>
            <DAGEditButtons
              fileName={fileName || ''}
              workspace={dagWorkspaceName}
            />
          </div>
        </div>
        <div className="flex-1 flex flex-col min-h-0">
          {activeTab === 'status' && currentDAGRun ? (
            <>
              <DAGStatus
                dagRun={currentDAGRun}
                fileName={fileName || ''}
                artifactEnabled={!!dag.artifacts?.enabled}
              />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'spec' ? (
            <DAGSpec
              key={fileName}
              fileName={fileName}
              localDags={localDags}
              editorHints={editorHints}
            />
          ) : null}
          {activeTab === 'history' ? (
            <>
              <DAGExecutionHistory fileName={fileName || ''} />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'webhook' ? (
            <>
              <WebhookTab fileName={fileName || ''} />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'notifications' ? (
            <>
              <NotificationsTab fileName={fileName || ''} />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'dagRun-log' ? (
            <ExecutionLog
              name={dag?.name || ''}
              dagRunId={dagRunId}
              dagRun={currentDAGRun}
            />
          ) : null}
          {activeTab === 'log' && stepName ? (
            <StepLog
              dagName={dag?.name || ''}
              dagRunId={dagRunId}
              stepName={stepName}
              dagRun={currentDAGRun}
            />
          ) : null}

          {/* Log viewer modal */}
          <LogViewer
            isOpen={logViewer.isOpen}
            onClose={closeLogViewer}
            logType={logViewer.logType}
            dagName={dag?.name || ''}
            dagRunId={dagRunId}
            stepName={logViewer.stepName}
            isInModal={isModal}
            dagRun={currentDAGRun}
          />
        </div>
      </div>
    </DAGContext.Provider>
  );
};

export default DAGDetailsContent;
