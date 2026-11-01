// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Activity,
  AlertTriangle,
  Bell,
  BookOpen,
  ClipboardList,
  FileCode2,
  FileCog,
  Gauge,
  GitBranch,
  History,
  KeyRound,
  Layers,
  Network,
  Package,
  Radar,
  ScrollText,
  Server,
  SlidersHorizontal,
  SquareTerminal,
  Users,
  Webhook,
} from 'lucide-react';
import * as React from 'react';
import { UserRole } from '@/api/v1/schema';
import {
  useAuth,
  useCanAccessGitSync,
  useCanAccessSystemStatus,
  useCanManageProfiles,
  useCanManageWebhooks,
  useCanViewAuditLogs,
  useCanViewEventLogs,
  useIsAdmin,
} from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useI18n } from '@/i18n/I18nProvider';
import { roleAtLeast } from '@/lib/workspaceAccess';

/** A page the current user can open from the app navigation. */
export type NavigationLink = {
  to: string;
  text: string;
  icon: React.ReactNode;
  /** Paths that also mark this link active; defaults to `to` and its children. */
  activePaths?: string[];
};

export type NavigationSection<T = NavigationLink> = {
  key: string;
  label: string;
  items: T[];
};

/**
 * Returns the app's navigation destinations filtered by the current user's
 * permissions: ungrouped primary pages, then labelled sections. Sections with
 * no visible pages are omitted.
 */
export function useNavigationSections(): {
  mainItems: NavigationLink[];
  sections: NavigationSection[];
} {
  const config = useConfig();
  const isAdmin = useIsAdmin();
  const { user } = useAuth();
  const canWrite =
    config.authMode !== 'builtin'
      ? config.permissions.writeDags
      : roleAtLeast(user?.role ?? null, UserRole.developer);
  const canManageNotifications =
    config.authMode !== 'builtin'
      ? config.permissions.writeDags
      : roleAtLeast(user?.role ?? null, UserRole.developer);
  const canManageIncidents = canManageNotifications;
  const canAccessSystemStatus = useCanAccessSystemStatus();
  const canAccessGitSync = useCanAccessGitSync();
  const canManageWebhooks = useCanManageWebhooks();
  const canManageProfiles = useCanManageProfiles();
  const canViewEventLogs = useCanViewEventLogs();
  const canViewAuditLogs = useCanViewAuditLogs();
  const { t } = useI18n();

  const mainItems: NavigationLink[] = [
    {
      to: '/',
      text: t('navigation.overview'),
      icon: <Gauge size={16} />,
      activePaths: ['/', '/dashboard', '/cockpit'],
    },
    {
      to: '/dags',
      text: t('navigation.workflows'),
      icon: <Network size={16} />,
    },
    {
      to: '/dag-runs',
      text: t('navigation.executions'),
      icon: <History size={16} />,
    },
    {
      to: '/queues',
      text: t('navigation.queues'),
      icon: <Layers size={16} />,
    },
    {
      to: '/artifacts',
      text: t('navigation.artifacts'),
      icon: <Package size={16} />,
    },
    { to: '/wiki', text: t('navigation.wiki'), icon: <BookOpen size={16} /> },
  ];

  const sections: (NavigationSection<NavigationLink | false> | false)[] = [
    {
      key: 'monitor',
      label: t('navigation.monitor'),
      items: [
        canAccessSystemStatus && {
          to: '/system-status',
          text: t('navigation.systemStatus'),
          icon: <Activity size={16} />,
        },
        canViewEventLogs && {
          to: '/event-logs',
          text: t('navigation.events'),
          icon: <ScrollText size={16} />,
        },
        canViewAuditLogs && {
          to: '/audit-logs',
          text: t('navigation.auditLogs'),
          icon: <ClipboardList size={16} />,
        },
        canManageNotifications && {
          to: '/notifications',
          text: t('navigation.notifications'),
          icon: <Bell size={16} />,
          activePaths: [
            '/notifications',
            '/notification-rules',
            '/notification-channels',
          ],
        },
        canManageIncidents && {
          to: '/incidents',
          text: t('navigation.incidents'),
          icon: <AlertTriangle size={16} />,
          activePaths: [
            '/incidents',
            '/incident-providers',
            '/incident-policies',
          ],
        },
        isAdmin && {
          to: '/license#monitoring',
          text: t('navigation.externalMonitoring'),
          icon: <Radar size={16} />,
        },
      ],
    },
    {
      key: 'configure',
      label: t('navigation.configure'),
      items: [
        canManageProfiles && {
          to: '/profiles',
          text: t('navigation.profilesSecrets'),
          icon: <SlidersHorizontal size={16} />,
        },
        canWrite && {
          to: '/base-config',
          text: t('navigation.baseConfig'),
          icon: <FileCog size={16} />,
        },
        canManageWebhooks && {
          to: '/webhooks',
          text: t('navigation.webhooks'),
          icon: <Webhook size={16} />,
        },
        canAccessGitSync &&
          config.gitSyncEnabled && {
            to: '/git-sync',
            text: t('navigation.gitSync'),
            icon: <GitBranch size={16} />,
          },
        {
          to: '/api-docs',
          text: t('navigation.apiReference'),
          icon: <FileCode2 size={16} />,
        },
      ],
    },
    isAdmin && {
      key: 'administration',
      label: t('navigation.administration'),
      items: [
        config.authMode === 'builtin' && {
          to: '/users',
          text: t('navigation.users'),
          icon: <Users size={16} />,
        },
        config.authMode === 'builtin' && {
          to: '/api-keys',
          text: t('navigation.apiKeys'),
          icon: <KeyRound size={16} />,
        },
        {
          to: '/remote-nodes',
          text: t('navigation.remoteNodes'),
          icon: <Server size={16} />,
        },
        config.terminalEnabled && {
          to: '/terminal',
          text: t('navigation.terminal'),
          icon: <SquareTerminal size={16} />,
        },
      ],
    },
  ];

  return {
    mainItems,
    sections: sections
      .filter(
        (section): section is NavigationSection<NavigationLink | false> =>
          section !== false
      )
      .map((section) => ({
        ...section,
        items: section.items.filter(
          (item): item is NavigationLink => item !== false
        ),
      }))
      .filter((section) => section.items.length > 0),
  };
}
