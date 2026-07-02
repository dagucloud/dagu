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
  const darkMode =
    typeof document !== 'undefined' && document.documentElement.classList.contains('dark');

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
