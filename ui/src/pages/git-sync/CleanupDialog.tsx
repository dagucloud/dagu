// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import ConfirmModal from '@/components/ui/confirm-dialog';
import { I18nProps } from '@/i18n/I18nProps';
import { I18nText } from '@/i18n/I18nText';

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
        <I18nText text={"Remove"} /> {missingCount} <I18nText text={"missing item"} />{missingCount !== 1 ? 's' : ''} <I18nText text={"from sync tracking? Files remain in the remote repository."} />
      </p>
    </ConfirmModal></I18nProps>
  );
}
