// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState } from 'react';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import { useWorkspaceRun } from '../data';
import { useRunSpec } from '../executions/useRunSpec';

export interface CopyRunSpecToEditor {
  /** The editor can take the definition of the selected run now. */
  canCopy: boolean;
  /** Copies at once, or asks first when the editor has unsaved changes. */
  request(): void;
  /** True while the confirmation of the replace is open. */
  confirming: boolean;
  /** The user accepted the replace. */
  confirm(): void;
  /** The user declined the replace. */
  cancel(): void;
}

/**
 * Puts the definition of the selected run in the editor text. The editor
 * handle refuses the text while the editor cannot be edited, so `canCopy`
 * is false then. `onCopied` runs after the text was replaced.
 */
export function useCopyRunSpecToEditor(
  fileName: string,
  onCopied?: () => void
): CopyRunSpecToEditor {
  const run = useWorkspaceRun();
  const editor = useWorkspaceEditor();
  const spec = useRunSpec(run.ref, fileName);
  const [confirming, setConfirming] = useState(false);
  const canCopy =
    !!editor.handle &&
    !!editor.state?.editable &&
    !editor.state.frozen &&
    spec.text !== null &&
    !spec.missing;

  // The editor or the run can change while the confirmation is open.
  const confirm = () => {
    setConfirming(false);
    if (!canCopy || !editor.handle || spec.text === null) return;
    editor.handle.replaceBuffer(spec.text);
    onCopied?.();
  };
  const request = () => {
    if (editor.state?.dirty) setConfirming(true);
    else confirm();
  };

  return {
    canCopy,
    request,
    confirming,
    confirm,
    cancel: () => setConfirming(false),
  };
}
