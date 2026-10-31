// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The duration extents of the run timeline: lanes, the cap badge, the long
// run marker and the lines of their tooltips.
export default defineMessages({
  en: {
    'Run durations': 'Run durations',
    'Long run': 'Long run',
    'Started before this view': 'Started before this view',
    'Still running': 'Still running',
    'Ended {time}': 'Ended {time}',
    'overlaps {n} other run': 'overlaps {n} other run',
    'overlaps {n} other runs': 'overlaps {n} other runs',
    '{n} more run overlaps': '{n} more run overlaps',
    '{n} more runs overlap': '{n} more runs overlap',
  },
  'zh-CN': {
    'Run durations': '运行时长',
    'Long run': '长时间运行',
    'Started before this view': '在此视图之前开始',
    'Still running': '仍在运行',
    'Ended {time}': '结束于 {time}',
    'overlaps {n} other run': '与另外 {n} 次运行重叠',
    'overlaps {n} other runs': '与另外 {n} 次运行重叠',
    '{n} more run overlaps': '另有 {n} 次运行重叠',
    '{n} more runs overlap': '另有 {n} 次运行重叠',
  },
  ja: {
    'Run durations': '実行時間',
    'Long run': '長時間の実行',
    'Started before this view': 'この表示より前に開始',
    'Still running': '実行中',
    'Ended {time}': '{time} に終了',
    'overlaps {n} other run': '他の {n} 件の実行と重なる',
    'overlaps {n} other runs': '他の {n} 件の実行と重なる',
    '{n} more run overlaps': 'さらに {n} 件の実行が重なる',
    '{n} more runs overlap': 'さらに {n} 件の実行が重なる',
  },
});
