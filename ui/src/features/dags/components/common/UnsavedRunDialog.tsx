// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AlertTriangle } from 'lucide-react';
import ConfirmModal from '@/components/ui/confirm-dialog';
import { useI18n } from '@/i18n/I18nProvider';

type Props = {
  visible: boolean;
  /** The editor can save its edits: the dialog offers to save and run. */
  canSave: boolean;
  onDismiss: () => void;
  /** Runs the last saved version; the edits stay unsaved. */
  onRunSaved: () => void;
  /** Saves the edits, then runs them. */
  onSaveAndRun: () => void;
};

/**
 * Asks what a Start with unsaved edits runs. A user who can save gets
 * Save & Run as the confirm button, with the last saved version as the second
 * choice; any other user can only run the last saved version.
 */
export function UnsavedRunDialog({
  visible,
  canSave,
  onDismiss,
  onRunSaved,
  onSaveAndRun,
}: Props) {
  const { ts } = useI18n();
  const runAnyway = ts('Run anyway');
  return (
    <ConfirmModal
      title={ts('Unsaved Changes')}
      buttonText={canSave ? ts('Save & Run') : runAnyway}
      visible={visible}
      dismissModal={onDismiss}
      onSubmit={canSave ? onSaveAndRun : onRunSaved}
      secondary={canSave ? { text: runAnyway, onClick: onRunSaved } : undefined}
    >
      <div className="flex items-start gap-3">
        <AlertTriangle className="h-5 w-5 text-warning flex-shrink-0 mt-0.5" />
        <div className="space-y-2">
          <p className="font-medium">
            {ts('You have unsaved changes in the DAG definition.')}
          </p>
          <p className="text-sm text-muted-foreground">
            {canSave
              ? ts(
                  'Save & Run saves your edits and runs them. Run anyway runs the last saved version, without your edits.'
                )
              : ts(
                  'The DAG will run with the last saved version, not your current edits. Save your changes first if you want them to take effect.'
                )}
          </p>
        </div>
      </div>
    </ConfirmModal>
  );
}
