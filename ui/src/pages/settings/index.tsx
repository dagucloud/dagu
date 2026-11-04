// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import Title from '@/components/ui/title';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useLicenseState } from '@/hooks/useLicense';
import { useI18n } from '@/i18n/I18nProvider';
import LicenseSection from '@/pages/license';
import RemoteNodesSection from '@/pages/remote-nodes';
import React from 'react';
import { useLocation } from 'react-router-dom';

/** The server's administrative settings on one page. */
export default function SettingsPage(): React.ReactElement {
  const { setTitle } = React.useContext(AppBarContext);
  const { t } = useI18n();
  const { loading } = useLicenseState();
  const { hash, key: locationKey } = useLocation();

  React.useEffect(() => {
    setTitle(t('navigation.settings'));
  }, [setTitle, t]);

  // A visit to #remote-nodes scrolls once the plan above it has its height.
  const handledVisit = React.useRef<string | null>(null);
  React.useEffect(() => {
    if (hash !== '#remote-nodes' || loading) return;
    if (handledVisit.current === locationKey) return;
    handledVisit.current = locationKey;
    document.getElementById('remote-nodes')?.scrollIntoView({ block: 'start' });
  }, [hash, locationKey, loading]);

  return (
    <div className="flex w-full max-w-5xl flex-col gap-10 pb-8">
      <Title>{t('navigation.settings')}</Title>
      <LicenseSection>
        <RemoteNodesSection />
      </LicenseSection>
    </div>
  );
}
