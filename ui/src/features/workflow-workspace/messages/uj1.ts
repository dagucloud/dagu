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
    'Edit the schedule in the YAML to switch to Always on.':
      'Edit the schedule in the YAML to switch to Always on.',
    'Not set up': 'Not set up',
    Launcher: 'Launcher',
    'Manual launch is always active': 'Manual launch is always active',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.',
    'This workflow has no parameters': 'This workflow has no parameters',
    'Cron expression': 'Cron expression',
    'New cron expression': 'New cron expression',
    'Add an expression': 'Add an expression',
    'Add schedule': 'Add schedule',
    'Use workflow default for {name}': 'Use workflow default for {name}',
    'Remove schedule': 'Remove schedule',
    'Next runs:': 'Next runs:',
    'No preview for this expression': 'No preview for this expression',
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
    'Wait {seconds} s': 'Wait {seconds} s',
    'Unless stopped, wait {seconds} s': 'Unless stopped, wait {seconds} s',
    'Next run in {seconds} s': 'Next run in {seconds} s',
    Starting: 'Starting',
    '{detail}, {live}': '{detail}, {live}',
    'next run in {seconds} s': 'next run in {seconds} s',
    starting: 'starting',
    'keeps {count} runs': 'keeps {count} runs',
    'keeps {count} run': 'keeps {count} run',
    'keeps {count} runs, or as many as the base configuration sets':
      'keeps {count} runs, or as many as the base configuration sets',
    'keeps runs for {count} days': 'keeps runs for {count} days',
    'keeps runs for {count} day': 'keeps runs for {count} day',
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
    'With no value, the history keeps the latest 100 runs, or as many as the base configuration sets.':
      'With no value, the history keeps the latest 100 runs, or as many as the base configuration sets.',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.',
    'Workflow > {tab}': 'Workflow > {tab}',
    'Does not start at {time}: {schedule} starts instead.':
      'Does not start at {time}: {schedule} starts instead.',
    'The workflow starts on each of these schedules.':
      'The workflow starts on each of these schedules.',
    Schedules: 'Schedules',
    'This expression is already in the schedule.':
      'This expression is already in the schedule.',
    'Edit the timing of this entry in the YAML.':
      'Edit the timing of this entry in the YAML.',
    'Edit this entry and its parameters in the YAML.':
      'Edit this entry and its parameters in the YAML.',
    'Not a parameter of this workflow': 'Not a parameter of this workflow',
    'Default ({value})': 'Default ({value})',
    'Default (not set)': 'Default (not set)',
    'Default (computed when the run starts)':
      'Default (computed when the run starts)',
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
    'Edit the schedule in the YAML to switch to Always on.':
      '要切换为始终开启，请在 YAML 中编辑计划。',
    'Not set up': '未设置',
    Launcher: '启动方式',
    'Manual launch is always active': '手动启动始终可用',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      '使用顶栏的“启动”按钮立即运行此工作流。运行使用你在启动对话框中填写的参数。',
    'This workflow has no parameters': '此工作流没有参数',
    'Cron expression': 'Cron 表达式',
    'New cron expression': '新的 Cron 表达式',
    'Add an expression': '添加表达式',
    'Add schedule': '添加计划',
    'Use workflow default for {name}': '使用工作流默认值：{name}',
    'Remove schedule': '删除计划',
    'Next runs:': '后续运行：',
    'No preview for this expression': '此表达式无法预览',
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
    'Wait {seconds} s': '等待 {seconds} 秒',
    'Unless stopped, wait {seconds} s': '除非被停止，等待 {seconds} 秒',
    'Next run in {seconds} s': '{seconds} 秒后再次运行',
    Starting: '正在启动',
    '{detail}, {live}': '{detail}，{live}',
    'next run in {seconds} s': '{seconds} 秒后再次运行',
    starting: '正在启动',
    'keeps {count} runs': '保留 {count} 次运行',
    'keeps {count} run': '保留 {count} 次运行',
    'keeps {count} runs, or as many as the base configuration sets':
      '保留 {count} 次运行，或基础配置设定的数量',
    'keeps runs for {count} days': '运行保留 {count} 天',
    'keeps runs for {count} day': '运行保留 {count} 天',
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
    'With no value, the history keeps the latest 100 runs, or as many as the base configuration sets.':
      '未设置时，历史保留最近 100 次运行，或基础配置设定的数量。',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      '请先删除工作流的 retry_policy：始终开启的工作流不能有它。',
    'Workflow > {tab}': '工作流 > {tab}',
    'Does not start at {time}: {schedule} starts instead.':
      '在 {time} 不会启动：改为由 {schedule} 启动。',
    'The workflow starts on each of these schedules.':
      '工作流按以下每个计划启动。',
    Schedules: '计划',
    'This expression is already in the schedule.': '该表达式已在计划中。',
    'Edit the timing of this entry in the YAML.':
      '请在 YAML 中编辑此条目的时间。',
    'Edit this entry and its parameters in the YAML.':
      '请在 YAML 中编辑此条目及其参数。',
    'Not a parameter of this workflow': '不是此工作流的参数',
    'Default ({value})': '默认（{value}）',
    'Default (not set)': '默认（未设置）',
    'Default (computed when the run starts)': '默认（运行开始时计算）',
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
    'Edit the schedule in the YAML to switch to Always on.':
      '常に有効に切り替えるには、YAML でスケジュールを編集してください。',
    'Not set up': '未設定',
    Launcher: '起動方法',
    'Manual launch is always active': '手動起動は常に有効です',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      'バーの開始ボタンで、このワークフローをすぐに実行します。実行には、開始ダイアログで指定したパラメータが使われます。',
    'This workflow has no parameters':
      'このワークフローにパラメータはありません',
    'Cron expression': 'Cron 式',
    'New cron expression': '新しい Cron 式',
    'Add an expression': '式を追加',
    'Add schedule': 'スケジュールを追加',
    'Use workflow default for {name}': '{name} にワークフローの既定値を使う',
    'Remove schedule': 'スケジュールを削除',
    'Next runs:': '次回の実行:',
    'No preview for this expression': 'この式はプレビューできません',
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
    'Wait {seconds} s': '待機 {seconds} 秒',
    'Unless stopped, wait {seconds} s': '停止されない限り、待機 {seconds} 秒',
    'Next run in {seconds} s': '次の実行まで {seconds} 秒',
    Starting: '起動中',
    '{detail}, {live}': '{detail}、{live}',
    'next run in {seconds} s': '次の実行まで {seconds} 秒',
    starting: '起動中',
    'keeps {count} runs': '実行 {count} 件を保持',
    'keeps {count} run': '実行 {count} 件を保持',
    'keeps {count} runs, or as many as the base configuration sets':
      '実行 {count} 件、または基本設定の件数を保持',
    'keeps runs for {count} days': '実行を {count} 日間保持',
    'keeps runs for {count} day': '実行を {count} 日間保持',
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
    'With no value, the history keeps the latest 100 runs, or as many as the base configuration sets.':
      '値がない場合、履歴には直近 100 件、または基本設定の件数の実行が残ります。',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      '先にワークフローの retry_policy を削除してください。常に有効なワークフローには置けません。',
    'Workflow > {tab}': 'ワークフロー > {tab}',
    'Does not start at {time}: {schedule} starts instead.':
      '{time} には起動しません。代わりに {schedule} が起動します。',
    'The workflow starts on each of these schedules.':
      'ワークフローは、次の各スケジュールで開始します。',
    Schedules: 'スケジュール',
    'This expression is already in the schedule.':
      'この式はすでにスケジュールにあります。',
    'Edit the timing of this entry in the YAML.':
      'この項目の実行タイミングは YAML で編集してください。',
    'Edit this entry and its parameters in the YAML.':
      'この項目とそのパラメータは YAML で編集してください。',
    'Not a parameter of this workflow':
      'このワークフローのパラメータではありません',
    'Default ({value})': '既定値（{value}）',
    'Default (not set)': '既定値（未設定）',
    'Default (computed when the run starts)': '既定値（実行開始時に計算）',
  },
  de: {
    Cron: 'Zeitplan (Cron)',
    'Cron, {count} schedule': 'Cron, {count} Zeitplan',
    'Cron, {count} schedules': 'Cron, {count} Zeitpläne',
    'Always on': 'Immer an',
    'Cron or Always on': 'Cron oder Immer an',
    'Not set up. Choose a cron schedule or Always on.':
      'Nicht eingerichtet. Einen Cron-Zeitplan oder „Immer an“ wählen.',
    'Choose how the workflow starts': 'Wählen, wie der Ablauf startet',
    'Start on a schedule of cron expressions.':
      'Nach einem Zeitplan aus Cron-Ausdrücken starten.',
    'Keep one run alive and start a new one when it ends.':
      'Einen Lauf am Leben halten und einen neuen starten, wenn er endet.',
    'Switch to Always on': 'Zu „Immer an“ wechseln',
    'Switch to Cron': 'Zu Cron wechseln',
    'Edit the schedule in the YAML to switch to Always on.':
      'Für den Wechsel zu „Immer an“ den Zeitplan im YAML bearbeiten.',
    'Not set up': 'Nicht eingerichtet',
    Launcher: 'Starter',
    'Manual launch is always active': 'Manueller Start ist immer möglich',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      'Mit dem Start-Knopf in der Leiste den Ablauf jetzt ausführen. Der Lauf nutzt die Parameter aus dem Start-Dialog.',
    'This workflow has no parameters': 'Dieser Ablauf hat keine Parameter',
    'Cron expression': 'Cron-Ausdruck',
    'New cron expression': 'Neuer Cron-Ausdruck',
    'Add an expression': 'Ausdruck hinzufügen',
    'Add schedule': 'Zeitplan hinzufügen',
    'Use workflow default for {name}':
      'Standard des Ablaufs für {name} verwenden',
    'Remove schedule': 'Zeitplan entfernen',
    'Next runs:': 'Nächste Läufe:',
    'No preview for this expression': 'Keine Vorschau für diesen Ausdruck',
    Hourly: 'Stündlich',
    'Daily at 09:00': 'Täglich um 09:00',
    'Weekdays at 09:00': 'Montags bis freitags um 09:00',
    'Edit with the form': 'Im Formular bearbeiten',
    Frequency: 'Häufigkeit',
    'Choose a frequency': 'Häufigkeit wählen',
    'Custom expression': 'Eigener Ausdruck',
    'Every few minutes': 'Alle paar Minuten',
    Daily: 'Täglich',
    Weekly: 'Wöchentlich',
    Monthly: 'Monatlich',
    'Minutes between runs': 'Minuten zwischen Läufen',
    'Minute of the hour': 'Minute der Stunde',
    'Day of the month': 'Tag im Monat',
    'Time of day': 'Uhrzeit',
    'Days of the week': 'Wochentage',
    'Every minute': 'Jede Minute',
    'Every {n} minutes': 'Alle {n} Minuten',
    'Every hour at minute {minute}': 'Jede Stunde in Minute {minute}',
    'Every day at {time}': 'Jeden Tag um {time}',
    'Every week on {days} at {time}': 'Jede Woche am {days} um {time}',
    'Every month on day {day} at {time}': 'Jeden Monat am {day}. um {time}',
    'A month without day {day} has no run.':
      'Ein Monat ohne den {day}. hat keinen Lauf.',
    'Times are in the time zone of the scheduler ({tz}).':
      'Zeiten in der Zeitzone des Schedulers ({tz}).',
    'Workflow sections': 'Ablauf-Abschnitte',
    Available: 'Verfügbar',
    'Restarts after each run': 'Startet nach jedem Lauf neu',
    'Restarts unless stopped': 'Startet neu, außer nach einem Stopp',
    '{state}, wait {seconds} s': '{state}, Wartezeit {seconds} Sek.',
    'Wait {seconds} s': 'Wartezeit {seconds} Sek.',
    'Unless stopped, wait {seconds} s': 'Außer nach Stopp, {seconds} Sek.',
    'Next run in {seconds} s': 'Nächster Lauf in {seconds} Sek.',
    Starting: 'Startet',
    '{detail}, {live}': '{detail}, {live}',
    'next run in {seconds} s': 'nächster Lauf in {seconds} Sek.',
    starting: 'startet',
    'keeps {count} runs': 'behält {count} Läufe',
    'keeps {count} run': 'behält {count} Lauf',
    'keeps {count} runs, or as many as the base configuration sets':
      'behält {count} Läufe oder so viele, wie die Grundkonfiguration festlegt',
    'keeps runs for {count} days': 'behält Läufe {count} Tage lang',
    'keeps runs for {count} day': 'behält Läufe {count} Tag lang',
    '{state}, {history}': '{state}, {history}',
    'Restart policy': 'Neustart-Richtlinie',
    'Restart settings': 'Neustart-Einstellungen',
    Always: 'Immer',
    'Unless stopped': 'Außer nach Stopp',
    'A new run follows each run, whatever its status.':
      'Auf jeden Lauf folgt ein neuer Lauf, unabhängig von seinem Status.',
    'A new run follows each run, except a run that an operator stopped.':
      'Auf jeden Lauf folgt ein neuer Lauf, außer auf einen Lauf, den ein Operator gestoppt hat.',
    'The scheduler keeps one run of this workflow alive. It starts a new run when the last run ends.':
      'Der Scheduler hält einen Lauf dieses Ablaufs am Leben. Er startet einen neuen Lauf, wenn der letzte Lauf endet.',
    'Wait before a new run (s)': 'Wartezeit vor einem neuen Lauf (s)',
    'Runs kept in the history': 'Läufe im Verlauf',
    'With no value, the history keeps the latest 100 runs, or as many as the base configuration sets.':
      'Ohne Wert behält der Verlauf die letzten 100 Läufe oder so viele, wie die Grundkonfiguration festlegt.',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      'Zuerst die retry_policy des Ablaufs entfernen: Ein Ablauf, der immer an ist, kann keine haben.',
    'Workflow > {tab}': 'Ablauf > {tab}',
    'Does not start at {time}: {schedule} starts instead.':
      'Startet nicht um {time}: Stattdessen startet {schedule}.',
    'The workflow starts on each of these schedules.':
      'Der Ablauf startet zu jedem dieser Zeitpläne.',
    Schedules: 'Zeitpläne',
    'This expression is already in the schedule.':
      'Dieser Ausdruck steht schon im Zeitplan.',
    'Edit the timing of this entry in the YAML.':
      'Zeitangaben dieses Eintrags im YAML bearbeiten.',
    'Edit this entry and its parameters in the YAML.':
      'Diesen Eintrag und seine Parameter im YAML bearbeiten.',
    'Not a parameter of this workflow': 'Kein Parameter dieses Ablaufs',
    'Default ({value})': 'Standard ({value})',
    'Default (not set)': 'Standard (nicht gesetzt)',
    'Default (computed when the run starts)':
      'Standard (beim Start des Laufs berechnet)',
  },
  fr: {
    Cron: 'Cron',
    'Cron, {count} schedule': 'Cron, {count} planification',
    'Cron, {count} schedules': 'Cron, {count} planifications',
    'Always on': 'Toujours actif',
    'Cron or Always on': 'Cron ou Toujours actif',
    'Not set up. Choose a cron schedule or Always on.':
      'Non configuré. Choisissez une planification cron ou «\u00a0Toujours actif\u00a0».',
    'Choose how the workflow starts':
      'Choisir le mode de démarrage du workflow',
    'Start on a schedule of cron expressions.':
      'Démarrer selon une planification d’expressions cron.',
    'Keep one run alive and start a new one when it ends.':
      'Maintenir une exécution active et en démarrer une nouvelle quand elle se termine.',
    'Switch to Always on': 'Passer à «\u00a0Toujours actif\u00a0»',
    'Switch to Cron': 'Passer à Cron',
    'Edit the schedule in the YAML to switch to Always on.':
      'Modifiez la planification dans le YAML pour passer à «\u00a0Toujours actif\u00a0».',
    'Not set up': 'Non configuré',
    Launcher: 'Lanceur',
    'Manual launch is always active':
      'Le lancement manuel est toujours disponible',
    'Use the Start button of the bar to run this workflow now. The run takes the parameters that you give in the Start dialog.':
      'Utilisez le bouton «\u00a0Démarrer\u00a0» de la barre pour exécuter ce workflow maintenant. L’exécution utilise les paramètres que vous indiquez dans la boîte de dialogue de démarrage.',
    'This workflow has no parameters': 'Ce workflow n’a aucun paramètre',
    'Cron expression': 'Expression cron',
    'New cron expression': 'Nouvelle expression cron',
    'Add an expression': 'Ajouter une expression',
    'Add schedule': 'Ajouter une planification',
    'Use workflow default for {name}':
      'Utiliser la valeur par défaut du workflow pour {name}',
    'Remove schedule': 'Supprimer la planification',
    'Next runs:': 'Prochaines exécutions\u00a0:',
    'No preview for this expression': 'Aucun aperçu pour cette expression',
    Hourly: 'Toutes les heures',
    'Daily at 09:00': 'Tous les jours à 09:00',
    'Weekdays at 09:00': 'Du lundi au vendredi à 09:00',
    'Edit with the form': 'Modifier avec le formulaire',
    Frequency: 'Fréquence',
    'Choose a frequency': 'Choisir une fréquence',
    'Custom expression': 'Expression personnalisée',
    'Every few minutes': 'Toutes les X minutes',
    Daily: 'Tous les jours',
    Weekly: 'Toutes les semaines',
    Monthly: 'Tous les mois',
    'Minutes between runs': 'Minutes entre deux exécutions',
    'Minute of the hour': 'Minute dans l’heure',
    'Day of the month': 'Jour du mois',
    'Time of day': 'Heure',
    'Days of the week': 'Jours de la semaine',
    'Every minute': 'Toutes les minutes',
    'Every {n} minutes': 'Toutes les {n} minutes',
    'Every hour at minute {minute}': 'Toutes les heures à la minute {minute}',
    'Every day at {time}': 'Tous les jours à {time}',
    'Every week on {days} at {time}': 'Toutes les semaines ({days}) à {time}',
    'Every month on day {day} at {time}':
      'Tous les mois, le jour {day} à {time}',
    'A month without day {day} has no run.':
      'Aucune exécution les mois sans jour {day}.',
    'Times are in the time zone of the scheduler ({tz}).':
      'Les heures sont exprimées dans le fuseau horaire du planificateur ({tz}).',
    'Workflow sections': 'Sections du workflow',
    Available: 'Disponible',
    'Restarts after each run': 'Redémarre après chaque exécution',
    'Restarts unless stopped': 'Redémarre sauf après un arrêt manuel',
    '{state}, wait {seconds} s': '{state}, attente de {seconds}\u00a0s',
    'Wait {seconds} s': 'Attente de {seconds}\u00a0s',
    'Unless stopped, wait {seconds} s': 'Sauf arrêt manuel, {seconds}\u00a0s',
    'Next run in {seconds} s': 'Exécution dans {seconds}\u00a0s',
    Starting: 'Démarrage en cours',
    '{detail}, {live}': '{detail}, {live}',
    'next run in {seconds} s': 'prochaine exécution dans {seconds}\u00a0s',
    starting: 'démarrage en cours',
    'keeps {count} runs': 'exécutions conservées\u00a0: {count}',
    'keeps {count} run': 'conserve {count} exécution',
    'keeps {count} runs, or as many as the base configuration sets':
      'conserve {count} exécutions, ou le nombre fixé par la configuration de base',
    'keeps runs for {count} days': 'jours de conservation\u00a0: {count}',
    'keeps runs for {count} day':
      'conserve les exécutions pendant {count} jour',
    '{state}, {history}': '{state}, {history}',
    'Restart policy': 'Politique de redémarrage',
    'Restart settings': 'Réglages de redémarrage',
    Always: 'Toujours',
    'Unless stopped': 'Sauf arrêt manuel',
    'A new run follows each run, whatever its status.':
      'Une nouvelle exécution suit chaque exécution, quel que soit son statut.',
    'A new run follows each run, except a run that an operator stopped.':
      'Une nouvelle exécution suit chaque exécution, sauf celle qu’un opérateur a arrêtée.',
    'The scheduler keeps one run of this workflow alive. It starts a new run when the last run ends.':
      'Le planificateur maintient active une exécution de ce workflow. Il en démarre une nouvelle quand la dernière se termine.',
    'Wait before a new run (s)': 'Attente avant une nouvelle exécution (s)',
    'Runs kept in the history': 'Exécutions conservées dans l’historique',
    'With no value, the history keeps the latest 100 runs, or as many as the base configuration sets.':
      'Sans valeur, l’historique conserve les 100 dernières exécutions, ou le nombre fixé par la configuration de base.',
    'Remove the retry_policy of the workflow first: a workflow that is always on cannot have one.':
      'Supprimez d’abord la retry_policy du workflow\u00a0: un workflow toujours actif ne peut pas en avoir.',
    'Workflow > {tab}': 'Workflow > {tab}',
    'Does not start at {time}: {schedule} starts instead.':
      'Ne démarre pas le {time}\u00a0: la planification {schedule} démarre à la place.',
    'The workflow starts on each of these schedules.':
      'Le workflow démarre selon chacune de ces planifications.',
    Schedules: 'Planifications',
    'This expression is already in the schedule.':
      'Cette expression figure déjà dans la planification.',
    'Edit the timing of this entry in the YAML.':
      'Modifiez l’horaire de cette entrée dans le YAML.',
    'Edit this entry and its parameters in the YAML.':
      'Modifiez cette entrée et ses paramètres dans le YAML.',
    'Not a parameter of this workflow':
      'Ce n’est pas un paramètre de ce workflow',
    'Default ({value})': 'Par défaut ({value})',
    'Default (not set)': 'Par défaut (non défini)',
    'Default (computed when the run starts)':
      'Par défaut (calculé au démarrage de l’exécution)',
  },
});
