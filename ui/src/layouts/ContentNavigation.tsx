// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbSeparator,
} from '@/components/ui/breadcrumb';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { useI18n } from '@/i18n/I18nProvider';
import { translate, type TranslationKey } from '@/i18n/messages';
import { Home } from 'lucide-react';
import React from 'react';
import { Link } from 'react-router-dom';

type BreadcrumbItemData = {
  label: string;
  to: string;
};

const STATIC_ROUTE_LABELS: Record<string, TranslationKey> = {
  '/': 'navigation.overview',
  '/home': 'navigation.home',
  '/dashboard': 'navigation.timeline',
  '/cockpit': 'navigation.cockpit',
  '/api-docs': 'navigation.apiReference',
  '/integrations': 'navigation.integrations',
  '/notifications': 'navigation.notifications',
  '/notification-rules': 'navigation.notificationRules',
  '/notification-channels': 'navigation.notificationChannels',
  '/incidents': 'navigation.incidents',
  '/incident-providers': 'navigation.incidentConnections',
  '/incident-policies': 'navigation.incidentRouting',
  '/dags': 'navigation.dags',
  '/search': 'navigation.search',
  '/base-config': 'navigation.baseConfig',
  '/wiki': 'navigation.wiki',
  '/queues': 'navigation.queues',
  '/dag-runs': 'navigation.dagRuns',
  '/system-status': 'navigation.systemStatus',
  '/users': 'navigation.users',
  '/administration': 'navigation.administration',
  '/remote-nodes': 'navigation.remoteNodes',
  '/api-keys': 'navigation.apiKeys',
  '/webhooks': 'navigation.webhooks',
  '/terminal': 'navigation.terminal',
  '/event-logs': 'navigation.events',
  '/audit-logs': 'navigation.auditLogs',
  '/license': 'navigation.license',
  '/git-sync': 'navigation.gitSync',
};

type Translate = (key: TranslationKey) => string;

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

export function getBreadcrumbItems(
  pathname: string,
  t: Translate = (key) => translate('en', key)
): BreadcrumbItemData[] {
  const normalized = pathname.replace(/\/+$/, '') || '/';
  const segments = normalized.split('/').filter(Boolean);
  const items: BreadcrumbItemData[] = [
    { label: t('navigation.home'), to: '/home' },
  ];

  if (normalized === '/home') {
    return items;
  }

  if (normalized === '/') {
    return [...items, { label: t('navigation.overview'), to: '/' }];
  }

  if (segments[0] === 'dags') {
    items.push(
      { label: t('navigation.workflows'), to: '/dags' },
      { label: t('navigation.dags'), to: '/dags' }
    );
    if (segments[1]) {
      items.push({
        label: decodePathSegment(segments[1]),
        to: `/dags/${segments[1]}`,
      });
    }
    if (segments[2]) {
      items.push({
        label: humanizePathSegment(segments[2]),
        to: `/dags/${segments[1]}/${segments[2]}`,
      });
    }
    return items;
  }

  if (segments[0] === 'dag-runs') {
    items.push(
      { label: t('navigation.executions'), to: '/dag-runs' },
      { label: t('navigation.dagRuns'), to: '/dag-runs' }
    );
    if (segments[1]) {
      const dagName = decodePathSegment(segments[1]);
      items.push({
        label: dagName,
        to: `/dag-runs?name=${encodeURIComponent(dagName)}`,
      });
    }
    if (segments[2]) {
      items.push({
        label: decodePathSegment(segments[2]),
        to: `/dag-runs/${segments[1]}/${segments[2]}`,
      });
    }
    return items;
  }

  if (segments[0] === 'queues') {
    items.push(
      { label: t('navigation.executions'), to: '/dag-runs' },
      { label: t('navigation.queues'), to: '/queues' }
    );
    if (segments[1]) {
      items.push({
        label: decodePathSegment(segments[1]),
        to: `/queues/${segments[1]}`,
      });
    }
    return items;
  }

  if (segments[0] === 'wiki' || segments[0] === 'docs') {
    items.push(
      { label: t('navigation.workflows'), to: '/dags' },
      { label: t('navigation.wiki'), to: '/wiki' }
    );
    let wikiPath = '/wiki';
    for (const segment of segments.slice(1)) {
      wikiPath += `/${segment}`;
      items.push({ label: decodePathSegment(segment), to: wikiPath });
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
      items.push({
        label: t('navigation.administration'),
        to: '/administration',
      });
    }
    items.push({
      label:
        (STATIC_ROUTE_LABELS[normalized]
          ? t(STATIC_ROUTE_LABELS[normalized])
          : undefined) ?? humanizePathSegment(segments[0] ?? ''),
      to: normalized,
    });
    return items;
  }

  if (
    ['system-status', 'event-logs', 'audit-logs'].includes(segments[0] ?? '')
  ) {
    items.push({ label: t('navigation.monitor'), to: '/system-status' });
    items.push({
      label:
        (STATIC_ROUTE_LABELS[normalized]
          ? t(STATIC_ROUTE_LABELS[normalized])
          : undefined) ?? humanizePathSegment(segments[0] ?? ''),
      to: normalized,
    });
    return items;
  }

  if (['integrations', 'webhooks', 'api-docs'].includes(segments[0] ?? '')) {
    if (normalized !== '/integrations') {
      items.push({
        label: t('navigation.integrations'),
        to: '/integrations',
      });
    }
    items.push({
      label:
        (STATIC_ROUTE_LABELS[normalized]
          ? t(STATIC_ROUTE_LABELS[normalized])
          : undefined) ?? humanizePathSegment(segments[0] ?? ''),
      to: normalized,
    });
    return items;
  }

  if (
    ['notifications', 'notification-rules', 'notification-channels'].includes(
      segments[0] ?? ''
    )
  ) {
    if (normalized !== '/notifications') {
      items.push({
        label: t('navigation.notifications'),
        to: '/notifications',
      });
    }
    items.push({
      label:
        (STATIC_ROUTE_LABELS[normalized]
          ? t(STATIC_ROUTE_LABELS[normalized])
          : undefined) ?? humanizePathSegment(segments[0] ?? ''),
      to: normalized,
    });
    return items;
  }

  if (
    ['incidents', 'incident-providers', 'incident-policies'].includes(
      segments[0] ?? ''
    )
  ) {
    if (normalized !== '/incidents') {
      items.push({ label: t('navigation.incidents'), to: '/incidents' });
    }
    items.push({
      label:
        (STATIC_ROUTE_LABELS[normalized]
          ? t(STATIC_ROUTE_LABELS[normalized])
          : undefined) ?? humanizePathSegment(segments[0] ?? ''),
      to: normalized,
    });
    return items;
  }

  if (segments[0] === 'views' && segments[1]) {
    items.push(
      { label: t('navigation.cockpit'), to: '/cockpit' },
      {
        label: decodePathSegment(segments[1]),
        to: `/views/${segments[1]}`,
      }
    );
    return items;
  }

  const exactLabel = STATIC_ROUTE_LABELS[normalized];
  if (exactLabel) {
    items.push({ label: t(exactLabel), to: normalized });
    return items;
  }

  for (let index = 0; index < segments.length; index += 1) {
    const path = `/${segments.slice(0, index + 1).join('/')}`;
    items.push({
      label:
        (STATIC_ROUTE_LABELS[path]
          ? t(STATIC_ROUTE_LABELS[path])
          : undefined) ?? humanizePathSegment(segments[index] ?? ''),
      to: path,
    });
  }

  return items;
}

export function ContentNavigation({
  pathname,
}: {
  pathname: string;
}): React.ReactElement {
  const { t } = useI18n();
  const items = getBreadcrumbItems(pathname, t);

  return (
    <div className="hidden min-h-12 items-center gap-3 border-b border-border bg-background/95 px-4 backdrop-blur md:flex">
      <Button asChild variant="ghost" size="icon-sm" className="shrink-0">
        <Link to="/home" aria-label={t('navigation.contentHome')}>
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
                  <Link
                    to={item.to}
                    aria-current={isLast ? 'page' : undefined}
                    className={cn(
                      'block truncate hover:text-foreground',
                      isLast
                        ? 'font-medium text-foreground'
                        : 'text-muted-foreground',
                      item.label.length > 28 && 'max-w-[28ch]'
                    )}
                  >
                    {item.label}
                  </Link>
                </BreadcrumbItem>
              </React.Fragment>
            );
          })}
        </BreadcrumbList>
      </Breadcrumb>
    </div>
  );
}
