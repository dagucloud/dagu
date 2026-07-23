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
type DeleteTarget = { id: string; name: string };

export function ControllerDeleteDialog({
  target,
  pending,
  onClose,
  onDelete,
}: {
  target: DeleteTarget | null;
  pending: boolean;
  onClose: () => void;
  onDelete: () => Promise<void>;
}) {
  return (
    <Dialog
      open={target !== null}
      onOpenChange={(open) => !open && !pending && onClose()}
    >
      <DialogContent hideCloseButton={pending}>
        <DialogHeader>
          <DialogTitle>Delete Controller</DialogTitle>
          <DialogDescription>
            This removes the Controller definition and runtime snapshot.
            Referenced DAGs and existing DAG runs are retained.
          </DialogDescription>
        </DialogHeader>
        <p className="text-sm">
          Delete <strong>{target?.name}</strong> (<code>{target?.id}</code>)?
        </p>
        <DialogFooter>
          <Button variant="ghost" disabled={pending} onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={pending}
            onClick={() => void onDelete()}
          >
            {pending ? 'Deleting…' : 'Delete'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
