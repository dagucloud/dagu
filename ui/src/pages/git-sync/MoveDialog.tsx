// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { SyncStatus } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
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
import { ArrowRightLeft, RefreshCw } from 'lucide-react';
import { useEffect, useState } from 'react';
import { I18nText } from '@/i18n/I18nText';
import { I18nTemplate } from '@/i18n/I18nTemplate';

interface MoveDialogProps {
  open: boolean;
  itemId: string;
  itemStatus: SyncStatus;
  isMoving: boolean;
  onConfirm: (newItemId: string, message: string, force: boolean) => void;
  onCancel: () => void;
}

export function MoveDialog({
  open,
  itemId,
  itemStatus,
  isMoving,
  onConfirm,
  onCancel,
}: MoveDialogProps) {
  const [newItemId, setNewItemId] = useState('');
  const [commitMessage, setCommitMessage] = useState('');
  const [force, setForce] = useState(false);
  const [validationError, setValidationError] = useState('');

  useEffect(() => {
    if (open) {
      setNewItemId('');
      setCommitMessage('');
      setForce(false);
      setValidationError('');
    }
  }, [open]);

  const validate = (): boolean => {
    if (!newItemId.trim()) {
      setValidationError('New item ID is required');
      return false;
    }
    if (newItemId.trim() === itemId) {
      setValidationError('New item ID must be different from the current one');
      return false;
    }
    setValidationError('');
    return true;
  };

  const handleSubmit = () => {
    if (!validate()) return;
    const msg = commitMessage.trim() || `Move ${itemId} to ${newItemId.trim()}`;
    onConfirm(newItemId.trim(), msg, force);
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onCancel()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-base">
            <I18nText text={'Move Item'} />
          </DialogTitle>
          <DialogDescription className="text-xs">
            <I18nTemplate
              text="Rename {item} to a new path."
              values={{
                item: <span className="font-mono font-medium">{itemId}</span>,
              }}
            />
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="new-item-id" className="text-xs">
              <I18nText text={'New Item ID'} />
            </Label>
            <Input
              id="new-item-id"
              className="h-8 text-sm font-mono"
              placeholder={itemId}
              value={newItemId}
              onChange={(e) => {
                setNewItemId(e.target.value);
                if (validationError) setValidationError('');
              }}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !isMoving) {
                  e.preventDefault();
                  handleSubmit();
                }
              }}
            />
            {validationError && (
              <p className="text-xs text-destructive">{validationError}</p>
            )}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="move-commit-msg" className="text-xs">
              <I18nText text={'Commit Message'} />
            </Label>
            <Input
              id="move-commit-msg"
              className="h-8 text-sm"
              placeholder={`Move ${itemId} to ...`}
              value={commitMessage}
              onChange={(e) => setCommitMessage(e.target.value)}
            />
          </div>
          {itemStatus === SyncStatus.conflict && (
            <div className="flex items-center gap-2">
              <Checkbox
                id="move-force"
                checked={force}
                onCheckedChange={(checked) => setForce(checked === true)}
              />
              <Label htmlFor="move-force" className="text-xs">
                <I18nText text={'Force move (override conflicts)'} />
              </Label>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onCancel}>
            <I18nText text={'Cancel'} />
          </Button>
          <Button size="sm" onClick={handleSubmit} disabled={isMoving}>
            {isMoving ? (
              <>
                <RefreshCw className="h-3.5 w-3.5 mr-1 animate-spin" />
                <I18nText text={'Moving...'} />
              </>
            ) : (
              <>
                <ArrowRightLeft className="h-3.5 w-3.5 mr-1" />
                <I18nText text={'Move'} />
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
