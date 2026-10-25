// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import LoadingIndicator from '@/components/ui/loading-indicator';
import { Tabs } from '@/components/ui/tabs';
import { useScrollFade } from '@/hooks/useScrollFade';
import { cn } from '@/lib/utils';
import {
  AlertTriangle,
  Bell,
  BookOpen,
  FileCode,
  PlayCircle,
  ScrollText,
  Settings as SettingsIcon,
  Webhook,
  type LucideIcon,
} from 'lucide-react';
import React, { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { components, Stream } from '../../../../api/v1/schema';
import { workspaceNameFromLabels } from '../../../../lib/workspace';
import { DAGStatus } from '../../components';
import { DAGContext } from '../../contexts/DAGContext';
import { LinkTab } from '../common';
import ModalLinkTab from '../common/ModalLinkTab';
import { DAGEditButtons, DAGSpec } from '../dag-editor';
import {
  ExecutionLog,
  LogViewer,
  RunHistoryGrid,
  StepLog,
} from '../dag-execution';
import { useDAGRunHistory } from '../../hooks/useDAGRunHistory';
import { isAlwaysOnDAG } from '@/lib/dagSchedule';
import { DAGHeader } from './';
import DAGSettingsTab from './DAGSettingsTab';
import DAGWikiTab from './DAGWikiTab';
import IncidentsTab from './IncidentsTab';
import NotificationsTab from './NotificationsTab';
import { useOptionalDAGTabs } from './useOptionalDAGTabs';
import WebhookTab from './WebhookTab';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type DAGDetailsContentProps = {
  fileName: string;
  dag: components['schemas']['DAGDetails'];
  currentDAGRun?: components['schemas']['DAGRunDetails'];
  refreshFn: () => void;
  activeTab: string;
  onTabChange?: (tab: string) => void;
  dagRunId?: string;
  stepName?: string | null;
  /** Stream shown by the full-page step log */
  logStream?: Stream;
  isModal?: boolean;
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
  /** When true, forces enqueue mode in DAGContext (used by cockpit) */
  forceEnqueue?: boolean;
  /** When true, automatically opens the start/enqueue modal on mount */
  autoOpenStartModal?: boolean;
  buildScopedUrl?: (path: string) => string;
  fillHeight?: boolean;
  /** Whether the DAG is pinned, shown in the header */
  pinned?: boolean;
  /** Called after the user pins or unpins the DAG */
  onPinnedChange?: () => void;
  /**
   * Called with the run that the run picker selected, or undefined while the
   * latest run is shown. A page that draws the header itself uses it to show
   * the same run in the header.
   */
  onPickedRunChange?: (
    run: components['schemas']['DAGRunDetails'] | undefined
  ) => void;
};

type DetailTab = {
  value: string;
  label: string;
  icon: LucideIcon;
  /** Path below the workflow page; the modal switches tabs in place. */
  path: string;
  active: boolean;
};

// Form-like tabs keep a readable line length on wide screens.
const FORM_TAB_MAX_WIDTH = 'w-full max-w-5xl';

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
  activeTab,
  onTabChange,
  dagRunId = 'latest',
  stepName = null,
  logStream = Stream.stdout,
  isModal = false,
  skipHeader = false,
  localDags,
  editorHints,
  onEnqueue,
  forceEnqueue = false,
  autoOpenStartModal = false,
  buildScopedUrl,
  fillHeight = false,
  pinned,
  onPinnedChange,
  onPickedRunChange,
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

  const [tabRowRef, tabRowFade] = useScrollFade<HTMLDivElement>('x');
  // The overview, history and spec tabs put their own controls in the tab
  // row, so the graph of each starts at the same place.
  const [toolbarSlot, setToolbarSlot] = useState<HTMLDivElement | null>(null);

  // The overview shows the latest run, and the grid of the latest runs picks
  // another one. A DAG that restarts all the time would stream its history
  // every few seconds, so an always-on DAG reads it at a slow interval instead.
  // The history holds the full details of up to 30 runs, so it is read once
  // the grid is near the screen, or at once when the URL names a picked run.
  const [gridWanted, setGridWanted] = useState(false);
  useEffect(() => setGridWanted(false), [fileName]);
  const wantGrid = useCallback(() => setGridWanted(true), []);
  // On the DAG page the picked run is in the URL (?run=), so a reload, the back
  // button and a shared link show the same run. In a panel over the DAG list it
  // stays in the state of the panel: the query string is the one of the list,
  // and a run named there would follow the person to the next DAG. No value
  // means the latest run.
  const [searchParams, setSearchParams] = useSearchParams();
  const [panelRunId, setPanelRunId] = useState<string | null>(null);
  useEffect(() => setPanelRunId(null), [fileName]);
  const pickedRunId = isModal ? panelRunId : searchParams.get('run');
  const history = useDAGRunHistory(fileName || '', {
    enabled: activeTab === 'status' && (gridWanted || !!pickedRunId),
    live: !isAlwaysOnDAG(dag),
  });
  const setPickedRunId = useCallback(
    (dagRunId: string | null) => {
      if (isModal) {
        setPanelRunId(dagRunId);
        return;
      }
      setSearchParams(
        (current) => {
          const next = new URLSearchParams(current);
          if (dagRunId) {
            next.set('run', dagRunId);
          } else {
            next.delete('run');
          }
          return next;
        },
        { replace: true }
      );
    },
    [isModal, setSearchParams]
  );
  const pickedRun = pickedRunId
    ? history.dagRuns.find((run) => run.dagRunId === pickedRunId)
    : undefined;
  const shownRun = pickedRun ?? currentDAGRun;
  const onPickedRunChangeRef = React.useRef(onPickedRunChange);
  onPickedRunChangeRef.current = onPickedRunChange;
  useEffect(() => {
    onPickedRunChangeRef.current?.(pickedRun);
  }, [pickedRun]);
  const showLogTab = activeTab === 'log' || activeTab === 'dagRun-log';
  const optionalTabs = useOptionalDAGTabs(activeTab);
  const tabs: DetailTab[] = [
    { value: 'status', label: 'Overview', icon: PlayCircle, path: '' },
    { value: 'spec', label: 'Spec', icon: FileCode, path: '/spec' },
    { value: 'webhook', label: 'Webhook', icon: Webhook, path: '/webhook' },
    ...(optionalTabs.settings
      ? [
          {
            value: 'settings',
            label: 'Settings',
            icon: SettingsIcon,
            path: '/settings',
          },
        ]
      : []),
    {
      value: 'notifications',
      label: 'Notifications',
      icon: Bell,
      path: '/notifications',
    },
    {
      value: 'incidents',
      label: 'Incidents',
      icon: AlertTriangle,
      path: '/incidents',
    },
    // The Wiki has its own page; only the modal shows it as a tab.
    ...(isModal
      ? [{ value: 'wiki', label: 'Wiki', icon: BookOpen, path: '/wiki' }]
      : []),
  ].map((tab) => ({
    ...tab,
    active:
      activeTab === tab.value || (tab.value === 'wiki' && activeTab === 'docs'),
  }));
  if (showLogTab) {
    tabs.push({
      value: activeTab,
      label: 'Log',
      icon: ScrollText,
      path: '',
      active: true,
    });
  }

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
            currentDAGRun={shownRun}
            fileName={fileName || ''}
            refreshFn={refreshFn}
            buildScopedUrl={buildScopedUrl}
            pinned={pinned}
            onPinnedChange={onPinnedChange}
          />
        )}
        <div className="mb-4 mt-3 flex min-w-0 flex-col items-center justify-between gap-3 lg:flex-row 2xl:gap-0">
          <div
            ref={tabRowRef}
            style={tabRowFade}
            className="w-full min-w-0 overflow-x-auto lg:flex-1"
          >
            <Tabs className="min-w-max whitespace-nowrap">
              {tabs.map((tab) =>
                isModal ? (
                  <I18nProps key={tab.value}>
                    <ModalLinkTab
                      label={tab.label}
                      value={tab.value}
                      isActive={tab.active}
                      icon={tab.icon}
                      onClick={() => handleTabClick(tab.value)}
                    />
                  </I18nProps>
                ) : (
                  <I18nProps key={tab.value}>
                    <LinkTab
                      label={tab.label}
                      value={scopedUrl(`${baseUrl}${tab.path}`)}
                      isActive={tab.active}
                      icon={tab.icon}
                    />
                  </I18nProps>
                )
              )}
            </Tabs>
          </div>

          <div
            ref={setToolbarSlot}
            className="flex flex-wrap items-center justify-end gap-2 empty:hidden lg:shrink-0"
          />
          <div className={activeTab === 'spec' ? 'visible shrink-0' : 'hidden'}>
            <DAGEditButtons
              fileName={fileName || ''}
              workspace={dagWorkspaceName}
            />
          </div>
        </div>
        <div className="flex min-h-0 flex-1 flex-col">
          {activeTab === 'status' && !currentDAGRun && dagRunId !== 'latest' && (
            <div role="status" className="flex min-h-64 items-center justify-center gap-2">
              <LoadingIndicator />
              <I18nText text="Loading run..." />
            </div>
          )}
          {activeTab === 'status' && shownRun ? (
            <>
              <DAGStatus
                dagRun={shownRun}
                fileName={fileName || ''}
                artifactEnabled={!!dag.artifacts?.enabled}
                fillHeight={fillHeight}
                summary
                showRunMeta={false}
                toolbarSlot={toolbarSlot}
                afterGraph={
                  <>
                    <NearScreen key={fileName} onNear={wantGrid} />
                    <RunHistoryGrid
                      dagRuns={history.dagRuns}
                      gridData={history.gridData}
                      selectedRunId={pickedRunId}
                      onSelect={setPickedRunId}
                    />
                  </>
                }
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
              toolbarSlot={toolbarSlot}
            />
          ) : null}
          {activeTab === 'webhook' ? (
            <>
              <WebhookTab fileName={fileName || ''} dagName={dag.name} />
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'settings' ? (
            <>
              <div className={FORM_TAB_MAX_WIDTH}>
                <DAGSettingsTab fileName={fileName || ''} />
              </div>
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'notifications' ? (
            <>
              <div className={FORM_TAB_MAX_WIDTH}>
                <NotificationsTab
                  fileName={fileName || ''}
                  workspaceName={dagWorkspaceName}
                />
              </div>
              <div className="h-6 flex-shrink-0" />
            </>
          ) : null}
          {activeTab === 'incidents' ? (
            <>
              <div className={FORM_TAB_MAX_WIDTH}>
                <IncidentsTab
                  fileName={fileName || ''}
                  workspaceName={dagWorkspaceName}
                />
              </div>
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
              stream={logStream}
              node={currentDAGRun?.nodes?.find(
                (node) => node.step.name === stepName
              )}
            />
          ) : null}

          {/* Log viewer modal */}
          <LogViewer
            isOpen={logViewer.isOpen}
            onClose={closeLogViewer}
            logType={logViewer.logType}
            dagName={dag?.name || ''}
            fileName={fileName}
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

/**
 * Calls onNear once, when this spot is within 200 px of the screen. Without
 * IntersectionObserver it calls it at once.
 */
function NearScreen({ onNear }: { onNear: () => void }): React.ReactElement {
  const ref = React.useRef<HTMLDivElement>(null);
  useEffect(() => {
    const node = ref.current;
    if (!node || typeof IntersectionObserver === 'undefined') {
      onNear();
      return;
    }
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) {
          onNear();
          observer.disconnect();
        }
      },
      { rootMargin: '200px' }
    );
    observer.observe(node);
    return () => observer.disconnect();
  }, [onNear]);
  return <div ref={ref} aria-hidden="true" className="h-px" />;
}
