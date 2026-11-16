// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { AliasMap } from './canvas/paletteModel';

/**
 * Words that users type for an action that has another name. Each word gives
 * the names of the actions to find. Every name must be in the catalog of the
 * DAG schema: a test checks it.
 */
export const ADD_STEP_ALIASES: AliasMap = {
  bash: ['run'],
  echo: ['run', 'log.write'],
  curl: ['http.request'],
  wget: ['http.request'],
  sleep: ['wait.duration'],
  delay: ['wait.duration'],
  email: ['mail.send'],
  mcp: ['mcp.call'],
  a2a: ['a2a.send', 'a2a.get_task'],
  agent: ['a2a.send', 'a2a.get_task'],
  sql: ['postgres.query', 'sqlite.query', 'clickhouse.query'],
  if: ['router.route', 'decision.evaluate'],
  switch: ['router.route'],
  subworkflow: ['dag.run', 'dag.enqueue'],
};
