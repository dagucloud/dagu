// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// Links from a chat step to the skills and DAGs that it uses: the
// open icons and the Skills section of the step form.
export default defineMessages({
  en: {
    'Open {name}': 'Open {name}',
    'Opens in a new tab': 'Opens in a new tab',
    'Open the skill': 'Open the skill',
    'No skills yet': 'No skills yet',
    'Invalid skill entry': 'Invalid skill entry',
    'Remove skill {name}': 'Remove skill {name}',
    'The model reads a skill with the tool load_skill.':
      'The model reads a skill with the tool load_skill.',
    'The CLI reads a skill with its tool Skill.':
      'The CLI reads a skill with its tool Skill.',
  },
  'zh-CN': {
    'Open {name}': '打开 {name}',
    'Opens in a new tab': '在新标签页中打开',
    'Open the skill': '打开技能',
    'No skills yet': '还没有技能',
    'Invalid skill entry': '无效的技能条目',
    'Remove skill {name}': '删除技能 {name}',
    'The model reads a skill with the tool load_skill.':
      '模型通过工具 load_skill 读取技能。',
    'The CLI reads a skill with its tool Skill.':
      'CLI 通过其工具 Skill 读取技能。',
  },
  ja: {
    'Open {name}': '{name} を開く',
    'Opens in a new tab': '新しいタブで開きます',
    'Open the skill': 'スキルを開く',
    'No skills yet': 'スキルはまだありません',
    'Invalid skill entry': '無効なスキルエントリ',
    'Remove skill {name}': 'スキル {name} を削除',
    'The model reads a skill with the tool load_skill.':
      'モデルはツール load_skill でスキルを読み込みます。',
    'The CLI reads a skill with its tool Skill.':
      'CLI はそのツール Skill でスキルを読み込みます。',
  },
  de: {
    'Open {name}': '{name} öffnen',
    'Opens in a new tab': 'Öffnet in einem neuen Tab',
    'Open the skill': 'Skill öffnen',
    'No skills yet': 'Noch keine Skills',
    'Invalid skill entry': 'Ungültiger Skill-Eintrag',
    'Remove skill {name}': 'Skill {name} entfernen',
    'The model reads a skill with the tool load_skill.':
      'Das Modell liest einen Skill mit dem Tool load_skill.',
    'The CLI reads a skill with its tool Skill.':
      'Die CLI liest einen Skill mit ihrem Tool Skill.',
  },
  fr: {
    'Open {name}': 'Ouvrir {name}',
    'Opens in a new tab': 'S’ouvre dans un nouvel onglet',
    'Open the skill': 'Ouvrir la compétence',
    'No skills yet': 'Aucune compétence pour le moment',
    'Invalid skill entry': 'Entrée de compétence non valide',
    'Remove skill {name}': 'Retirer la compétence {name}',
    'The model reads a skill with the tool load_skill.':
      'Le modèle lit une compétence avec l’outil load_skill.',
    'The CLI reads a skill with its tool Skill.':
      'La CLI lit une compétence avec son outil Skill.',
  },
});
