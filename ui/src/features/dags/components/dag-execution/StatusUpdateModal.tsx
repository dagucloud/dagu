// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * StatusUpdateModal component provides a modal dialog for manually updating a step's status.
 *
 * @module features/dags/components/dag-execution
 */
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { CheckCircle2, XCircle } from 'lucide-react';
import React from 'react';
import { components, NodeStatus } from '../../../../api/v1/schema';
import { I18nText } from '@/i18n/I18nText';

/**
 * Props for the StatusUpdateModal component
 */
type Props = {
  /** Whether the modal is visible */
  visible: boolean;
  /** Function to close the modal */
  dismissModal: () => void;
  /** Step to update status for */
  step?: components['schemas']['Step'];
  /** Function called when the user submits the status update */
  onSubmit: (
    step: components['schemas']['Step'],
    status: NodeStatus
  ) => void | Promise<void>;
  /** Optional position for the modal (x, y coordinates) */
  position?: { x: number; y: number };
};

/**
 * StatusUpdateModal allows manually setting a step's status to success or failure
 */
function StatusUpdateModal({ visible, dismissModal, step, onSubmit }: Props) {
  // Don't render if no step is provided
  if (!step) {
    return null;
  }

  // Create refs for the buttons
  const successButtonRef = React.useRef<HTMLButtonElement>(null);
  const failedButtonRef = React.useRef<HTMLButtonElement>(null);

  // Track which button is selected (0 = success, 1 = failed)
  const [selectedButton, setSelectedButton] = React.useState(0);
  const submitStatus = React.useCallback(
    (status: NodeStatus) => {
      void (async () => {
        try {
          await onSubmit(step, status);
        } catch {
          // Submit handlers are expected to surface their own user-facing errors.
        }
      })();
    },
    [onSubmit, step]
  );

  // Handle keyboard events
  React.useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      // Only handle events when modal is visible
      if (!visible || !step) return;

      switch (e.key) {
        case 'ArrowLeft':
        case 'ArrowRight':
          e.preventDefault();
          setSelectedButton(selectedButton === 0 ? 1 : 0);
          break;

        case 'Enter':
          e.preventDefault();
          if (selectedButton === 0) {
            submitStatus(NodeStatus.Success);
          } else {
            submitStatus(NodeStatus.Failed);
          }
          break;

        case 'Escape':
          e.preventDefault();
          dismissModal();
          break;
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => {
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [visible, step, submitStatus, dismissModal, selectedButton]);

  // We're not using the position prop anymore as we want the modal to be centered
  // The Dialog component from shadcn/ui will center the modal by default

  return (
    <Dialog open={visible} onOpenChange={(open) => !open && dismissModal()}>
      <DialogContent className="sm:max-w-[400px]">
        <DialogHeader>
          <DialogTitle className="text-base font-mono">
            <I18nText text={"Update Status"} />
          </DialogTitle>
          <p className="text-sm text-muted-foreground font-mono mt-1">
            {step.name}
          </p>
        </DialogHeader>

        <div className="py-6">
          <div className="grid grid-cols-2 gap-3">
            <button
              ref={successButtonRef}
              className={`
                group relative overflow-hidden rounded-lg border p-4 transition-all duration-200 focus:outline-none
                ${
                  selectedButton === 0
                    ? 'border-success bg-success-muted'
                    : 'border-border hover:border-success hover:bg-success-muted'
                }
              `}
              onClick={() => submitStatus(NodeStatus.Success)}
              onMouseEnter={() => setSelectedButton(0)}
            >
              <div className="flex flex-col items-center gap-2">
                <CheckCircle2 className="h-8 w-8 text-success" />
                <span className="font-mono text-sm"><I18nText text={"Success"} /></span>
              </div>
            </button>

            <button
              ref={failedButtonRef}
              className={`
                group relative overflow-hidden rounded-lg border p-4 transition-all duration-200 focus:outline-none
                ${
                  selectedButton === 1
                    ? 'border-error bg-error-muted'
                    : 'border-border hover:border-error hover:bg-error-muted'
                }
              `}
              onClick={() => submitStatus(NodeStatus.Failed)}
              onMouseEnter={() => setSelectedButton(1)}
            >
              <div className="flex flex-col items-center gap-2">
                <XCircle className="h-8 w-8 text-error" />
                <span className="font-mono text-sm"><I18nText text={"Failed"} /></span>
              </div>
            </button>
          </div>
        </div>

        <div className="flex items-center gap-3 text-xs text-muted-foreground font-mono pt-2 border-t border-border">
          <span><I18nText text={"←→ Select"} /></span>
          <span className="opacity-40">•</span>
          <span><I18nText text={"Enter: Confirm"} /></span>
          <span className="opacity-40">•</span>
          <span><I18nText text={"ESC: Cancel"} /></span>
        </div>
      </DialogContent>
    </Dialog>
  );
}

export default StatusUpdateModal;
