// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The toolbar of the view selector: save, rename and delete the active view.
export default defineMessages({
  en: {
    'View actions': 'View actions',
    'Rename view': 'Rename view',
    'The view keeps its saved filters.': 'The view keeps its saved filters.',
    'Save the changes of “{name}”': 'Save the changes of “{name}”',
  },
  'zh-CN': {
    'View actions': '视图操作',
    'Rename view': '重命名视图',
    'The view keeps its saved filters.': '视图保留其已保存的筛选条件。',
    'Save the changes of “{name}”': '保存“{name}”的更改',
  },
  ja: {
    'View actions': 'ビューの操作',
    'Rename view': 'ビューの名前を変更',
    'The view keeps its saved filters.':
      'ビューは保存済みのフィルターをそのまま保持します。',
    'Save the changes of “{name}”': '「{name}」の変更を保存',
  },
});
