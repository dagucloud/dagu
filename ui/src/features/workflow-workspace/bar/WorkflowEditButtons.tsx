// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { PencilLine, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { useDAGEditActions } from '@/features/dags/components/dag-editor/DAGEditButtons';
import { useI18n } from '@/i18n/I18nProvider';
import type { BarSlotProps } from '../contracts';

type EditSlot = Pick<BarSlotProps, 'fileName' | 'workspaceName'>;

/**
 * The Rename button, a pencil that sits next to the workflow name. It opens
 * the rename dialog. Without write permission on the workspace of the
 * workflow, it renders nothing.
 */
export function WorkflowRenameButton({ fileName, workspaceName }: EditSlot) {
  const { ts } = useI18n();
  const edit = useDAGEditActions(fileName, workspaceName);
  if (!edit.canWrite) return null;
  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        data-testid="ww-bar-rename"
        aria-label={ts('Rename')}
        title={ts('Rename')}
        onClick={edit.openRename}
        className="shrink-0"
      >
        <PencilLine aria-hidden="true" />
      </Button>
      {edit.dialogs}
    </>
  );
}

/**
 * The Delete button, a trash icon at the end of the bar. It opens the delete
 * confirmation. Without write permission on the workspace of the workflow,
 * it renders nothing.
 */
export function WorkflowDeleteButton({ fileName, workspaceName }: EditSlot) {
  const { ts } = useI18n();
  const edit = useDAGEditActions(fileName, workspaceName);
  if (!edit.canWrite) return null;
  return (
    <>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        data-testid="ww-bar-delete"
        aria-label={ts('Delete')}
        title={ts('Delete')}
        onClick={edit.openDelete}
        // On the default accent the red text is under 4.5:1.
        className="text-destructive hover:text-destructive"
      >
        <Trash2 aria-hidden="true" />
      </Button>
      {edit.dialogs}
    </>
  );
}
