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
import { RefreshCw, Trash2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { I18nText } from '@/i18n/I18nText';

interface DeleteMissingDialogProps {
  open: boolean;
  missingCount: number;
  isDeletingMissing: boolean;
  onConfirm: (message: string) => void;
  onCancel: () => void;
}

export function DeleteMissingDialog({
  open,
  missingCount,
  isDeletingMissing,
  onConfirm,
  onCancel,
}: DeleteMissingDialogProps) {
  const defaultMessage = `Remove ${missingCount} missing sync item${missingCount !== 1 ? 's' : ''}`;
  const [commitMessage, setCommitMessage] = useState('');

  useEffect(() => {
    if (open) {
      setCommitMessage('');
    }
  }, [open]);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onCancel()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-base">
            <I18nText text={'Delete All Missing Items'} />
          </DialogTitle>
          <DialogDescription className="text-xs">
            <I18nText
              text={
                missingCount === 1
                  ? 'This will remove {count} missing item from the remote repository and sync state. This action cannot be undone.'
                  : 'This will remove {count} missing items from the remote repository and sync state. This action cannot be undone.'
              }
              values={{ count: missingCount }}
            />
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="delete-missing-msg" className="text-xs">
              <I18nText text={'Commit Message'} />
            </Label>
            <Input
              id="delete-missing-msg"
              className="h-8 text-sm"
              placeholder={defaultMessage}
              value={commitMessage}
              onChange={(e) => setCommitMessage(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !isDeletingMissing) {
                  e.preventDefault();
                  onConfirm(commitMessage.trim() || defaultMessage);
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
            onClick={() => onConfirm(commitMessage.trim() || defaultMessage)}
            disabled={isDeletingMissing}
          >
            {isDeletingMissing ? (
              <>
                <RefreshCw className="h-3.5 w-3.5 mr-1 animate-spin" />
                <I18nText text={'Deleting...'} />
              </>
            ) : (
              <>
                <Trash2 className="h-3.5 w-3.5 mr-1" />
                <I18nText text={'Delete All Missing'} />
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
