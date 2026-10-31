// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { loader } from '@monaco-editor/react';
import * as monaco from 'monaco-editor';
import { KILN_DARK, KILN_LIGHT, registerKilnThemes } from '@/lib/monaco-theme';
// The YAML service that the DAG editor uses, for the YAML field of a step.
import '@/lib/monaco-yaml';

// Same setup as the DAG editor: the bundled Monaco, not a CDN copy.
loader.config({ monaco });
registerKilnThemes();

/** Monaco theme that matches the current color mode of the page. */
export function monacoTheme(): string {
  const dark =
    typeof document !== 'undefined' &&
    document.documentElement.classList.contains('dark');
  return dark ? KILN_DARK : KILN_LIGHT;
}
