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

interface DeleteDialogProps {
  open: boolean;
  itemId: string;
  itemStatus: SyncStatus;
  isDeleting: boolean;
  onConfirm: (force: boolean) => void;
  onCancel: () => void;
}

function getDeleteMessage(itemId: string, status: SyncStatus): string {
  switch (status) {
    case SyncStatus.synced:
      return `Delete "${itemId}" from the remote repository and local disk? This will remove the file from both locations.`;
    case SyncStatus.missing:
      return `Delete "${itemId}" from the remote repository? The file is already missing locally.`;
    case SyncStatus.modified:
      return `Delete "${itemId}"? The file has local modifications that will be lost. This removes it from both remote and local.`;
    case SyncStatus.conflict:
      return `Delete "${itemId}"? The file has unresolved conflicts. This removes it from both remote and local.`;
    default:
      return `Delete "${itemId}"? This cannot be undone.`;
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
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onCancel()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-base">Delete Sync Item</DialogTitle>
          <DialogDescription className="text-xs">
            This action cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">
          {getDeleteMessage(itemId, itemStatus)}
        </p>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onCancel}>
            Cancel
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
                Deleting...
              </>
            ) : (
              <>
                <Trash2 className="h-3.5 w-3.5 mr-1" />
                Delete
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
