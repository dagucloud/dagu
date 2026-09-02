// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import Title from '@/components/ui/title';
import {
  useAuth,
  useCanAccessSystemStatus,
  useCanManageWebhooks,
  useCanViewAuditLogs,
  useCanViewEventLogs,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useHasFeature } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import { roleAtLeast } from '@/lib/workspaceAccess';
import { UserRole } from '@/api/v1/schema';
import React from 'react';
import { Link } from 'react-router-dom';

type HomeLink = {
  to: string;
  label: string;
  description: string;
};

type HomeSection = {
  title: string;
  links: HomeLink[];
};

function SectionLinks({
  section,
}: {
  section: HomeSection;
}): React.ReactElement {
  return (
    <section className="space-y-2">
      <h3 className="text-xs font-semibold uppercase text-muted-foreground">
        {section.title}
      </h3>
      <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
        {section.links.map((link) => (
          <Link
            key={link.to}
            to={link.to}
            className="rounded-md border border-border bg-card px-4 py-3 transition-colors hover:border-border-strong hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <span className="block text-sm font-medium text-foreground">
              {link.label}
            </span>
            <span className="mt-1 block text-xs text-muted-foreground">
              {link.description}
            </span>
          </Link>
        ))}
      </div>
    </section>
  );
}

export default function HomePage(): React.ReactElement {
  const { setTitle } = React.useContext(AppBarContext);
  const { t } = useI18n();
  const config = useConfig();
  const { user } = useAuth();
  const isAdmin = useIsAdmin();
  const hasAudit = useHasFeature('audit');
  const canWrite =
    config.authMode !== 'builtin'
      ? config.permissions.writeDags
      : roleAtLeast(user?.role ?? null, UserRole.developer);
  const canAccessSystemStatus = useCanAccessSystemStatus();
  const canManageWebhooks = useCanManageWebhooks();
  const canViewEventLogs = useCanViewEventLogs();
  const canViewAuditLogs = useCanViewAuditLogs();

  React.useEffect(() => {
    setTitle(t('navigation.home'));
  }, [setTitle, t]);

  const sections: HomeSection[] = [
    {
      title: t('navigation.overview'),
      links: [
        {
          to: '/',
          label: t('navigation.overview'),
          description: t('home.overviewDescription'),
        },
        {
          to: '/dashboard',
          label: t('navigation.timeline'),
          description: t('home.timelineDescription'),
        },
        {
          to: '/cockpit',
          label: t('navigation.cockpit'),
          description: t('home.cockpitDescription'),
        },
      ],
    },
    {
      title: t('navigation.workflows'),
      links: [
        {
          to: '/dags',
          label: t('navigation.dags'),
          description: t('home.dagsDescription'),
        },
        {
          to: '/search',
          label: t('navigation.search'),
          description: t('home.searchDescription'),
        },
        {
          to: '/wiki',
          label: t('navigation.wiki'),
          description: t('home.wikiDescription'),
        },
        ...(canWrite
          ? [
              {
                to: '/base-config',
                label: t('navigation.baseConfig'),
                description: t('home.baseConfigDescription'),
              },
            ]
          : []),
        ...(canWrite && config.gitSyncEnabled
          ? [
              {
                to: '/git-sync',
                label: t('navigation.gitSync'),
                description: t('home.gitSyncDescription'),
              },
            ]
          : []),
      ],
    },
    {
      title: t('navigation.executions'),
      links: [
        {
          to: '/dag-runs',
          label: t('navigation.dagRuns'),
          description: t('home.dagRunsDescription'),
        },
        {
          to: '/queues',
          label: t('navigation.queues'),
          description: t('home.queuesDescription'),
        },
      ],
    },
    {
      title: t('navigation.monitor'),
      links: [
        ...(canAccessSystemStatus
          ? [
              {
                to: '/system-status',
                label: t('navigation.systemStatus'),
                description: t('home.systemStatusDescription'),
              },
            ]
          : []),
        ...(canViewEventLogs
          ? [
              {
                to: '/event-logs',
                label: t('navigation.events'),
                description: t('home.eventsDescription'),
              },
            ]
          : []),
        ...(canViewAuditLogs
          ? [
              {
                to: '/audit-logs',
                label: hasAudit
                  ? t('navigation.auditLogs')
                  : t('home.auditLogsPro'),
                description: t('home.auditLogsDescription'),
              },
            ]
          : []),
      ],
    },
    {
      title: t('navigation.integrations'),
      links: [
        {
          to: '/integrations',
          label: t('navigation.integrations'),
          description: t('home.integrationsDescription'),
        },
        ...(canManageWebhooks
          ? [
              {
                to: '/webhooks',
                label: t('navigation.webhooks'),
                description: t('home.webhooksDescription'),
              },
            ]
          : []),
        {
          to: '/api-docs',
          label: t('navigation.apiReference'),
          description: t('home.apiReferenceDescription'),
        },
      ],
    },
    {
      title: t('navigation.administration'),
      links: isAdmin
        ? [
            {
              to: '/administration',
              label: t('navigation.administration'),
              description: t('home.administrationDescription'),
            },
            {
              to: '/remote-nodes',
              label: t('navigation.remoteNodes'),
              description: t('home.remoteNodesDescription'),
            },
            ...(config.terminalEnabled
              ? [
                  {
                    to: '/terminal',
                    label: t('navigation.terminal'),
                    description: t('home.terminalDescription'),
                  },
                ]
              : []),
            {
              to: '/license',
              label: t('navigation.license'),
              description: t('home.licenseDescription'),
            },
          ]
        : [],
    },
  ].filter((section) => section.links.length > 0);

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <Title>{t('navigation.home')}</Title>

      {sections.map((section) => (
        <SectionLinks key={section.title} section={section} />
      ))}
    </div>
  );
}
