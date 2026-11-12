// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useContext, useEffect } from 'react';
import { useLocation } from 'react-router-dom';
import { NotFoundState } from '@/components/PageStates';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nTemplate } from '@/i18n/I18nTemplate';

function NotFoundPage() {
  const { pathname } = useLocation();
  const { setTitle } = useContext(AppBarContext);
  const { ts } = useI18n();

  useEffect(() => {
    setTitle(ts('Page not found'));
  }, [setTitle, ts]);

  return (
    <NotFoundState
      title={ts('Page not found')}
      description={
        <I18nTemplate
          text="Nothing exists at {path}. Check the address or start from your workflows."
          values={{ path: <code>{pathname}</code> }}
        />
      }
      action={{ to: '/dags', label: ts('Go to Workflows') }}
    />
  );
}

export default NotFoundPage;
