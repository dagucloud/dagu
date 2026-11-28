// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { InlineLoadError } from '@/components/PageStates';
import { Badge } from '@/components/ui/badge';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import dayjs from '@/lib/dayjs';
import React from 'react';
import { LOCAL_NODE } from './shared';

/**
 * Lists the tools of each MCP service of a connection, read only, from its
 * tool catalog. The server lists a service again only when its catalog is
 * older than 10 minutes, once per host however many pages ask.
 */
export function ConnectionTools({
  name,
  workspace,
}: {
  name: string;
  workspace: string;
}): React.ReactElement {
  const { ts } = useI18n();
  const { data, error, isLoading, mutate } = useQuery(
    '/connections/{name}/tools',
    {
      params: {
        path: { name },
        query: { workspace, remoteNode: LOCAL_NODE },
      },
    }
  );

  if (isLoading) {
    return (
      <p className="text-sm text-muted-foreground">{ts('Loading tools…')}</p>
    );
  }
  if (error && !data) {
    return <InlineLoadError error={error} onRetry={() => void mutate()} />;
  }
  const servers = data?.servers ?? [];
  if (servers.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        {ts('This connection has no MCP services.')}
      </p>
    );
  }
  return (
    <div className="space-y-3">
      {servers.map((server) => (
        <section key={server.server} aria-label={server.server}>
          <div className="mb-1 flex items-baseline gap-2">
            <h3 className="text-sm font-semibold">{server.server}</h3>
            {server.listedAt && (
              <span className="text-xs text-muted-foreground">
                {ts('Listed {time}', {
                  time: dayjs(server.listedAt).format('MMM D, HH:mm'),
                })}
              </span>
            )}
          </div>
          {server.error ? (
            <p className="whitespace-normal break-words rounded-md bg-destructive/10 p-2 text-sm text-destructive">
              {server.error}
            </p>
          ) : server.tools.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              {ts('The service lists no tools.')}
            </p>
          ) : (
            <ul className="divide-y divide-border rounded-md border border-border">
              {server.tools.map((tool) => (
                <li key={tool.name} className="px-2 py-1.5">
                  <div className="flex items-center gap-2">
                    <code className="text-xs font-medium">{tool.name}</code>
                    {tool.annotations?.readOnlyHint === true && (
                      <Badge size="sm">{ts('Read only')}</Badge>
                    )}
                  </div>
                  {tool.description && (
                    <p className="mt-0.5 line-clamp-2 whitespace-normal break-words text-xs text-muted-foreground">
                      {tool.description}
                    </p>
                  )}
                </li>
              ))}
            </ul>
          )}
        </section>
      ))}
    </div>
  );
}
