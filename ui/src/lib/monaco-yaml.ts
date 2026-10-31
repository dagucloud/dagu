// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The YAML language service of Monaco, configured once when this module
 * loads. Every YAML editor of the page shares it: the DAG editor and the
 * step fields import this module, so that the first of them to load sets it.
 *
 * @module lib/monaco-yaml
 */
import * as monaco from 'monaco-editor';
import { configureMonacoYaml } from 'monaco-yaml';

export const monacoYaml = configureMonacoYaml(monaco, {
  enableSchemaRequest: true,
  hover: true,
  completion: true,
  validate: true,
  format: true,
  schemas: [],
});
