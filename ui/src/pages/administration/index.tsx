// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import Title from '@/components/ui/title';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useI18n } from '@/i18n/I18nProvider';
import React from 'react';
import { Link } from 'react-router-dom';

type AdminLink = {
  to: string;
  label: string;
  description: string;
};

type AdminSection = {
  title: string;
  links: AdminLink[];
};

function SectionLinks({
  section,
}: {
  section: AdminSection;
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

export default function AdministrationPage(): React.ReactElement {
  const { setTitle } = React.useContext(AppBarContext);
  const { t } = useI18n();
  const config = useConfig();

  React.useEffect(() => {
    setTitle(t('navigation.administration'));
  }, [setTitle, t]);

  const sections: AdminSection[] = [
    {
      title: t('navigation.access'),
      links:
        config.authMode === 'builtin'
          ? [
              {
                to: '/users',
                label: t('navigation.users'),
                description: t('administration.usersDescription'),
              },
              {
                to: '/api-keys',
                label: t('navigation.apiKeys'),
                description: t('administration.apiKeysDescription'),
              },
            ]
          : [],
    },
    {
      title: t('administration.security'),
      links: [
        {
          to: '/profiles',
          label: t('navigation.profilesSecrets'),
          description: t('administration.profilesDescription'),
        },
      ],
    },
    {
      title: t('navigation.infrastructure'),
      links: [
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
      ],
    },
  ].filter((section) => section.links.length > 0);

  return (
    <div className="flex h-full min-h-0 flex-col gap-5 overflow-auto">
      <Title>{t('navigation.administration')}</Title>

      {sections.map((section) => (
        <SectionLinks key={section.title} section={section} />
      ))}
    </div>
  );
}
