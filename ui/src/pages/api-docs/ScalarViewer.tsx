// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ApiReferenceReact } from '@scalar/api-reference-react';
import '@scalar/api-reference-react/style.css';
import * as React from 'react';

type ScalarViewerProps = {
  spec: Record<string, unknown>;
  preferredBearerToken?: string;
};

export default function ScalarViewer({
  spec,
  preferredBearerToken,
}: ScalarViewerProps): React.ReactElement {
  const [darkMode, setDarkMode] = React.useState(
    () =>
      typeof document !== 'undefined' &&
      document.documentElement.classList.contains('dark')
  );

  React.useEffect(() => {
    if (typeof document === 'undefined') {
      return;
    }

    const updateDarkMode = () => {
      setDarkMode(document.documentElement.classList.contains('dark'));
    };
    updateDarkMode();

    const observer = new MutationObserver(updateDarkMode);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ['class'],
    });

    return () => observer.disconnect();
  }, []);

  const configuration: Record<string, unknown> = {
    content: spec,
    layout: 'modern',
    hideDarkModeToggle: true,
    withDefaultFonts: false,
    forceDarkModeState: darkMode ? 'dark' : 'light',
  };

  if (preferredBearerToken) {
    configuration.authentication = {
      preferredSecurityScheme: 'apiToken',
      securitySchemes: {
        apiToken: {
          token: preferredBearerToken,
        },
      },
    };
  }

  return (
    <div className="api-docs-viewer h-full min-h-0">
      <ApiReferenceReact configuration={configuration} />
    </div>
  );
}
