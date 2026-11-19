// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useMemo, useState, type ReactElement } from 'react';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { RelativeTime } from '@/components/ui/relative-time';
import { AppBarContext } from '@/contexts/AppBarContext';
import { scopeQuery, type AIEndpoint } from '@/features/ai-endpoints/endpoint';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';

/**
 * The model names of one endpoint, with a search box and the count. The
 * names are the last good read, as the server keeps them.
 */
export function ModelsDialog({
  endpoint,
  onClose,
}: {
  endpoint: AIEndpoint;
  onClose(): void;
}): ReactElement {
  const { ts } = useI18n();
  const appBar = useContext(AppBarContext);
  const remoteNode = appBar.selectedRemoteNode || 'local';
  const [search, setSearch] = useState('');
  const { data, error } = useQuery('/ai/endpoints/{name}/models', {
    params: {
      path: { name: endpoint.name },
      query: { remoteNode, workspace: scopeQuery(endpoint.scope) },
    },
  });
  const models = data?.models;
  const shown = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return (models ?? []).filter(
      (name) => needle === '' || name.toLowerCase().includes(needle)
    );
  }, [models, search]);

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {ts('The models of {name}', { name: endpoint.name })}
          </DialogTitle>
          <DialogDescription>
            {data?.refreshedAt ? (
              <>
                {ts('Last good read')}{' '}
                <RelativeTime timestamp={data.refreshedAt} />
              </>
            ) : data ? (
              ts('Not read yet')
            ) : null}
          </DialogDescription>
        </DialogHeader>
        {error && !data ? (
          <p role="alert" className="text-sm text-destructive">
            {ts("Couldn't load the models.")}
          </p>
        ) : !data ? (
          <p className="text-sm text-muted-foreground">{ts('Loading…')}</p>
        ) : (
          <div className="space-y-2">
            <Input
              value={search}
              placeholder={ts('Search')}
              aria-label={ts('Search the models')}
              autoComplete="off"
              className="h-8 text-xs"
              onChange={(event) => setSearch(event.target.value)}
            />
            <p role="status" className="text-xs text-muted-foreground">
              {ts('{shown} of {count} models', {
                shown: shown.length,
                count: data.count,
              })}
            </p>
            {shown.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                {ts('No model matches.')}
              </p>
            ) : (
              <ul
                aria-label={ts('Models')}
                className="max-h-72 overflow-y-auto rounded-md border border-border"
              >
                {shown.map((name) => (
                  <li
                    key={name}
                    className="border-b border-border px-2 py-1 font-mono text-xs last:border-b-0"
                  >
                    {name}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
