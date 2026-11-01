// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// Launchers (Manual, Cron, Webhook), their panels and the tabs of the
// Workflow panel.
export default defineMessages({
  en: {
    Cron: 'Cron',
    'Cron, {count} schedule': 'Cron, {count} schedule',
    'Cron, {count} schedules': 'Cron, {count} schedules',
    'Always on': 'Always on',
    'Not set up': 'Not set up',
    Launcher: 'Launcher',
    'Manual launch is always active': 'Manual launch is always active',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.',
    'This workflow has no parameters': 'This workflow has no parameters',
    'Cron expression': 'Cron expression',
    'Cron expressions': 'Cron expressions',
    'New cron expression': 'New cron expression',
    'Add an expression': 'Add an expression',
    'Remove this expression': 'Remove this expression',
    'Remove schedule': 'Remove schedule',
    'Next runs:': 'Next runs:',
    'No preview for this expression': 'No preview for this expression',
    'The workflow starts on each of these cron expressions.':
      'The workflow starts on each of these cron expressions.',
    '{count} other schedule entries stay in the YAML. Edit them there.':
      '{count} other schedule entries stay in the YAML. Edit them there.',
    Hourly: 'Hourly',
    'Daily at 09:00': 'Daily at 09:00',
    'Weekdays at 09:00': 'Weekdays at 09:00',
    'Workflow sections': 'Workflow sections',
    'Workflow > {tab}': 'Workflow > {tab}',
  },
  'zh-CN': {
    Cron: '定时（Cron）',
    'Cron, {count} schedule': '定时（Cron），{count} 个计划',
    'Cron, {count} schedules': '定时（Cron），{count} 个计划',
    'Always on': '始终开启',
    'Not set up': '未设置',
    Launcher: '启动方式',
    'Manual launch is always active': '手动启动始终可用',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      '使用顶栏的“启动”按钮立即运行此工作流。运行使用你在启动对话框中填写的参数。',
    'This workflow has no parameters': '此工作流没有参数',
    'Cron expression': 'Cron 表达式',
    'Cron expressions': 'Cron 表达式',
    'New cron expression': '新的 Cron 表达式',
    'Add an expression': '添加表达式',
    'Remove this expression': '删除此表达式',
    'Remove schedule': '删除计划',
    'Next runs:': '后续运行：',
    'No preview for this expression': '此表达式无法预览',
    'The workflow starts on each of these cron expressions.':
      '工作流按以下每个 Cron 表达式启动。',
    '{count} other schedule entries stay in the YAML. Edit them there.':
      '另有 {count} 个计划条目保留在 YAML 中，请在 YAML 中编辑。',
    Hourly: '每小时',
    'Daily at 09:00': '每天 09:00',
    'Weekdays at 09:00': '工作日 09:00',
    'Workflow sections': '工作流分区',
    'Workflow > {tab}': '工作流 > {tab}',
  },
  ja: {
    Cron: '定期実行（Cron）',
    'Cron, {count} schedule': '定期実行（Cron）、スケジュール {count} 件',
    'Cron, {count} schedules': '定期実行（Cron）、スケジュール {count} 件',
    'Always on': '常に有効',
    'Not set up': '未設定',
    Launcher: '起動方法',
    'Manual launch is always active': '手動起動は常に有効です',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      'バーの開始ボタンで、このワークフローをすぐに実行します。実行には、開始ダイアログで指定したパラメータが使われます。',
    'This workflow has no parameters':
      'このワークフローにパラメータはありません',
    'Cron expression': 'Cron 式',
    'Cron expressions': 'Cron 式',
    'New cron expression': '新しい Cron 式',
    'Add an expression': '式を追加',
    'Remove this expression': 'この式を削除',
    'Remove schedule': 'スケジュールを削除',
    'Next runs:': '次回の実行:',
    'No preview for this expression': 'この式はプレビューできません',
    'The workflow starts on each of these cron expressions.':
      'ワークフローは、次の各 Cron 式で開始します。',
    '{count} other schedule entries stay in the YAML. Edit them there.':
      'ほかのスケジュール項目 {count} 件は YAML に残ります。YAML で編集してください。',
    Hourly: '毎時',
    'Daily at 09:00': '毎日 09:00',
    'Weekdays at 09:00': '平日 09:00',
    'Workflow sections': 'ワークフローのセクション',
    'Workflow > {tab}': 'ワークフロー > {tab}',
  },
});
