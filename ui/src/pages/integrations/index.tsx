// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import Title from '@/components/ui/title';
import { AppBarContext } from '@/contexts/AppBarContext';
import React from 'react';
import { Link } from 'react-router-dom';

type IntegrationLink = {
  to: string;
  label: string;
  description: string;
};

const integrationLinks: IntegrationLink[] = [
  {
    to: '/webhooks',
    label: 'Webhooks',
    description: 'Trigger workflows from external systems.',
  },
  {
    to: '/api-docs',
    label: 'API Docs',
    description: 'Explore authenticated REST API endpoints.',
  },
];

export default function IntegrationsPage(): React.ReactElement {
  const { setTitle } = React.useContext(AppBarContext);

  React.useEffect(() => {
    setTitle('Integrations');
  }, [setTitle]);

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-auto">
      <Title>Integrations</Title>

      <div className="grid gap-3 md:grid-cols-2">
        {integrationLinks.map((link) => (
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
    </div>
  );
}
