// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The run details popover of the run chip: the button, the log button of the
// status line and the label and value of the progress row.
export default defineMessages({
  en: {
    'Run details': 'Run details',
    'Show log': 'Show log',
    Progress: 'Progress',
    '{done} of {total} succeeded': '{done} of {total} succeeded',
  },
  'zh-CN': {
    'Run details': '运行详情',
    'Show log': '显示日志',
    Progress: '进度',
    '{done} of {total} succeeded': '已成功 {done} / {total}',
  },
  ja: {
    'Run details': '実行の詳細',
    'Show log': 'ログを表示',
    Progress: '進捗',
    '{done} of {total} succeeded': '{done} / {total} 成功',
  },
});
