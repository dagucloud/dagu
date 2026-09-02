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
import { I18nText } from '@/i18n/I18nText';

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
            <I18nText text={"External Changes Detected"} />
          </DialogTitle>
        </DialogHeader>

        <div className="py-4 space-y-3">
          <p className="text-sm text-muted-foreground">
            <I18nText text={"This Wiki page has been modified externally, possibly by another process or user."} />
          </p>
          <div className="text-sm space-y-1">
            <p className="font-medium"><I18nText text={"What would you like to do?"} /></p>
            <ul className="text-muted-foreground space-y-1 ml-4 list-disc">
              <li>
                <strong><I18nText text={"Discard & Reload:"} /></strong> <I18nText text={"Lose your changes and load the latest version"} />
              </li>
              <li>
                <strong><I18nText text={"Ignore:"} /></strong> <I18nText text={"Keep your changes (you may overwrite external changes when saving)"} />
              </li>
            </ul>
          </div>
        </div>

        <DialogFooter>
          <Button variant="ghost" onClick={onIgnore}>
            <X className="h-4 w-4" />
            <I18nText text={"Ignore"} />
          </Button>
          <Button variant="primary" onClick={onDiscard}>
            <RefreshCw className="h-4 w-4" />
            <I18nText text={"Discard & Reload"} />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default WikiPageExternalChangeDialog;
