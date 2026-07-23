// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import * as React from 'react';
import { AlertTriangle } from 'lucide-react';

import { Alert, AlertDescription } from '@/components/ui/alert';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Textarea } from '@/components/ui/textarea';
import {
  MAX_CONTROLLER_PROMPT_BYTES,
  utf8ByteLength,
  validateControllerPrompt,
} from '../constraints';

export function ControllerPromptDialog({
  open,
  title,
  description,
  submitLabel,
  pending = false,
  onOpenChange,
  onSubmit,
}: {
  open: boolean;
  title: string;
  description: string;
  submitLabel: string;
  pending?: boolean;
  onOpenChange: (open: boolean) => void;
  onSubmit: (prompt: string) => Promise<void> | void;
}) {
  const [prompt, setPrompt] = React.useState('');
  const promptBytes = utf8ByteLength(prompt);
  const promptError = validateControllerPrompt(prompt);

  React.useEffect(() => {
    if (!open) setPrompt('');
  }, [open]);

  const submit = async () => {
    if (promptError) return;
    await onSubmit(prompt);
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen || !pending) onOpenChange(nextOpen);
      }}
    >
      <DialogContent hideCloseButton={pending}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <Alert variant="warning">
            <AlertTriangle className="h-4 w-4" />
            <AlertDescription>
              This prompt is stored in Controller context and may be sent to an
              external LLM. Do not include secrets.
            </AlertDescription>
          </Alert>
          <Textarea
            autoFocus
            value={prompt}
            maxLength={MAX_CONTROLLER_PROMPT_BYTES}
            placeholder="Describe the outcome the Controller should achieve…"
            onChange={(event) => setPrompt(event.target.value)}
            className="min-h-32"
          />
          <div className="flex justify-between text-xs text-muted-foreground">
            <span className="text-destructive">
              {prompt ? promptError : null}
            </span>
            <span>
              {promptBytes} / {MAX_CONTROLLER_PROMPT_BYTES} bytes
            </span>
          </div>
        </div>
        <DialogFooter>
          <Button
            variant="ghost"
            disabled={pending}
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button
            variant="primary"
            disabled={pending || promptError !== null}
            onClick={() => void submit()}
          >
            {pending ? 'Submitting…' : submitLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
