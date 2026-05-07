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
    setTitle('Home');
  }, [setTitle]);

  const sections: HomeSection[] = [
    {
      title: 'Overview',
      links: [
        {
          to: '/',
          label: 'Overview',
          description: 'Review workflow status and recent activity.',
        },
        {
          to: '/dashboard',
          label: 'Timeline',
          description: 'Inspect scheduled and historical execution trends.',
        },
        {
          to: '/cockpit',
          label: 'Cockpit',
          description: 'Work through active and waiting runs.',
        },
      ],
    },
    {
      title: 'Workflows',
      links: [
        {
          to: '/dags',
          label: 'DAGs',
          description: 'Browse and open workflow definitions.',
        },
        {
          to: '/search',
          label: 'Search',
          description: 'Find workflows and documentation.',
        },
        {
          to: '/docs',
          label: 'Runbooks',
          description: 'Read and edit operational docs.',
        },
        ...(canWrite
          ? [
              {
                to: '/base-config',
                label: 'Base Config',
                description: 'Edit shared workflow defaults.',
              },
            ]
          : []),
        ...(canWrite && config.gitSyncEnabled
          ? [
              {
                to: '/git-sync',
                label: 'Git Sync',
                description: 'Sync workflow files with Git.',
              },
            ]
          : []),
      ],
    },
    {
      title: 'Executions',
      links: [
        {
          to: '/dag-runs',
          label: 'DAG Runs',
          description: 'Review run history and execution state.',
        },
        {
          to: '/queues',
          label: 'Queues',
          description: 'Inspect queued workflow work.',
        },
      ],
    },
    {
      title: 'Monitor',
      links: [
        ...(canAccessSystemStatus
          ? [
              {
                to: '/system-status',
                label: 'System Status',
                description: 'Check services, paths, and runtime health.',
              },
            ]
          : []),
        ...(canViewEventLogs
          ? [
              {
                to: '/event-logs',
                label: 'Events',
                description: 'Inspect workflow and system events.',
              },
            ]
          : []),
        ...(canViewAuditLogs
          ? [
              {
                to: '/audit-logs',
                label: hasAudit ? 'Audit Logs' : 'Audit Logs (Pro)',
                description: 'Review administrative activity.',
              },
            ]
          : []),
      ],
    },
    {
      title: 'Integrations',
      links: [
        {
          to: '/integrations',
          label: 'Integrations',
          description: 'Open integration entry points.',
        },
        ...(canManageWebhooks
          ? [
              {
                to: '/webhooks',
                label: 'Webhooks',
                description: 'Manage webhook endpoints.',
              },
            ]
          : []),
        {
          to: '/api-docs',
          label: 'API Reference',
          description: 'Browse HTTP API documentation.',
        },
      ],
    },
    {
      title: 'Administration',
      links: isAdmin
        ? [
            {
              to: '/administration',
              label: 'Administration',
              description: 'Open access, infrastructure, and agent settings.',
            },
            {
              to: '/remote-nodes',
              label: 'Remote Nodes',
              description: 'Configure distributed execution targets.',
            },
            ...(config.terminalEnabled
              ? [
                  {
                    to: '/terminal',
                    label: 'Terminal',
                    description: 'Open a server-side shell.',
                  },
                ]
              : []),
            {
              to: '/license',
              label: 'License',
              description: 'Review plan and entitlement status.',
            },
            ...(config.agentEnabled
              ? [
                  {
                    to: '/agent',
                    label: 'Agent',
                    description: 'Configure models, tools, memory, and souls.',
                  },
                ]
              : []),
          ]
        : [],
    },
  ].filter((section) => section.links.length > 0);

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <Title>Home</Title>

      {sections.map((section) => (
        <SectionLinks key={section.title} section={section} />
      ))}
    </div>
  );
}
