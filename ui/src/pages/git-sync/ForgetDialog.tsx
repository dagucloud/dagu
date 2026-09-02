// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import ConfirmModal from '@/components/ui/confirm-dialog';
import { I18nProps } from '@/i18n/I18nProps';

interface ForgetDialogProps {
  open: boolean;
  itemId: string;
  isForgetting: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

export function ForgetDialog({
  open,
  itemId,
  isForgetting,
  onConfirm,
  onCancel,
}: ForgetDialogProps) {
  return (
    <I18nProps><ConfirmModal
      title="Forget Sync Item"
      buttonText={isForgetting ? 'Forgetting...' : 'Forget'}
      visible={open}
      dismissModal={onCancel}
      onSubmit={onConfirm}
    >
      <p className="text-sm text-muted-foreground">
        Remove{' '}
        <span className="font-mono font-medium text-foreground break-all">
          {itemId}
        </span>{' '}
        from sync tracking? This does not delete the file from the remote
        repository.
      </p>
    </ConfirmModal></I18nProps>
  );
}
