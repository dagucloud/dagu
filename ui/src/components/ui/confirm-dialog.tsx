// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Check, X } from 'lucide-react';
import React from 'react';

import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { I18nText } from '@/i18n/I18nText';

type ConfirmDialogProps = {
  title: string;
  buttonText: string;
  children: React.ReactNode;
  visible: boolean;
  dismissModal: () => void;
  onSubmit: () => void;
  submitDisabled?: boolean;
  fullscreen?: boolean;
  contentClassName?: string;
  headerClassName?: string;
  bodyClassName?: string;
  footerClassName?: string;
};

function ConfirmDialog({
  children,
  title,
  buttonText,
  visible,
  dismissModal,
  onSubmit,
  submitDisabled = false,
  fullscreen = false,
  contentClassName,
  headerClassName,
  bodyClassName,
  footerClassName,
}: ConfirmDialogProps) {
  const cancelButtonRef = React.useRef<HTMLButtonElement>(null);
  const submitButtonRef = React.useRef<HTMLButtonElement>(null);

  React.useEffect(() => {
    if (!visible || !submitButtonRef.current) {
      return;
    }

    requestAnimationFrame(() => {
      submitButtonRef.current?.focus();
    });
  }, [visible]);

  React.useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!visible || event.key !== 'Enter') {
        return;
      }

      const activeElement = document.activeElement;
      const isInputFocused =
        activeElement instanceof HTMLInputElement ||
        activeElement instanceof HTMLTextAreaElement ||
        activeElement instanceof HTMLSelectElement;

      if (isInputFocused) {
        return;
      }

      if (activeElement === cancelButtonRef.current) {
        event.preventDefault();
        dismissModal();
        return;
      }

      if (activeElement === submitButtonRef.current) {
        event.preventDefault();
        if (!submitDisabled) {
          onSubmit();
        }
        return;
      }

      if (activeElement instanceof HTMLButtonElement) {
        return;
      }

      event.preventDefault();
      if (!submitDisabled) {
        onSubmit();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [visible, onSubmit, dismissModal, submitDisabled]);

  return (
    <Dialog open={visible} onOpenChange={(open) => !open && dismissModal()}>
      <DialogContent
        fullscreen={fullscreen}
        className={cn(
          !fullscreen && !contentClassName && 'sm:max-w-[500px]',
          contentClassName
        )}
      >
        <DialogHeader className={headerClassName}>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="sr-only">
            <I18nText text={"Confirm the requested action."} />
          </DialogDescription>
        </DialogHeader>

        <div className={cn('py-4', bodyClassName)}>{children}</div>

        <DialogFooter className={footerClassName}>
          <Button ref={cancelButtonRef} variant="ghost" onClick={dismissModal}>
            <X className="h-4 w-4" />
            <I18nText text={"Cancel"} />
          </Button>
          <Button
            ref={submitButtonRef}
            variant="primary"
            disabled={submitDisabled}
            onClick={onSubmit}
          >
            <Check className="h-4 w-4" />
            {buttonText}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export type { ConfirmDialogProps };
export { ConfirmDialog };
export default ConfirmDialog;
