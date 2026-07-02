// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

declare module '@scalar/api-reference-react' {
  import type * as React from 'react';

  export const ApiReferenceReact: React.ComponentType<{
    configuration: Record<string, unknown>;
  }>;

  const DefaultExport: typeof ApiReferenceReact;
  export default DefaultExport;
}
