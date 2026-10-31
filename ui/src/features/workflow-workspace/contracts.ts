// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components, NodeStatus, Status } from '@/api/v1/schema';
import type { DAGPageData } from './data/types';

export type { NodeStatus, Status };
export type ApiDAGDetails = components['schemas']['DAGDetails'];
export type ApiSubRun = components['schemas']['SubDAGRun'];
// One declaration of each data type: data/types.ts owns them.
export type {
  ApiDAGRunDetails,
  ApiNode,
  ApiSubRunDetail,
  DAGPageData,
  HandlerEvent,
  RunHandler,
  RunRef,
  RunSelection,
  WaitingCounts,
  WorkspacePermissions,
  WorkspaceRunValue,
} from './data/types';
// The editor types have one source: the DAGSpec host types and the visual
// editor contracts.
export type {
  SaveState,
  WorkspaceEditorHandle,
  WorkspaceEditorState,
  WorkspaceNotice,
} from '@/features/dags/components/dag-editor/hostTypes';
export type {
  PaletteTarget,
  VisualEditorHandle,
  VisualEditorState,
} from '@/features/visual-editor/contracts';

/**
 * The page (pages/dags/dag/index.tsx) keeps the DAG query, SSE, remote node,
 * workspace scope, UnsavedChangesProvider, RemoteNodeProvider and DAGContext.
 * The workspace is the body only.
 *
 * Component tree, fixed so that later tasks fill files and do not re-plumb:
 * <WorkspaceDataProvider>             data/** (added around the tree in W2)
 *  <RightPanelProvider>               contexts/RightPanelContext.tsx (added in W2)
 *   <WorkspaceRunHosts>               hosts/WorkspaceRunHosts.tsx
 *    <WorkspaceEditorProvider>        canvas/WorkspaceEditorContext.tsx
 *     <SettingsLeaveGuardProvider>    settings/useSettingsLeaveGuard.ts
 *      <DockProvider>                 dock/DockContext.tsx
 *       <RunStartProvider>            bar/RunStartContext.tsx
 *        <WorkflowBar/>               bar/**
 *        <WorkspaceCanvas/>           canvas/WorkspaceCanvas.tsx (one pane)
 *        <LogsDock/>                  dock/LogsDock.tsx
 *        <WorkflowCommands/>          commands/WorkflowCommands.tsx
 * Mode 'popout' renders <LogPopoutPage/> in place of the tree.
 */
export interface WorkflowWorkspaceProps {
  fileName: string;
  /** Route param :tab, after useLegacyRedirect settled. */
  tab: string | undefined;
  dagData: DAGPageData;
  refreshDag(): Promise<unknown>;
  /** workspaceNameFromLabels([...labels, ...tags]) */
  workspaceName: string | null;
  /** Legacy /history?idx=n or /executions?idx=n link, resolved at the root of the workspace. */
  legacyHistoryIdx: number | null;
}

/** Props of every bar slot (identity, chips, run controls, menu). */
export interface BarSlotProps {
  fileName: string;
  dag: ApiDAGDetails;
  workspaceName: string | null;
}
