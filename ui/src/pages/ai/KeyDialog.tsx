// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useId, useRef, useState, type ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  describeApiError,
  keyProblem,
  scopeQuery,
  type AIEndpoint,
} from '@/features/ai-endpoints/endpoint';
import { problemText } from '@/features/ai-endpoints/problemText';
import {
  ChatField,
  errorIssue,
} from '@/features/visual-editor/inspector/sections/chat/fields';
import { useClient } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * The dialog that sets a new key for an endpoint. The key lives in the field
 * only: React never holds it, and the field is cleared after the request.
 */
export function KeyDialog({
  endpoint,
  onSaved,
  onClose,
}: {
  endpoint: AIEndpoint;
  onSaved(): void;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const client = useClient();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const id = useId();
  const keyRef = useRef<HTMLInputElement>(null);
  const [issue, setIssue] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const key = keyRef.current?.value ?? '';
    const found = keyProblem(key, true);
    if (found) {
      setIssue(problemText(ts, found));
      return;
    }
    setBusy(true);
    setIssue(null);
    try {
      const result = await client.PUT('/ai/endpoints/{name}/key', {
        params: {
          path: { name: endpoint.name },
          query: { remoteNode, workspace: scopeQuery(endpoint.scope) },
        },
        body: { key },
      });
      if (result.error) {
        setIssue(
          describeApiError(
            ts,
            result.response.status,
            result.error,
            ts('The key was not saved.')
          )
        );
        return;
      }
      onSaved();
    } catch {
      setIssue(ts('The server did not respond.'));
    } finally {
      if (keyRef.current) keyRef.current.value = '';
      setBusy(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>
            {ts('Rotate the key of {name}', { name: endpoint.name })}
          </DialogTitle>
          <DialogDescription>
            {ts(
              'The new key replaces the old key. The server never shows a key after you save.'
            )}
          </DialogDescription>
        </DialogHeader>
        <form
          className="space-y-3"
          autoComplete="off"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <ChatField
            label={ts('New key')}
            htmlFor={`${id}-key`}
            issues={issue ? [errorIssue(issue)] : undefined}
          >
            <input
              ref={keyRef}
              id={`${id}-key`}
              type="password"
              name={`${id}-secret`}
              autoComplete="new-password"
              spellCheck={false}
              aria-invalid={issue ? true : undefined}
              className="h-7 w-full min-w-0 rounded-md border border-input bg-card px-2 font-mono text-xs shadow-sm focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/20 aria-invalid:border-destructive"
            />
          </ChatField>
          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={onClose}
            >
              {ts('Cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={busy}>
              {ts('Rotate key')}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
