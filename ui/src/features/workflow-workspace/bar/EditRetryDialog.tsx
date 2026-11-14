// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { lazy, Suspense, type ReactElement } from 'react';
import LoadingIndicator from '@/components/ui/loading-indicator';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useI18n } from '@/i18n/I18nProvider';

const DAGSpecReadOnly = lazy(
  () => import('@/features/dags/components/dag-editor/DAGSpecReadOnly')
);

/**
 * The edited retry of a run: its definition in an editor, and a preview
 * that chooses which steps run again and which keep their outputs, for a
 * new run.
 */
export function EditRetryDialog({
  open,
  onClose,
  dagName,
  dagRunId,
  fileName,
  onRetried,
}: {
  open: boolean;
  onClose(): void;
  /** Called with the ID of the new run; the dialog closes. */
  onRetried(dagRunId: string): void;
  dagName: string;
  dagRunId: string;
  /** The file of the workflow, which the run can be compared with. */
  fileName: string;
}): ReactElement {
  const { ts } = useI18n();
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="flex h-[90vh] max-w-6xl flex-col gap-3">
        <DialogHeader>
          <DialogTitle>{ts('Edit and retry')}</DialogTitle>
          <DialogDescription>
            {ts('Edit the definition and choose the steps to run')}
          </DialogDescription>
        </DialogHeader>
        {open && (
          <Suspense fallback={<LoadingIndicator fill delayed />}>
            <DAGSpecReadOnly
              dagName={dagName}
              dagRunId={dagRunId}
              sourceFileName={fileName}
              previewEngine="canvas"
              className="min-h-0 flex-1"
              onRetried={(runId) => {
                onClose();
                onRetried(runId);
              }}
            />
          </Suspense>
        )}
      </DialogContent>
    </Dialog>
  );
}
