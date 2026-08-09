// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { AlertTriangle, RefreshCw, X } from 'lucide-react';

type Props = {
  visible: boolean;
  onDiscard: () => void;
  onIgnore: () => void;
};

function WikiPageExternalChangeDialog({ visible, onDiscard, onIgnore }: Props) {
  return (
    <Dialog open={visible} onOpenChange={(open) => !open && onIgnore()}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <AlertTriangle className="h-5 w-5 text-warning" />
            External Changes Detected
          </DialogTitle>
        </DialogHeader>

        <div className="py-4 space-y-3">
          <p className="text-sm text-muted-foreground">
            This Wiki page has been modified externally, possibly by another
            process or user.
          </p>
          <div className="text-sm space-y-1">
            <p className="font-medium">What would you like to do?</p>
            <ul className="text-muted-foreground space-y-1 ml-4 list-disc">
              <li>
                <strong>Discard & Reload:</strong> Lose your changes and load
                the latest version
              </li>
              <li>
                <strong>Ignore:</strong> Keep your changes (you may overwrite
                external changes when saving)
              </li>
            </ul>
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={onIgnore}>
            <X className="h-4 w-4" />
            Ignore
          </Button>
          <Button variant="primary" onClick={onDiscard}>
            <RefreshCw className="h-4 w-4" />
            Discard & Reload
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default WikiPageExternalChangeDialog;
