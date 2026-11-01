// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The text tools of the YAML views.
export default defineMessages({
  en: {
    'Apply all quick fixes': 'Apply all quick fixes',
    'No quick fix to apply': 'No quick fix to apply',
    'Fix all quick fixes in the file': 'Fix all quick fixes in the file',
    'Format YAML': 'Format YAML',
    'Fix the YAML syntax error before you format':
      'Fix the YAML syntax error before you format',
    'The YAML is already formatted': 'The YAML is already formatted',
  },
  'zh-CN': {
    'Apply all quick fixes': '应用所有快速修复',
    'No quick fix to apply': '没有可应用的快速修复',
    'Fix all quick fixes in the file': '修复文件中的所有快速修复',
    'Format YAML': '格式化 YAML',
    'Fix the YAML syntax error before you format':
      '请先修复 YAML 语法错误，再格式化',
    'The YAML is already formatted': 'YAML 已是标准格式',
  },
  ja: {
    'Apply all quick fixes': 'すべてのクイックフィックスを適用',
    'No quick fix to apply': '適用できるクイックフィックスはありません',
    'Fix all quick fixes in the file':
      'ファイル内のすべてのクイックフィックスを修正',
    'Format YAML': 'YAML を整形',
    'Fix the YAML syntax error before you format':
      '整形する前に YAML の構文エラーを修正してください',
    'The YAML is already formatted': 'YAML は整形済みです',
  },
});
