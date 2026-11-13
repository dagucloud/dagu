// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// Launchers (Manual, Cron, Always on, Webhook), their panels and the tabs of the
// Workflow panel.
export default defineMessages({
  en: {
    Cron: 'Cron',
    'Cron, {count} schedule': 'Cron, {count} schedule',
    'Cron, {count} schedules': 'Cron, {count} schedules',
    'Always on': 'Always on',
    'Cron or Always on': 'Cron or Always on',
    'Not set up. Choose a cron schedule or Always on.':
      'Not set up. Choose a cron schedule or Always on.',
    'Choose how the workflow starts': 'Choose how the workflow starts',
    'Start on a schedule of cron expressions.':
      'Start on a schedule of cron expressions.',
    'Keep one run alive and start a new one when it ends.':
      'Keep one run alive and start a new one when it ends.',
    'Switch to Always on': 'Switch to Always on',
    'Switch to Cron': 'Switch to Cron',
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
    'Remove all cron expressions': 'Remove all cron expressions',
    'Other schedule entries (stop, restart, one-off) stay in the YAML.':
      'Other schedule entries (stop, restart, one-off) stay in the YAML.',
    'Next runs:': 'Next runs:',
    'No preview for this expression': 'No preview for this expression',
    'The workflow starts on each of these cron expressions.':
      'The workflow starts on each of these cron expressions.',
    '{count} other schedule entries stay in the YAML. Edit them there.':
      '{count} other schedule entries stay in the YAML. Edit them there.',
    Hourly: 'Hourly',
    'Daily at 09:00': 'Daily at 09:00',
    'Weekdays at 09:00': 'Weekdays at 09:00',
    'Edit with the form': 'Edit with the form',
    Frequency: 'Frequency',
    'Choose a frequency': 'Choose a frequency',
    'Custom expression': 'Custom expression',
    'Every few minutes': 'Every few minutes',
    Daily: 'Daily',
    Weekly: 'Weekly',
    Monthly: 'Monthly',
    'Minutes between runs': 'Minutes between runs',
    'Minute of the hour': 'Minute of the hour',
    'Day of the month': 'Day of the month',
    'Time of day': 'Time of day',
    'Days of the week': 'Days of the week',
    'Every minute': 'Every minute',
    'Every {n} minutes': 'Every {n} minutes',
    'Every hour at minute {minute}': 'Every hour at minute {minute}',
    'Every day at {time}': 'Every day at {time}',
    'Every week on {days} at {time}': 'Every week on {days} at {time}',
    'Every month on day {day} at {time}': 'Every month on day {day} at {time}',
    'A month without day {day} has no run.':
      'A month without day {day} has no run.',
    'Times are in the time zone of the scheduler ({tz}).':
      'Times are in the time zone of the scheduler ({tz}).',
    'Workflow sections': 'Workflow sections',
    Available: 'Available',
    'Restarts after each run': 'Restarts after each run',
    'Restarts unless stopped': 'Restarts unless stopped',
    '{state}, wait {seconds} s': '{state}, wait {seconds} s',
    'unless stopped': 'unless stopped',
    '{seconds} s': '{seconds} s',
    'keeps {count} runs': 'keeps {count} runs',
    'keeps {count} run': 'keeps {count} run',
    'keeps {count} runs (default)': 'keeps {count} runs (default)',
    '{state}, {history}': '{state}, {history}',
    'Restart policy': 'Restart policy',
    'Restart settings': 'Restart settings',
    Always: 'Always',
    'Unless stopped': 'Unless stopped',
    'A new run follows each run, whatever its status.':
      'A new run follows each run, whatever its status.',
    'A new run follows each run, except a run that an operator stopped.':
      'A new run follows each run, except a run that an operator stopped.',
    'The scheduler keeps one run of this workflow alive. It starts a new run when the last run ends.':
      'The scheduler keeps one run of this workflow alive. It starts a new run when the last run ends.',
    'Wait before a new run (s)': 'Wait before a new run (s)',
    'Runs kept in the history': 'Runs kept in the history',
    'With no value, the history keeps the latest 100 runs.':
      'With no value, the history keeps the latest 100 runs.',
    'Remove the restart policy': 'Remove the restart policy',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.',
    'Not available with Always on: a workflow that is always on cannot have a schedule. Turn Always on off to use cron.':
      'Not available with Always on: a workflow that is always on cannot have a schedule. Turn Always on off to use cron.',
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      'A service DAG cannot have a schedule or a retry_policy. Remove it first.',
    'The value must be a positive integer.':
      'The value must be a positive integer.',
    'Workflow > {tab}': 'Workflow > {tab}',
  },
  'zh-CN': {
    Cron: '定时（Cron）',
    'Cron, {count} schedule': '定时（Cron），{count} 个计划',
    'Cron, {count} schedules': '定时（Cron），{count} 个计划',
    'Always on': '始终开启',
    'Cron or Always on': 'Cron 或始终开启',
    'Not set up. Choose a cron schedule or Always on.':
      '未设置。选择 cron 计划或始终开启。',
    'Choose how the workflow starts': '选择工作流的启动方式',
    'Start on a schedule of cron expressions.': '按 cron 表达式的计划启动。',
    'Keep one run alive and start a new one when it ends.':
      '保持一个运行，结束后启动新的运行。',
    'Switch to Always on': '切换为始终开启',
    'Switch to Cron': '切换为 Cron',
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
    'Remove all cron expressions': '删除所有 Cron 表达式',
    'Other schedule entries (stop, restart, one-off) stay in the YAML.':
      '其他计划条目（stop、restart、一次性）保留在 YAML 中。',
    'Next runs:': '后续运行：',
    'No preview for this expression': '此表达式无法预览',
    'The workflow starts on each of these cron expressions.':
      '工作流按以下每个 Cron 表达式启动。',
    '{count} other schedule entries stay in the YAML. Edit them there.':
      '另有 {count} 个计划条目保留在 YAML 中，请在 YAML 中编辑。',
    Hourly: '每小时',
    'Daily at 09:00': '每天 09:00',
    'Weekdays at 09:00': '工作日 09:00',
    'Edit with the form': '用表单编辑',
    Frequency: '频率',
    'Choose a frequency': '选择频率',
    'Custom expression': '自定义表达式',
    'Every few minutes': '每隔几分钟',
    Daily: '每天',
    Weekly: '每周',
    Monthly: '每月',
    'Minutes between runs': '运行间隔（分钟）',
    'Minute of the hour': '每小时的第几分钟',
    'Day of the month': '每月的日期',
    'Time of day': '时间',
    'Days of the week': '星期',
    'Every minute': '每分钟',
    'Every {n} minutes': '每 {n} 分钟',
    'Every hour at minute {minute}': '每小时的第 {minute} 分',
    'Every day at {time}': '每天 {time}',
    'Every week on {days} at {time}': '每周 {days} {time}',
    'Every month on day {day} at {time}': '每月 {day} 日 {time}',
    'A month without day {day} has no run.': '没有 {day} 日的月份不会运行。',
    'Times are in the time zone of the scheduler ({tz}).':
      '时间采用调度器的时区（{tz}）。',
    'Workflow sections': '工作流分区',
    Available: '可用',
    'Restarts after each run': '每次运行结束后重新启动',
    'Restarts unless stopped': '除非被停止，否则重新启动',
    '{state}, wait {seconds} s': '{state}，等待 {seconds} 秒',
    'unless stopped': '除非停止',
    '{seconds} s': '{seconds} 秒',
    'keeps {count} runs': '保留 {count} 次运行',
    'keeps {count} run': '保留 {count} 次运行',
    'keeps {count} runs (default)': '保留 {count} 次运行（默认）',
    '{state}, {history}': '{state}，{history}',
    'Restart policy': '重启策略',
    'Restart settings': '重启设置',
    Always: '始终',
    'Unless stopped': '除非被停止',
    'A new run follows each run, whatever its status.':
      '每次运行结束后都会启动新的运行，无论其状态如何。',
    'A new run follows each run, except a run that an operator stopped.':
      '每次运行结束后都会启动新的运行，但操作员停止的运行除外。',
    'The scheduler keeps one run of this workflow alive. It starts a new run when the last run ends.':
      '调度器让此工作流始终保持一个运行。上一个运行结束时，它会启动新的运行。',
    'Wait before a new run (s)': '新运行前的等待时间（秒）',
    'Runs kept in the history': '历史中保留的运行数',
    'With no value, the history keeps the latest 100 runs.':
      '未设置时，历史保留最近 100 次运行。',
    'Remove the restart policy': '删除重启策略',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      '请先删除工作流的 retry_policy：始终开启的工作流不能有它。',
    'Not available with Always on: a workflow that is always on cannot have a schedule. Turn Always on off to use cron.':
      '始终开启时不可用：始终开启的工作流不能有计划。关闭“始终开启”后才能使用 Cron。',
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      '服务型 DAG 不能有计划或 retry_policy。请先删除它。',
    'The value must be a positive integer.': '该值必须是正整数。',
    'Workflow > {tab}': '工作流 > {tab}',
  },
  ja: {
    Cron: '定期実行（Cron）',
    'Cron, {count} schedule': '定期実行（Cron）、スケジュール {count} 件',
    'Cron, {count} schedules': '定期実行（Cron）、スケジュール {count} 件',
    'Always on': '常に有効',
    'Cron or Always on': 'Cron または常に有効',
    'Not set up. Choose a cron schedule or Always on.':
      '未設定。cron スケジュールか常に有効を選びます。',
    'Choose how the workflow starts': 'ワークフローの開始方法を選択',
    'Start on a schedule of cron expressions.':
      'cron 式のスケジュールで開始します。',
    'Keep one run alive and start a new one when it ends.':
      '実行を 1 つ維持し、終了したら新しい実行を開始します。',
    'Switch to Always on': '常に有効に切り替え',
    'Switch to Cron': 'Cron に切り替え',
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
    'Remove all cron expressions': 'すべての Cron 式を削除',
    'Other schedule entries (stop, restart, one-off) stay in the YAML.':
      'ほかのスケジュール項目（stop、restart、単発）は YAML に残ります。',
    'Next runs:': '次回の実行:',
    'No preview for this expression': 'この式はプレビューできません',
    'The workflow starts on each of these cron expressions.':
      'ワークフローは、次の各 Cron 式で開始します。',
    '{count} other schedule entries stay in the YAML. Edit them there.':
      'ほかのスケジュール項目 {count} 件は YAML に残ります。YAML で編集してください。',
    Hourly: '毎時',
    'Daily at 09:00': '毎日 09:00',
    'Weekdays at 09:00': '平日 09:00',
    'Edit with the form': 'フォームで編集',
    Frequency: '頻度',
    'Choose a frequency': '頻度を選択',
    'Custom expression': 'カスタム式',
    'Every few minutes': '数分ごと',
    Daily: '毎日',
    Weekly: '毎週',
    Monthly: '毎月',
    'Minutes between runs': '実行間隔（分）',
    'Minute of the hour': '毎時の分',
    'Day of the month': '日付',
    'Time of day': '時刻',
    'Days of the week': '曜日',
    'Every minute': '毎分',
    'Every {n} minutes': '{n} 分ごと',
    'Every hour at minute {minute}': '毎時 {minute} 分',
    'Every day at {time}': '毎日 {time}',
    'Every week on {days} at {time}': '毎週 {days} の {time}',
    'Every month on day {day} at {time}': '毎月 {day} 日の {time}',
    'A month without day {day} has no run.':
      '{day} 日がない月は実行されません。',
    'Times are in the time zone of the scheduler ({tz}).':
      '時刻はスケジューラーのタイムゾーン（{tz}）です。',
    'Workflow sections': 'ワークフローのセクション',
    Available: '利用可能',
    'Restarts after each run': '実行が終わるたびに再起動',
    'Restarts unless stopped': '停止されない限り再起動',
    '{state}, wait {seconds} s': '{state}、待機 {seconds} 秒',
    'unless stopped': '停止時を除く',
    '{seconds} s': '{seconds} 秒',
    'keeps {count} runs': '実行 {count} 件を保持',
    'keeps {count} run': '実行 {count} 件を保持',
    'keeps {count} runs (default)': '実行 {count} 件を保持（既定）',
    '{state}, {history}': '{state}、{history}',
    'Restart policy': '再起動ポリシー',
    'Restart settings': '再起動の設定',
    Always: '常に',
    'Unless stopped': '停止されない限り',
    'A new run follows each run, whatever its status.':
      '実行が終わるたびに、状態にかかわらず新しい実行を開始します。',
    'A new run follows each run, except a run that an operator stopped.':
      '実行が終わるたびに新しい実行を開始します。ただし、操作者が停止した実行の後は開始しません。',
    'The scheduler keeps one run of this workflow alive. It starts a new run when the last run ends.':
      'スケジューラーは、このワークフローの実行を 1 つ維持します。前の実行が終わると、新しい実行を開始します。',
    'Wait before a new run (s)': '新しい実行までの待機時間（秒）',
    'Runs kept in the history': '履歴に残す実行数',
    'With no value, the history keeps the latest 100 runs.':
      '値がない場合、履歴には直近 100 件の実行が残ります。',
    'Remove the restart policy': '再起動ポリシーを削除',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      '先にワークフローの retry_policy を削除してください。常に有効なワークフローには置けません。',
    'Not available with Always on: a workflow that is always on cannot have a schedule. Turn Always on off to use cron.':
      '常に有効のときは利用できません。常に有効なワークフローにスケジュールは置けません。Cron を使うには「常に有効」をオフにしてください。',
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      'サービス DAG にスケジュールと retry_policy は置けません。先に削除してください。',
    'The value must be a positive integer.': '値は正の整数にしてください。',
    'Workflow > {tab}': 'ワークフロー > {tab}',
  },
});
