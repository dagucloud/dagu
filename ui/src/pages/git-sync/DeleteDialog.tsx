// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { RefreshCw, Trash2 } from 'lucide-react';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';

interface DeleteDialogProps {
  open: boolean;
  itemId: string;
  itemStatus: SyncStatus;
  isDeleting: boolean;
  onConfirm: (force: boolean) => void;
  onCancel: () => void;
}

function getDeleteMessage(
  itemId: string,
  status: SyncStatus,
  ts: (source: string, values?: Record<string, string | number>) => string
): string {
  switch (status) {
    case SyncStatus.synced:
      return ts(
        'Delete "{id}" from the remote repository and local disk? This will remove the file from both locations.',
        { id: itemId }
      );
    case SyncStatus.missing:
      return ts(
        'Delete "{id}" from the remote repository? The file is already missing locally.',
        { id: itemId }
      );
    case SyncStatus.modified:
      return ts(
        'Delete "{id}"? The file has local modifications that will be lost. This removes it from both remote and local.',
        { id: itemId }
      );
    case SyncStatus.conflict:
      return ts(
        'Delete "{id}"? The file has unresolved conflicts. This removes it from both remote and local.',
        { id: itemId }
      );
    default:
      return ts('Delete "{id}"? This cannot be undone.', { id: itemId });
  }
}

function needsForce(status: SyncStatus): boolean {
  return status === SyncStatus.modified || status === SyncStatus.conflict;
}

export function DeleteDialog({
  open,
  itemId,
  itemStatus,
  isDeleting,
  onConfirm,
  onCancel,
}: DeleteDialogProps) {
  const { ts } = useI18n();
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onCancel()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-base">
            <I18nText text={'Delete Sync Item'} />
          </DialogTitle>
          <DialogDescription className="text-xs">
            <I18nText text={'This action cannot be undone.'} />
          </DialogDescription>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          {getDeleteMessage(itemId, itemStatus, ts)}
        </p>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onCancel}>
            <I18nText text={'Cancel'} />
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => onConfirm(needsForce(itemStatus))}
            disabled={isDeleting}
          >
            {isDeleting ? (
              <>
                <RefreshCw className="h-3.5 w-3.5 mr-1 animate-spin" />
                <I18nText text={'Deleting...'} />
              </>
            ) : (
              <>
                <Trash2 className="h-3.5 w-3.5 mr-1" />
                <I18nText text={'Delete'} />
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
