// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The pages of older runs that the History tab reads.
export default defineMessages({
  en: {
    'Loading older runs...': 'Loading older runs...',
    "Couldn't load older runs": "Couldn't load older runs",
    "Couldn't load older runs. Retry": "Couldn't load older runs. Retry",
  },
  'zh-CN': {
    'Loading older runs...': '正在加载更早的运行...',
    "Couldn't load older runs": '无法加载更早的运行',
    "Couldn't load older runs. Retry": '无法加载更早的运行。重试',
  },
  ja: {
    'Loading older runs...': '過去の実行を読み込み中...',
    "Couldn't load older runs": '過去の実行を読み込めませんでした',
    "Couldn't load older runs. Retry":
      '過去の実行を読み込めませんでした。再試行',
  },
});
