// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Tabs } from '@/components/ui/tabs';
import { cn } from '@/lib/utils';
import {
  AlertTriangle,
  Bell,
  BookOpen,
  FileCode,
  History,
  PlayCircle,
  ScrollText,
  Settings as SettingsIcon,
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
import DAGSettingsTab from './DAGSettingsTab';
import DAGWikiTab from './DAGWikiTab';
import IncidentsTab from './IncidentsTab';
import NotificationsTab from './NotificationsTab';
import WebhookTab from './WebhookTab';
import { I18nProps } from '@/i18n/I18nProps';

type DAGDetailsContentProps = {
  fileName: string;
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
    immediate?: boolean,
    profile?: string,
    noReuse?: boolean
  ) => string | void | Promise<string | void>;
  onRunStarted?: (dagRunId: string) => void | Promise<void>;
  /** When true, forces enqueue mode in DAGContext (used by cockpit) */
  forceEnqueue?: boolean;
  /** When true, automatically opens the start/enqueue modal on mount */
  autoOpenStartModal?: boolean;
  buildScopedUrl?: (path: string) => string;
  fillHeight?: boolean;
};

type LogViewerState = {
  isOpen: boolean;
  logType: 'execution' | 'step';
  stepName?: string;
};

const DAGDetailsContent: React.FC<DAGDetailsContentProps> = ({
  fileName,
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
  buildScopedUrl,
  fillHeight = false,
}) => {
  const baseUrl = isModal ? '#' : `/dags/${fileName}`;
  const scopedUrl = React.useCallback(
    (path: string) => (buildScopedUrl ? buildScopedUrl(path) : path),
    [buildScopedUrl]
  );
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
      <div
        className={cn(
          'flex w-full min-w-0 flex-col',
          fillHeight && 'h-full min-h-0'
        )}
      >
        {/* Only render the header if skipHeader is not true */}
        {!skipHeader && (
          <DAGHeader
            dag={dag}
            currentDAGRun={currentDAGRun}
            fileName={fileName || ''}
            refreshFn={refreshFn}
            formatDuration={formatDuration}
            navigateToStatusTab={navigateToStatusTab}
            buildScopedUrl={buildScopedUrl}
          />
        )}
        <div className="mb-4 mt-3 flex min-w-0 flex-col items-center justify-between gap-3 lg:flex-row 2xl:gap-0">
          {/* Desktop Tabs */}
          <div className="hidden min-w-0 flex-1 overflow-x-auto 2xl:block">
            <Tabs className="whitespace-nowrap">
              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Latest Run"
                  value="status"
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                  onClick={() => handleTabClick('status')}
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Latest Run"
                  value={scopedUrl(baseUrl)}
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Spec"
                  value="spec"
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                  onClick={() => handleTabClick('spec')}
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Spec"
                  value={scopedUrl(`${baseUrl}/spec`)}
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Webhook"
                  value="webhook"
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                  onClick={() => handleTabClick('webhook')}
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Webhook"
                  value={scopedUrl(`${baseUrl}/webhook`)}
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Settings"
                  value="settings"
                  isActive={activeTab === 'settings'}
                  icon={SettingsIcon}
                  onClick={() => handleTabClick('settings')}
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Settings"
                  value={scopedUrl(`${baseUrl}/settings`)}
                  isActive={activeTab === 'settings'}
                  icon={SettingsIcon}
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Notifications"
                  value="notifications"
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                  onClick={() => handleTabClick('notifications')}
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Notifications"
                  value={scopedUrl(`${baseUrl}/notifications`)}
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Incidents"
                  value="incidents"
                  isActive={activeTab === 'incidents'}
                  icon={AlertTriangle}
                  onClick={() => handleTabClick('incidents')}
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Incidents"
                  value={scopedUrl(`${baseUrl}/incidents`)}
                  isActive={activeTab === 'incidents'}
                  icon={AlertTriangle}
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="History"
                  value="history"
                  isActive={activeTab === 'history'}
                  icon={History}
                  onClick={() => handleTabClick('history')}
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="History"
                  value={scopedUrl(`${baseUrl}/history`)}
                  isActive={activeTab === 'history'}
                  icon={History}
                /></I18nProps>
              )}

              {isModal && (
                <I18nProps><ModalLinkTab
                  label="Wiki"
                  value="wiki"
                  isActive={activeTab === 'wiki' || activeTab === 'docs'}
                  icon={BookOpen}
                  onClick={() => handleTabClick('wiki')}
                /></I18nProps>
              )}

              {(activeTab === 'log' || activeTab === 'dagRun-log') &&
                (isModal ? (
                  <I18nProps><ModalLinkTab
                    label="Log"
                    value={activeTab}
                    isActive={true}
                    icon={ScrollText}
                    onClick={() => {}}
                  /></I18nProps>
                ) : (
                  <I18nProps><LinkTab
                    label="Log"
                    value={scopedUrl(baseUrl)}
                    isActive={true}
                    icon={ScrollText}
                  /></I18nProps>
                ))}
            </Tabs>
          </div>

          {/* Compact Tabs */}
          <div className="w-full min-w-0 overflow-x-auto lg:flex-1 2xl:hidden">
            <div className="flex min-w-max space-x-1">
              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Latest Run"
                  value="status"
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                  onClick={() => handleTabClick('status')}
                  className="flex-1 justify-center"
                  aria-label="Latest Run"
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Latest Run"
                  value={scopedUrl(baseUrl)}
                  isActive={activeTab === 'status'}
                  icon={PlayCircle}
                  className="flex-1 justify-center"
                  aria-label="Latest Run"
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Incidents"
                  value="incidents"
                  isActive={activeTab === 'incidents'}
                  icon={AlertTriangle}
                  onClick={() => handleTabClick('incidents')}
                  className="flex-1 justify-center"
                  aria-label="Incidents"
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Incidents"
                  value={scopedUrl(`${baseUrl}/incidents`)}
                  isActive={activeTab === 'incidents'}
                  icon={AlertTriangle}
                  className="flex-1 justify-center"
                  aria-label="Incidents"
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Spec"
                  value="spec"
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                  onClick={() => handleTabClick('spec')}
                  className="flex-1 justify-center"
                  aria-label="Spec"
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Spec"
                  value={scopedUrl(`${baseUrl}/spec`)}
                  isActive={activeTab === 'spec'}
                  icon={FileCode}
                  className="flex-1 justify-center"
                  aria-label="Spec"
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Webhook"
                  value="webhook"
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                  onClick={() => handleTabClick('webhook')}
                  className="flex-1 justify-center"
                  aria-label="Webhook"
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Webhook"
                  value={scopedUrl(`${baseUrl}/webhook`)}
                  isActive={activeTab === 'webhook'}
                  icon={Webhook}
                  className="flex-1 justify-center"
                  aria-label="Webhook"
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Settings"
                  value="settings"
                  isActive={activeTab === 'settings'}
                  icon={SettingsIcon}
                  onClick={() => handleTabClick('settings')}
                  className="flex-1 justify-center"
                  aria-label="Settings"
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Settings"
                  value={scopedUrl(`${baseUrl}/settings`)}
                  isActive={activeTab === 'settings'}
                  icon={SettingsIcon}
                  className="flex-1 justify-center"
                  aria-label="Settings"
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="Notifications"
                  value="notifications"
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                  onClick={() => handleTabClick('notifications')}
                  className="flex-1 justify-center"
                  aria-label="Notifications"
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="Notifications"
                  value={scopedUrl(`${baseUrl}/notifications`)}
                  isActive={activeTab === 'notifications'}
                  icon={Bell}
                  className="flex-1 justify-center"
                  aria-label="Notifications"
                /></I18nProps>
              )}

              {isModal ? (
                <I18nProps><ModalLinkTab
                  label="History"
                  value="history"
                  isActive={activeTab === 'history'}
                  icon={History}
                  onClick={() => handleTabClick('history')}
                  className="flex-1 justify-center"
                  aria-label="History"
                /></I18nProps>
              ) : (
                <I18nProps><LinkTab
                  label="History"
                  value={scopedUrl(`${baseUrl}/history`)}
                  isActive={activeTab === 'history'}
                  icon={History}
                  className="flex-1 justify-center"
                  aria-label="History"
                /></I18nProps>
              )}

              {isModal && (
                <I18nProps><ModalLinkTab
                  label="Wiki"
                  value="wiki"
                  isActive={activeTab === 'wiki' || activeTab === 'docs'}
                  icon={BookOpen}
                  onClick={() => handleTabClick('wiki')}
                  className="flex-1 justify-center"
                  aria-label="Wiki"
                /></I18nProps>
              )}

              {(activeTab === 'log' || activeTab === 'dagRun-log') &&
                (isModal ? (
                  <I18nProps><ModalLinkTab
                    label="Log"
                    value={activeTab}
                    isActive={true}
                    icon={ScrollText}
                    onClick={() => {}}
                    className="flex-1 justify-center"
                    aria-label="Log"
                  /></I18nProps>
                ) : (
                  <I18nProps><LinkTab
                    label="Log"
                    value={scopedUrl(baseUrl)}
                    isActive={true}
                    icon={ScrollText}
                    className="flex-1 justify-center"
                    aria-label="Log"
                  /></I18nProps>
                ))}
            </div>
          </div>

          <div className={activeTab === 'spec' ? 'visible shrink-0' : 'hidden'}>
            <DAGEditButtons
              fileName={fileName || ''}
              workspace={dagWorkspaceName}
            />
          </div>
        </div>
        <div className="flex min-h-0 flex-1 flex-col">
          {activeTab === 'status' && currentDAGRun ? (
            <>
              <DAGStatus
                dagRun={currentDAGRun}
                fileName={fileName || ''}
                artifactEnabled={!!dag.artifacts?.enabled}
                fillHeight={fillHeight}
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
          {activeTab === 'settings' ? (
            <>
              <DAGSettingsTab fileName={fileName || ''} />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'notifications' ? (
            <>
              <NotificationsTab
                fileName={fileName || ''}
                workspaceName={dagWorkspaceName}
              />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'incidents' ? (
            <>
              <IncidentsTab
                fileName={fileName || ''}
                workspaceName={dagWorkspaceName}
              />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'wiki' || activeTab === 'docs' ? (
            <>
              <DAGWikiTab
                dagName={dag?.name || ''}
                workspaceName={dagWorkspaceName}
              />
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
