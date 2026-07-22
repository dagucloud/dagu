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
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!open) {
      setPrompt('');
      setError(null);
    }
  }, [open]);

  const submit = async () => {
    if (!prompt.trim()) {
      setError('Enter a prompt.');
      return;
    }
    if (new TextEncoder().encode(prompt).length > 16_384) {
      setError('The prompt must be 16 KiB or less.');
      return;
    }
    setError(null);
    await onSubmit(prompt);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
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
            maxLength={16_384}
            placeholder="Describe the outcome the Controller should achieve…"
            onChange={(event) => setPrompt(event.target.value)}
            className="min-h-32"
          />
          <div className="flex justify-between text-xs text-muted-foreground">
            <span className="text-destructive">{error}</span>
            <span>
              {new TextEncoder().encode(prompt).length} / 16,384 bytes
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
            disabled={pending || !prompt.trim()}
            onClick={() => void submit()}
          >
            {pending ? 'Submitting…' : submitLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
