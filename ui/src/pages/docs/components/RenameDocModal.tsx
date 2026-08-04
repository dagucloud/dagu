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
import { Pencil, X } from 'lucide-react';
import { useCallback, useEffect, useState } from 'react';
import { validateDocPath } from '../lib/doc-validation';

interface RenameDocModalProps {
  isOpen: boolean;
  onClose: () => void;
  onSubmit: (newPath: string) => Promise<void>;
  currentPath: string;
  isLoading?: boolean;
  externalError?: string | null;
}

export function RenameDocModal({
  isOpen,
  onClose,
  onSubmit,
  currentPath,
  isLoading = false,
  externalError = null,
}: RenameDocModalProps) {
  const [newPath, setNewPath] = useState('');
  const [validationError, setValidationError] = useState<string | null>(null);

  useEffect(() => {
    if (isOpen) {
      setNewPath(currentPath);
      setValidationError(null);
    }
  }, [isOpen, currentPath]);

  const handlePathChange = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      setNewPath(e.target.value);
      if (validationError) {
        setValidationError(null);
      }
    },
    [validationError]
  );

  const handleSubmit = useCallback(
    async (e: React.FormEvent) => {
      e.preventDefault();
      const trimmed = newPath.trim();
      const validation = validateDocPath(trimmed);
      if (!validation.isValid) {
        setValidationError(validation.error || 'Invalid path');
        return;
      }
      if (trimmed === currentPath) {
        setValidationError('New path must be different from current path');
        return;
      }
      await onSubmit(trimmed);
    },
    [newPath, currentPath, onSubmit]
  );

  const currentError = validationError || externalError;

  return (
    <Dialog open={isOpen} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-[425px]">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>Rename Document</DialogTitle>
            <DialogDescription>
              Enter a new path for the document.
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 py-4">
            <div className="grid grid-cols-4 items-center gap-4">
              <Label className="text-right text-muted-foreground">
                Current
              </Label>
              <div className="col-span-3 font-mono text-sm bg-muted px-3 py-1.5 rounded-md truncate">
                {currentPath}
              </div>
            </div>
            <div className="grid grid-cols-4 items-center gap-4">
              <Label htmlFor="new-doc-path" className="text-right">
                New Path
              </Label>
              <Input
                id="new-doc-path"
                value={newPath}
                onChange={handlePathChange}
                className="col-span-3 font-mono"
                placeholder="docs/deployment"
                autoFocus
                disabled={isLoading}
              />
            </div>
            {currentError && (
              <div className="text-destructive text-sm px-4">
                {currentError}
              </div>
            )}
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={onClose}
              disabled={isLoading}
            >
              <X className="h-4 w-4" />
              Cancel
            </Button>
            <Button type="submit" disabled={isLoading}>
              <Pencil className="h-4 w-4" />
              {isLoading ? 'Renaming...' : 'Rename'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
