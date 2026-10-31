// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Types of the props that a host page gives to DAGSpec, and of the handle
 * that DAGSpec gives back.
 *
 * @module features/dags/components/dag-editor
 */
import type { ReactNode, Ref } from 'react';
import type { components } from '@/api/v1/schema';
import type {
  EditorHostProps,
  VisualEditorHandle,
  VisualEditorState,
} from '@/features/visual-editor/contracts';

type DAGDetails = components['schemas']['DAGDetails'];

export type {
  EditorHostProps,
  EditorNotice,
  PaletteTarget,
  RunSource,
  StepCommand,
  StepCommandGates,
  VisualEditorHandle,
  VisualEditorState,
} from '@/features/visual-editor/contracts';

/** The state of the validation of the edited text, as the Save button shows it. */
export type SaveState =
  | 'clean'
  | 'unchecked'
  | 'validating'
  | 'valid'
  | 'warnings'
  | 'errors';

export interface WorkspaceEditorState extends VisualEditorState {
  loaded: boolean;
  editable: boolean;
  dirty: boolean;
  saving: boolean;
  saveState: SaveState;
  hasDraft: boolean;
  conflict: boolean;
}

export interface WorkspaceEditorHandle extends VisualEditorHandle {
  /** Same as the Save button: SaveDiffDialog when dirty and not saving; else no-op. */
  save(): void;
  /** Same as the 'Revert to the last save' button, with no confirmation. */
  discard(): void;
  /** 'Copy to editor': sets the text and marks it dirty. */
  replaceBuffer(text: string): void;
  isDirty(): boolean;
  getBuffer(): string;
  /**
   * The definition that the server built from the buffer, as the canvas
   * shows it: the saved one while a draft is not checked or does not build.
   */
  getDag(): DAGDetails | undefined;
  /** Same as the Copy button over the YAML editor. */
  copyYaml(): Promise<void>;
}

export interface WorkspaceNotice {
  id:
    | 'draft'
    | 'frozen'
    | 'edit-refused'
    | 'run-not-found'
    | 'approval-resume'
    | 'actions-waiting'
    | 'monitoring-offer'
    | 'newer-run-active'
    | 'validation'
    | string;
  tone: 'info' | 'warning' | 'error';
  message: ReactNode;
  action?: { label: ReactNode; run(): void };
  dismissible?: boolean;
  /** Called when the user dismisses a dismissible notice. */
  dismiss?(): void;
  /** The highest shows; the others count in a '+n' popover. */
  priority: number;
}

export interface DAGSpecHostProps extends EditorHostProps {
  /** The root of DAGSpec fills the height of its parent. */
  fillHeight?: boolean;
  /** With it, the YAML and form section is open exactly when state is not null. */
  ndv?: {
    /** 'docked' is the YAML panel of the file; 'expanded' is a step's node view. */
    state: 'docked' | 'expanded' | null;
    onStateChange(s: 'docked' | 'expanded' | null): void;
    /** False for a hidden pane: it follows the state and never corrects it. Default true. */
    active?: boolean;
  };
  /** With chrome.compactNotices: rendered in one notice line (NoticeLine). */
  notices?: WorkspaceNotice[];
  /**
   * Wraps the overview of the Workflow panel with the tabs of the host. It
   * gets the overview and the DAG of the preview. With it the panel is
   * `bare`: the host content brings its own body.
   */
  renderWorkflowPanel?(overview: ReactNode, dag: DAGDetails): ReactNode;
  /** Null until the spec has loaded. */
  editorHandleRef?: Ref<WorkspaceEditorHandle>;
  onEditorStateChange?(s: WorkspaceEditorState): void;
}
