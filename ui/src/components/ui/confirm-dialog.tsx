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
  type DialogLayer,
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
  /** 'raised' puts the dialog above the node view; see DialogLayer. */
  layer?: DialogLayer;
  /** A short second choice between Cancel and the confirm button. */
  secondary?: { text: string; onClick: () => void };
  /**
   * The button that has the focus when the dialog opens. 'confirm' is the
   * default: Enter confirms. A destructive question passes 'cancel'.
   */
  initialFocus?: 'confirm' | 'cancel';
  /**
   * When true, the focus goes back to the element that had it before the
   * dialog opened, if that element is still on the page.
   */
  restoreFocus?: boolean;
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
  layer,
  secondary,
  initialFocus = 'confirm',
  restoreFocus = false,
}: ConfirmDialogProps) {
  const cancelButtonRef = React.useRef<HTMLButtonElement>(null);
  const submitButtonRef = React.useRef<HTMLButtonElement>(null);
  const returnTo = React.useRef<HTMLElement | null>(null);

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
        layer={layer}
        className={cn(
          !fullscreen && !contentClassName && 'sm:max-w-[500px]',
          contentClassName
        )}
        onOpenAutoFocus={(event) => {
          const active = document.activeElement;
          returnTo.current = active instanceof HTMLElement ? active : null;
          event.preventDefault();
          // A disabled button cannot take the focus.
          const target =
            initialFocus === 'cancel' || submitDisabled
              ? cancelButtonRef
              : submitButtonRef;
          target.current?.focus();
        }}
        onCloseAutoFocus={(event) => {
          if (!restoreFocus) return;
          event.preventDefault();
          if (returnTo.current?.isConnected) returnTo.current.focus();
          returnTo.current = null;
        }}
      >
        <DialogHeader className={headerClassName}>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription className="sr-only">
            <I18nText text={'Confirm the requested action.'} />
          </DialogDescription>
        </DialogHeader>

        <div className={cn('py-4', bodyClassName)}>{children}</div>

        <DialogFooter className={footerClassName}>
          <Button ref={cancelButtonRef} variant="ghost" onClick={dismissModal}>
            <X className="h-4 w-4" />
            <I18nText text={'Cancel'} />
          </Button>
          {secondary && (
            <Button variant="outline" onClick={secondary.onClick}>
              {secondary.text}
            </Button>
          )}
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
