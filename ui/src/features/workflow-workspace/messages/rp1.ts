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
    'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration':
      'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration',
  },
  'zh-CN': {
    'restart_policy cannot be combined with schedule':
      'restart_policy 不能与 schedule 同时使用',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy 不能与 retry_policy 同时使用',
    'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration':
      '缺少 restart_wait_sec：请在文件中将其设为 1 或更大，或在基础配置中设置',
  },
  ja: {
    'restart_policy cannot be combined with schedule':
      'restart_policy は schedule と併用できません',
    'restart_policy cannot be combined with retry_policy':
      'restart_policy は retry_policy と併用できません',
    'restart_wait_sec is missing: set it to 1 or more in the file, or set it in the base configuration':
      'restart_wait_sec がありません。ファイルで 1 以上を設定するか、ベース設定で設定してください',
  },
});
