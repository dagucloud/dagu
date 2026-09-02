// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import ConfirmModal from '@/components/ui/confirm-dialog';
import { I18nProps } from '@/i18n/I18nProps';

interface CleanupDialogProps {
  open: boolean;
  missingCount: number;
  isCleaningUp: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function CleanupDialog({
  open,
  missingCount,
  isCleaningUp,
  onConfirm,
  onCancel,
}: CleanupDialogProps) {
  return (
    <I18nProps><ConfirmModal
      title="Cleanup Missing Items"
      buttonText={isCleaningUp ? 'Cleaning up...' : 'Cleanup'}
      visible={open}
      dismissModal={onCancel}
      onSubmit={onConfirm}
    >
      <p className="text-sm text-muted-foreground">
        Remove {missingCount} missing item{missingCount !== 1 ? 's' : ''} from
        sync tracking? Files remain in the remote repository.
      </p>
    </ConfirmModal></I18nProps>
  );
}
