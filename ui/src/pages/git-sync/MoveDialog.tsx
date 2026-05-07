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

type ItemKind = 'dag' | 'config' | 'memory' | 'skill' | 'soul' | 'doc';

interface MoveDialogProps {
  open: boolean;
  itemId: string;
  itemKind: ItemKind;
  itemStatus: SyncStatus;
  isMoving: boolean;
  onConfirm: (newItemId: string, message: string, force: boolean) => void;
  onCancel: () => void;
}

function deriveKind(id: string): ItemKind {
  if (id === 'base' || /^workspaces\/[^/]+\/base$/.test(id)) return 'config';
  if (id.startsWith('memory/')) return 'memory';
  if (id.startsWith('skills/')) return 'skill';
  if (id.startsWith('souls/')) return 'soul';
  if (id.startsWith('docs/')) return 'doc';
  return 'dag';
}

export function MoveDialog({
  open,
  itemId,
  itemKind,
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
    if (itemKind === 'config') {
      setValidationError('Config items cannot be moved');
      return false;
    }
    const newKind = deriveKind(newItemId.trim());
    if (newKind !== itemKind) {
      setValidationError(
        `Cannot move a ${itemKind} item to a ${newKind} path`
      );
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
          <DialogTitle className="text-base">Move Item</DialogTitle>
          <DialogDescription className="text-xs">
            Rename{' '}
            <span className="font-mono font-medium">{itemId}</span> to a new
            path.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="new-item-id" className="text-xs">
              New Item ID
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
              Commit Message
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
                Force move (override conflicts)
              </Label>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onCancel}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSubmit}
            disabled={isMoving}
          >
            {isMoving ? (
              <>
                <RefreshCw className="h-3.5 w-3.5 mr-1 animate-spin" />
                Moving...
              </>
            ) : (
              <>
                <ArrowRightLeft className="h-3.5 w-3.5 mr-1" />
                Move
              </>
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
