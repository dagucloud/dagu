// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The DAGs card of an agent step on the canvas, and the enlarged view of the
// DAG that a DAG tool calls.
export default defineMessages({
  en: {
    'Add a DAG': 'Add a DAG',
    'Skill not found in the skills directory':
      'Skill not found in the skills directory',
    'No DAGs': 'No DAGs',
    'DAG not found': 'DAG not found',
    'Chosen when the step runs': 'Chosen when the step runs',
    'No input': 'No input',
    '{count} step': '{count} step',
    '{count} steps': '{count} steps',
    '{steps} steps in {count} stages': '{steps} steps in {count} stages',
    'Show the graph of {name}': 'Show the graph of {name}',
    'This DAG calls itself.': 'This DAG calls itself.',
    '{name} run': '{name} run',
    'Path of the DAG': 'Path of the DAG',
    'Calls {name}': 'Calls {name}',
    'Open {name} here': 'Open {name} here',
    'Open already: a call that loops is shown once.':
      'Open already: a call that loops is shown once.',
    'Select a step to see its details.': 'Select a step to see its details.',
    'This DAG has no steps.': 'This DAG has no steps.',
    'Edit the tool': 'Edit the tool',
    'Only if': 'Only if',
    'Search DAGs': 'Search DAGs',
    'No DAG matches the search.': 'No DAG matches the search.',
    'Show all DAGs': 'Show all DAGs',
    'No DAGs yet. The model has nothing to call. Pick a DAG below.':
      'No DAGs yet. The model has nothing to call. Pick a DAG below.',
    'Details of the DAG': 'Details of the DAG',
    'Choose a DAG': 'Choose a DAG',
    'Select a DAG in the list to see its details here.':
      'Select a DAG in the list to see its details here.',
    'Show the DAGs of the step {name}': 'Show the DAGs of the step {name}',
    Parallel: 'Parallel',
  },
  'zh-CN': {
    'Add a DAG': '添加 DAG',
    'Skill not found in the skills directory': '在技能目录中找不到该技能',
    'No DAGs': '没有 DAG',
    'DAG not found': '找不到 DAG',
    'Chosen when the step runs': '步骤运行时确定',
    'No input': '无输入',
    '{count} step': '{count} 个步骤',
    '{count} steps': '{count} 个步骤',
    '{steps} steps in {count} stages': '{count} 个阶段，共 {steps} 个步骤',
    'Show the graph of {name}': '显示 {name} 的图',
    'This DAG calls itself.': '此 DAG 调用自身。',
    '{name} run': '{name} 的运行',
    'Path of the DAG': 'DAG 路径',
    'Calls {name}': '调用 {name}',
    'Open {name} here': '在此打开 {name}',
    'Open already: a call that loops is shown once.':
      '已打开：循环调用只显示一次。',
    'Select a step to see its details.': '选择一个步骤以查看详情。',
    'This DAG has no steps.': '此 DAG 没有步骤。',
    'Edit the tool': '编辑工具',
    'Only if': '仅当',
    'Search DAGs': '搜索 DAG',
    'No DAG matches the search.': '没有匹配搜索的 DAG。',
    'Show all DAGs': '显示全部 DAG',
    'No DAGs yet. The model has nothing to call. Pick a DAG below.':
      '还没有 DAG。模型没有可调用的内容。请在下方选择一个 DAG。',
    'Details of the DAG': 'DAG 详情',
    'Choose a DAG': '选择 DAG',
    'Select a DAG in the list to see its details here.':
      '在列表中选择一个 DAG，即可在此查看详情。',
    'Show the DAGs of the step {name}': '显示步骤 {name} 的 DAG',
    Parallel: '并行',
  },
  ja: {
    'Add a DAG': 'DAG を追加',
    'Skill not found in the skills directory':
      'スキルディレクトリにスキルが見つかりません',
    'No DAGs': 'DAG なし',
    'DAG not found': 'DAG が見つかりません',
    'Chosen when the step runs': 'ステップの実行時に決まります',
    'No input': '入力なし',
    '{count} step': 'ステップ {count} 件',
    '{count} steps': 'ステップ {count} 件',
    '{steps} steps in {count} stages': '{count} 段階、ステップ {steps} 件',
    'Show the graph of {name}': '{name} のグラフを表示',
    'This DAG calls itself.': 'この DAG は自分自身を呼び出します。',
    '{name} run': '{name} の実行',
    'Path of the DAG': 'DAG のパス',
    'Calls {name}': '{name} を呼び出し',
    'Open {name} here': 'ここで {name} を開く',
    'Open already: a call that loops is shown once.':
      '開いています。ループする呼び出しは一度だけ表示します。',
    'Select a step to see its details.': 'ステップを選ぶと詳細を表示します。',
    'This DAG has no steps.': 'この DAG にはステップがありません。',
    'Edit the tool': 'ツールを編集',
    'Only if': '条件',
    'Search DAGs': 'DAG を検索',
    'No DAG matches the search.': '検索に一致する DAG はありません。',
    'Show all DAGs': 'すべての DAG を表示',
    'No DAGs yet. The model has nothing to call. Pick a DAG below.':
      'DAG はまだありません。モデルが呼び出せるものがありません。下から DAG を選んでください。',
    'Details of the DAG': 'DAG の詳細',
    'Choose a DAG': 'DAG を選択',
    'Select a DAG in the list to see its details here.':
      'リストで DAG を選ぶと、ここに詳細を表示します。',
    'Show the DAGs of the step {name}': 'ステップ {name} の DAG を表示',
    Parallel: '並列',
  },
});
