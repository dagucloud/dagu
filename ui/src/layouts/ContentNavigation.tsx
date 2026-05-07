// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { Home } from 'lucide-react';
import React from 'react';
import { Link } from 'react-router-dom';

type BreadcrumbItemData = {
  label: string;
  to?: string;
};

const STATIC_ROUTE_LABELS: Record<string, string> = {
  '/': 'Overview',
  '/home': 'Home',
  '/dashboard': 'Timeline',
  '/cockpit': 'Cockpit',
  '/api-docs': 'API Reference',
  '/integrations': 'Integrations',
  '/dags': 'DAGs',
  '/search': 'Search',
  '/base-config': 'Base Config',
  '/docs': 'Runbooks',
  '/queues': 'Queues',
  '/dag-runs': 'DAG Runs',
  '/system-status': 'System Status',
  '/users': 'Users',
  '/administration': 'Administration',
  '/remote-nodes': 'Remote Nodes',
  '/api-keys': 'API Keys',
  '/webhooks': 'Webhooks',
  '/terminal': 'Terminal',
  '/event-logs': 'Events',
  '/audit-logs': 'Audit Logs',
  '/license': 'License',
  '/git-sync': 'Git Sync',
  '/agent': 'Agent',
  '/agent-settings': 'Models',
  '/agent-tools': 'Tools',
  '/agent-memory': 'Memory',
  '/agent-souls': 'Souls',
  '/agent-souls/new': 'New Soul',
};

function decodePathSegment(segment: string): string {
  try {
    return decodeURIComponent(segment);
  } catch {
    return segment;
  }
}

function humanizePathSegment(segment: string): string {
  return decodePathSegment(segment)
    .replace(/[-_]+/g, ' ')
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

export function getBreadcrumbItems(pathname: string): BreadcrumbItemData[] {
  const normalized = pathname.replace(/\/+$/, '') || '/';
  const segments = normalized.split('/').filter(Boolean);
  const items: BreadcrumbItemData[] = [{ label: 'Home', to: '/home' }];

  if (normalized === '/home') {
    return [{ label: 'Home' }];
  }

  if (normalized === '/') {
    return [...items, { label: 'Overview', to: '/' }];
  }

  if (segments[0] === 'dags') {
    items.push({ label: 'Workflows' }, { label: 'DAGs', to: '/dags' });
    if (segments[1]) {
      items.push({
        label: decodePathSegment(segments[1]),
        to: `/dags/${segments[1]}`,
      });
    }
    if (segments[2]) {
      items.push({ label: humanizePathSegment(segments[2]) });
    }
    return items;
  }

  if (segments[0] === 'dag-runs') {
    items.push({ label: 'Executions' }, { label: 'DAG Runs', to: '/dag-runs' });
    if (segments[1]) {
      items.push({ label: decodePathSegment(segments[1]) });
    }
    if (segments[2]) {
      items.push({ label: decodePathSegment(segments[2]) });
    }
    return items;
  }

  if (segments[0] === 'queues') {
    items.push({ label: 'Executions' }, { label: 'Queues', to: '/queues' });
    if (segments[1]) {
      items.push({ label: decodePathSegment(segments[1]) });
    }
    return items;
  }

  if (segments[0] === 'docs') {
    items.push({ label: 'Workflows' }, { label: 'Runbooks', to: '/docs' });
    for (const segment of segments.slice(1)) {
      items.push({ label: decodePathSegment(segment) });
    }
    return items;
  }

  if (segments[0]?.startsWith('agent')) {
    items.push(
      { label: 'Administration', to: '/administration' },
      { label: 'Agent', to: normalized === '/agent' ? undefined : '/agent' }
    );

    if (normalized === '/agent') {
      return items;
    }

    const sectionPath = `/${segments[0]}`;
    items.push({
      label:
        STATIC_ROUTE_LABELS[sectionPath] ??
        humanizePathSegment(segments[0] ?? ''),
      to: normalized === sectionPath ? undefined : sectionPath,
    });

    for (let index = 1; index < segments.length; index += 1) {
      const path = `${sectionPath}/${segments.slice(1, index + 1).join('/')}`;
      items.push({
        label:
          STATIC_ROUTE_LABELS[path] ?? decodePathSegment(segments[index] ?? ''),
        to: index === segments.length - 1 ? undefined : path,
      });
    }

    return items;
  }

  if (
    [
      'users',
      'administration',
      'remote-nodes',
      'api-keys',
      'terminal',
      'license',
      'git-sync',
    ].includes(segments[0] ?? '')
  ) {
    if (normalized !== '/administration') {
      items.push({ label: 'Administration', to: '/administration' });
    }
    items.push({ label: STATIC_ROUTE_LABELS[normalized] ?? humanizePathSegment(segments[0] ?? '') });
    return items;
  }

  if (['system-status', 'event-logs', 'audit-logs'].includes(segments[0] ?? '')) {
    items.push({ label: 'Monitor' });
    items.push({ label: STATIC_ROUTE_LABELS[normalized] ?? humanizePathSegment(segments[0] ?? '') });
    return items;
  }

  if (['integrations', 'webhooks', 'api-docs'].includes(segments[0] ?? '')) {
    if (normalized !== '/integrations') {
      items.push({ label: 'Integrations', to: '/integrations' });
    }
    items.push({ label: STATIC_ROUTE_LABELS[normalized] ?? humanizePathSegment(segments[0] ?? '') });
    return items;
  }

  const exactLabel = STATIC_ROUTE_LABELS[normalized];
  if (exactLabel) {
    items.push({ label: exactLabel, to: normalized });
    return items;
  }

  for (let index = 0; index < segments.length; index += 1) {
    const path = `/${segments.slice(0, index + 1).join('/')}`;
    items.push({
      label: STATIC_ROUTE_LABELS[path] ?? humanizePathSegment(segments[index] ?? ''),
      to: index === segments.length - 1 ? undefined : path,
    });
  }

  return items;
}

export function ContentNavigation({
  pathname,
}: {
  pathname: string;
}): React.ReactElement {
  const items = getBreadcrumbItems(pathname);

  return (
    <div className="hidden min-h-12 items-center gap-3 border-b border-border bg-background/95 px-4 backdrop-blur md:flex">
      <Button asChild variant="ghost" size="icon-sm" className="shrink-0">
        <Link to="/home" aria-label="Content home">
          <Home className="h-4 w-4" />
        </Link>
      </Button>
      <Breadcrumb className="min-w-0">
        <BreadcrumbList className="min-w-0 flex-nowrap overflow-hidden">
          {items.map((item, index) => {
            const isLast = index === items.length - 1;
            return (
              <React.Fragment key={`${item.label}-${index}`}>
                {index > 0 && <BreadcrumbSeparator className="shrink-0" />}
                <BreadcrumbItem className="min-w-0">
                  {isLast || !item.to ? (
                    <BreadcrumbPage
                      className={cn(
                        'block truncate',
                        item.label.length > 28 && 'max-w-[28ch]'
                      )}
                    >
                      {item.label}
                    </BreadcrumbPage>
                  ) : (
                    <Link
                      to={item.to}
                      className="block truncate text-muted-foreground hover:text-foreground"
                    >
                      {item.label}
                    </Link>
                  )}
                </BreadcrumbItem>
              </React.Fragment>
            );
          })}
        </BreadcrumbList>
      </Breadcrumb>
    </div>
  );
}
