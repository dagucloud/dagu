// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The row of sub-nodes under a chat step on the canvas: the Model node, the
// Tools and Skills cards, and the docks that add a tool or a skill.
export default defineMessages({
  en: {
    'No model set': 'No model set',
    '{count} tool': '{count} tool',
    '{count} tools': '{count} tools',
    Skills: 'Skills',
    'Add a skill': 'Add a skill',
    'No skills': 'No skills',
    'Add to {name}': 'Add to {name}',
    'Skill name': 'Skill name',
    'This skill is already in the list': 'This skill is already in the list',
    'The list of skills could not be loaded. Type a name instead.':
      'The list of skills could not be loaded. Type a name instead.',
  },
  'zh-CN': {
    'No model set': '未设置模型',
    '{count} tool': '{count} 个工具',
    '{count} tools': '{count} 个工具',
    Skills: '技能',
    'Add a skill': '添加技能',
    'No skills': '没有技能',
    'Add to {name}': '添加到 {name}',
    'Skill name': '技能名称',
    'This skill is already in the list': '此技能已在列表中',
    'The list of skills could not be loaded. Type a name instead.':
      '无法加载技能列表。请直接输入名称。',
  },
  ja: {
    'No model set': 'モデル未設定',
    '{count} tool': 'ツール {count} 件',
    '{count} tools': 'ツール {count} 件',
    Skills: 'スキル',
    'Add a skill': 'スキルを追加',
    'No skills': 'スキルはありません',
    'Add to {name}': '{name} に追加',
    'Skill name': 'スキル名',
    'This skill is already in the list': 'このスキルはすでにリストにあります',
    'The list of skills could not be loaded. Type a name instead.':
      'スキルの一覧を読み込めませんでした。名前を入力してください。',
  },
});
