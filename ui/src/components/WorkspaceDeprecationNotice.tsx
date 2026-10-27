// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Admin notice listing the workspace settings that v3.0 moves into projects.
 * It stays a small icon until opened, and a dismissal hides it until another
 * setting comes into use.
 *
 * @module components
 */
import { WorkspaceDeprecationItemKind, type components } from '@/api/v1/schema';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog';
import { useIsAdmin } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { useI18n } from '@/i18n/I18nProvider';
import { writeLocalStorage } from '@/lib/local-storage-migration';
import { AlertTriangle } from 'lucide-react';
import * as React from 'react';

type Item = components['schemas']['WorkspaceDeprecationItem'];

const feedbackIssueURL = 'https://github.com/dagucloud/dagu/issues/3288';

const kindLabels: Record<WorkspaceDeprecationItemKind, string> = {
  [WorkspaceDeprecationItemKind.baseConfig]: 'Base config',
  [WorkspaceDeprecationItemKind.secret]: 'Secret',
  [WorkspaceDeprecationItemKind.profileEntry]: 'Profile entry',
  [WorkspaceDeprecationItemKind.notificationRoute]: 'Notification route',
  [WorkspaceDeprecationItemKind.incidentPolicy]: 'Incident policy',
  [WorkspaceDeprecationItemKind.configKey]: 'Config key',
  [WorkspaceDeprecationItemKind.user]: 'User',
  [WorkspaceDeprecationItemKind.apiKey]: 'API key',
  [WorkspaceDeprecationItemKind.wikiDirectory]: 'Wiki directory',
};

function itemKey(item: Item): string {
  return `${item.kind}:${item.name}`;
}

function readDismissed(storageKey: string): string[] {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(storageKey) ?? '[]');
    return Array.isArray(value) ? value : [];
  } catch {
    return [];
  }
}

// Groups names by kind, keeping the order the server returns.
function groupByKind(
  items: Item[]
): [WorkspaceDeprecationItemKind, string[]][] {
  const groups = new Map<WorkspaceDeprecationItemKind, string[]>();
  for (const item of items) {
    groups.set(item.kind, [...(groups.get(item.kind) ?? []), item.name]);
  }
  return [...groups];
}

export function WorkspaceDeprecationNotice() {
  const { ts } = useI18n();
  const isAdmin = useIsAdmin();
  const remoteNode = useRemoteNode();
  const { data } = useQuery(
    '/workspaces/deprecation',
    isAdmin ? { params: { query: { remoteNode } } } : null
  );
  const storageKey = `workspace-deprecation-dismissed-${remoteNode}`;
  const [dismissed, setDismissed] = React.useState<Record<string, string[]>>(
    {}
  );

  const items = data?.items ?? [];
  const dismissedKeys = dismissed[storageKey] ?? readDismissed(storageKey);
  if (
    !isAdmin ||
    items.every((item) => dismissedKeys.includes(itemKey(item)))
  ) {
    return null;
  }

  const dismiss = () => {
    const keys = items.map(itemKey);
    writeLocalStorage(storageKey, JSON.stringify(keys));
    setDismissed((prev) => ({ ...prev, [storageKey]: keys }));
  };

  const title = ts('Workspaces become projects in v3.0');
  return (
    <Dialog>
      <DialogTrigger
        aria-label={title}
        title={title}
        className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-amber-600 hover:bg-sidebar-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-sidebar-ring dark:text-amber-400"
      >
        <AlertTriangle className="h-4 w-4" />
      </DialogTrigger>
      <DialogContent className="max-h-[80vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {ts(
              'v3.0 moves these settings into projects with a migration command. No action is needed now.'
            )}
          </DialogDescription>
        </DialogHeader>
        <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
          {groupByKind(items).map(([kind, names]) => (
            <React.Fragment key={kind}>
              <dt className="font-medium">{ts(kindLabels[kind])}</dt>
              <dd className="break-words font-mono text-xs leading-5">
                {names.join(', ')}
              </dd>
            </React.Fragment>
          ))}
        </dl>
        <div className="flex items-center justify-between gap-3">
          <a
            href={feedbackIssueURL}
            target="_blank"
            rel="noopener noreferrer"
            className="text-sm text-muted-foreground underline hover:no-underline"
          >
            {ts('Have feedback? Comment on the GitHub issue.')}
          </a>
          <Button variant="outline" size="sm" onClick={dismiss}>
            {ts('Dismiss')}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
