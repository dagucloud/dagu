// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';

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
  const [name, setName] = React.useState('');
  const [id, setID] = React.useState('');

  React.useEffect(() => {
    if (!target) {
      setName('');
      setID('');
    }
  }, [target]);

  const matches = target !== null && name === target.name && id === target.id;
  return (
    <Dialog open={target !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete Controller</DialogTitle>
          <DialogDescription>
            This removes the Controller definition and runtime snapshot.
            Referenced DAGs and existing DAG runs are retained.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <p className="text-sm">
            Enter <strong>{target?.name}</strong> and <code>{target?.id}</code>{' '}
            to confirm.
          </p>
          <Input
            aria-label="Controller name confirmation"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
          <Input
            aria-label="Controller ID confirmation"
            value={id}
            onChange={(event) => setID(event.target.value)}
          />
        </div>
        <DialogFooter>
          <Button variant="ghost" disabled={pending} onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            disabled={!matches || pending}
            onClick={() => void onDelete()}
          >
            {pending ? 'Deleting…' : 'Delete'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
