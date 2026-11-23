// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The checks of the restart policy of a DAG (spec 080), as the loader says them.
export default defineMessages({
  en: {
    'restart_policy cannot be combined with schedule':
      'restart_policy cannot be combined with schedule',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy cannot be combined with retry_policy',
  },
  'zh-CN': {
    'restart_policy cannot be combined with schedule':
      'restart_policy 不能与 schedule 同时使用',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy 不能与 retry_policy 同时使用',
  },
  ja: {
    'restart_policy cannot be combined with schedule':
      'restart_policy は schedule と併用できません',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy は retry_policy と併用できません',
  },
});
