// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RotateCw } from 'lucide-react';
import type { ReactElement } from 'react';
import { Button } from '@/components/ui/button';
import { RelativeTime } from '@/components/ui/relative-time';
import type { AIEndpoint } from '@/features/ai-endpoints/endpoint';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * The models list of an endpoint in the table of endpoints: the number of
 * models, the date of the last good read, the last error and the Reload
 * button. A failed read keeps the last good list, so the count and the date
 * stay beside the error. The error comes from the server and never has the key.
 */
export function ModelsCell({
  endpoint,
  busy,
  reloadError,
  onShow,
  onReload,
}: {
  endpoint: AIEndpoint;
  /** True while a reload of this endpoint runs. */
  busy: boolean;
  /** The text of a reload that the request itself refused, or null. */
  reloadError: string | null;
  onShow(): void;
  onReload(): void;
}): ReactElement {
  const { ts } = useI18n();
  const models = endpoint.models;
  if (!models?.supported) {
    return (
      <span data-testid="models-none" className="text-muted-foreground">
        {ts('No model list')}
      </span>
    );
  }
  const subscription = endpoint.auth === 'anthropic-oauth';
  return (
    <div className="flex items-start justify-between gap-2">
      <div className="min-w-0 space-y-0.5">
        <button
          type="button"
          disabled={models.count === 0}
          onClick={onShow}
          className="rounded text-left underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:no-underline"
        >
          {models.count === 1
            ? ts('1 model')
            : ts('{count} models', { count: models.count })}
        </button>
        <div className="text-muted-foreground">
          {busy ? (
            ts('Reading the models…')
          ) : models.refreshedAt ? (
            <>
              {ts('Last good read')}{' '}
              <RelativeTime timestamp={models.refreshedAt} />
            </>
          ) : models.error ? (
            ts('No good read yet')
          ) : (
            ts('Not read yet')
          )}
        </div>
        {models.error && (
          <p
            data-testid="models-error"
            className="whitespace-normal break-words text-destructive"
          >
            {ts('The last read failed: {error}', { error: models.error })}
            {subscription && (
              <>
                {' '}
                {ts(
                  'A token of a subscription can lack the right to list models. Type the model name in the step.'
                )}
              </>
            )}
          </p>
        )}
        {reloadError && (
          <p role="alert" className="whitespace-normal text-destructive">
            {reloadError}
          </p>
        )}
      </div>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        className="h-6 shrink-0 px-1.5 text-xs"
        disabled={busy}
        aria-label={ts('Reload the models of {name}', { name: endpoint.name })}
        onClick={onReload}
      >
        <RotateCw
          className={busy ? 'size-3 animate-spin' : 'size-3'}
          aria-hidden="true"
        />
        {ts('Reload')}
      </Button>
    </div>
  );
}
