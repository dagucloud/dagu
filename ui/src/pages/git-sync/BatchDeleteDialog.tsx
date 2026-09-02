// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { RefreshCw, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';

interface BatchDeleteDialogProps {
  open: boolean;
  itemIds: string[];
  hasModifiedOrConflict: boolean;
  isDeletingBatch: boolean;
  onConfirm: (message: string, force: boolean) => void;
  onCancel: () => void;
}

export function BatchDeleteDialog({
  open,
  itemIds,
  hasModifiedOrConflict,
  isDeletingBatch,
  onConfirm,
  onCancel,
}: BatchDeleteDialogProps) {
  const { ts } = useI18n();
  const count = itemIds.length;
  const defaultMessage = ts(
    count === 1
      ? 'Delete {count} sync item'
      : 'Delete {count} sync items',
    { count }
  );
  const [commitMessage, setCommitMessage] = useState('');

  useEffect(() => {
    if (open) {
      setCommitMessage('');
    }
  }, [open]);

  const handleConfirm = () => {
    onConfirm(commitMessage.trim() || defaultMessage, hasModifiedOrConflict);
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onCancel()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-base">
            <I18nText text={'Delete Selected Items'} />
          </DialogTitle>
          <DialogDescription className="text-xs">
            {ts(
              count === 1
                ? 'This will remove {count} sync item from the remote repository, local disk, and sync state. This action cannot be undone.'
                : 'This will remove {count} sync items from the remote repository, local disk, and sync state. This action cannot be undone.',
              { count }
            )}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          {hasModifiedOrConflict && (
            <p className="text-xs text-amber-600 dark:text-amber-400">
              <I18nText
                text={
                  'Some items have local modifications or conflicts that will be lost.'
                }
              />
            </p>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="batch-delete-msg" className="text-xs">
              <I18nText text={'Commit Message'} />
            </Label>
            <Input
              id="batch-delete-msg"
              className="h-8 text-sm"
              placeholder={defaultMessage}
              value={commitMessage}
              onChange={(e) => setCommitMessage(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !isDeletingBatch) {
                  e.preventDefault();
                  handleConfirm();
                }
              }}
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onCancel}>
            <I18nText text={'Cancel'} />
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={handleConfirm}
            disabled={isDeletingBatch}
          >
            {isDeletingBatch ? (
              <>
                <RefreshCw className="h-3.5 w-3.5 mr-1 animate-spin" />
                <I18nText text={'Deleting...'} />
              </>
            ) : (
              <>
                <Trash2 className="h-3.5 w-3.5 mr-1" />
                {ts(
                  count === 1 ? 'Delete {count} item' : 'Delete {count} items',
                  {
                    count,
                  }
                )}
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
