// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from '@/features/workflow-workspace/messages/define';

/** The static messages of the backfill pages and panels. */
export const backfillMessages = defineMessages({
  en: {
    Backfills: 'Backfills',
    'Backfill {id}': 'Backfill {id}',
    'Running: {done} of {total} slots done':
      'Running: {done} of {total} slots done',
    'Running: {done} of {total} slots done, {failed} failed':
      'Running: {done} of {total} slots done, {failed} failed',
    'Finishing: all {total} slots are done':
      'Finishing: all {total} slots are done',
    'Waiting for a scheduler: {done} of {total} slots done':
      'Waiting for a scheduler: {done} of {total} slots done',
    'No scheduler is running, so the backfill queues no run.':
      'No scheduler is running, so the backfill queues no run.',
    'Succeeded: all {total} slots': 'Succeeded: all {total} slots',
    'Succeeded: {ran} slots ran, {skipped} skipped':
      'Succeeded: {ran} slots ran, {skipped} skipped',
    'Failed: {failed} of {total} slots failed':
      'Failed: {failed} of {total} slots failed',
    'Stopped: {failed} failed, {notRun} not run':
      'Stopped: {failed} failed, {notRun} not run',
    'Cancelled: {cancelled} slots were not run':
      'Cancelled: {cancelled} slots were not run',
    'Not run': 'Not run',
    Cancelled: 'Cancelled',
    Slots: 'Slots',
    'Run past slots of the schedule': 'Run past slots of the schedule',
    'Could not read the last success: {message}':
      'Could not read the last success: {message}',
    'Since the last success': 'Since the last success',
    'Since the last success, {time}': 'Since the last success, {time}',
    'Fire times': 'Fire times',
    'No slot succeeded in the last {days} days.':
      'No slot succeeded in the last {days} days.',
    'First fire time': 'First fire time',
    'Last fire time': 'Last fire time',
    'Times are in {zone}.': 'Times are in {zone}.',
    Options: 'Options',
    '{count} at a time, stop after a failed slot':
      '{count} at a time, stop after a failed slot',
    '{count} at a time, continue after a failed slot':
      '{count} at a time, continue after a failed slot',
    "The DAG's default profile": "The DAG's default profile",
    '{name} (protected)': '{name} (protected)',
    '{name} (admin only)': '{name} (admin only)',
    'No slots in this state.': 'No slots in this state.',
    'Slot {time}': 'Slot {time}',
    'Older definition': 'Older definition',
    'Backfill {done}/{total}': 'Backfill {done}/{total}',
    'Backfill {done}/{total}, {failed} failed':
      'Backfill {done}/{total}, {failed} failed',
    'Backfill failed: {failed} slots': 'Backfill failed: {failed} slots',
    'Page {page} of {pages}': 'Page {page} of {pages}',
    'Submitted {time}': 'Submitted {time}',
    'Submitted {time} by {actor}': 'Submitted {time} by {actor}',
    'Resumed once': 'Resumed once',
    'Resumed {count} times': 'Resumed {count} times',
    'Fill gaps': 'Fill gaps',
    'Fill gaps and redo failed': 'Fill gaps and redo failed',
    'Redo everything': 'Redo everything',
    'Named slots': 'Named slots',
    '{count} at a time': '{count} at a time',
    'Stops after a slot fails': 'Stops after a slot fails',
    'Goes on after a slot fails': 'Goes on after a slot fails',
    'View runs': 'View runs',
    'Open backfill': 'Open backfill',
    'Open workflow': 'Open workflow',
    'Cancel backfill': 'Cancel backfill',
    'Cancel the backfill?': 'Cancel the backfill?',
    'Its queued runs are withdrawn and the slots it has not reached are cancelled. Runs in progress finish unless you stop them.':
      'Its queued runs are withdrawn and the slots it has not reached are cancelled. Runs in progress finish unless you stop them.',
    'Also stop the run in progress': 'Also stop the run in progress',
    'Also stop the {count} runs in progress':
      'Also stop the {count} runs in progress',
    'Keep running': 'Keep running',
    'Resume backfill': 'Resume backfill',
    'Runs again the slots that did not succeed, with the current definition of the DAG and new runs.':
      'Runs again the slots that did not succeed, with the current definition of the DAG and new runs.',
    'Runs at once': 'Runs at once',
    'The queue runs {limit} at a time.': 'The queue runs {limit} at a time.',
    'After a slot fails': 'After a slot fails',
    'Stop: for loads that build on the slot before':
      'Stop: for loads that build on the slot before',
    'Continue: for slots that do not depend on each other':
      'Continue: for slots that do not depend on each other',
    'Planning…': 'Planning…',
    'Nothing to resume: every slot succeeded or was skipped.':
      'Nothing to resume: every slot succeeded or was skipped.',
    'The plan changed. Review it and resume again.':
      'The plan changed. Review it and resume again.',
    'Resume 1 run': 'Resume 1 run',
    'Resume {count} runs': 'Resume {count} runs',
    'About {duration}': 'About {duration}',
    'Backfill running: {done} of {total} slots done':
      'Backfill running: {done} of {total} slots done',
    '{count} slots passed from {from} to {to} while {why}':
      '{count} slots passed from {from} to {to} while {why}',
    '1 slot passed at {at} while {why}': '1 slot passed at {at} while {why}',
    'this DAG was suspended': 'this DAG was suspended',
    'the scheduler was paused': 'the scheduler was paused',
    'the scheduler was down': 'the scheduler was down',
    'this DAG could not run': 'this DAG could not run',
    'The missed slots were not dismissed.':
      'The missed slots were not dismissed.',
    'Please try again or check the server connection.':
      'Please try again or check the server connection.',
    'Backfill running: {done} of {total} slots done, {failed} failed':
      'Backfill running: {done} of {total} slots done, {failed} failed',
    'Backfill failed: {failed} slots did not succeed':
      'Backfill failed: {failed} slots did not succeed',
    'No backfills yet.': 'No backfills yet.',
    'A backfill runs the past slots of this schedule.':
      'A backfill runs the past slots of this schedule.',
    'A backfill runs the past slots of this schedule. Start one from the slots above.':
      'A backfill runs the past slots of this schedule. Start one from the slots above.',
    'Backfill not found': 'Backfill not found',
    'Failed to load the backfill': 'Failed to load the backfill',
    'Failed to load the backfills': 'Failed to load the backfills',
    'Slot states': 'Slot states',
    'Failed to load the slots': 'Failed to load the slots',
    'Last 90 days': 'Last 90 days',
    Range: 'Range',
    'The schedule fires no slot in this range.':
      'The schedule fires no slot in this range.',
    'Showing the newest {shown} of {total} slots. Pick a shorter range to see the rest.':
      'Showing the newest {shown} of {total} slots. Pick a shorter range to see the rest.',
    'Pick missing': 'Pick missing',
    'Pick missing and failed': 'Pick missing and failed',
    'Backfill 1 slot': 'Backfill 1 slot',
    'Backfill {count} slots': 'Backfill {count} slots',
    'Backfill this range': 'Backfill this range',
    'Runs the {count} slots you picked, oldest first.':
      'Runs the {count} slots you picked, oldest first.',
    'Runs the past slots of the range, oldest first.':
      'Runs the past slots of the range, oldest first.',
    'What to run': 'What to run',
    'Fix the highlighted params before starting.':
      'Fix the highlighted params before starting.',
    'Nothing to run: every slot is done or held.':
      'Nothing to run: every slot is done or held.',
    'Slots {from} to {to}': 'Slots {from} to {to}',
    'Loads {from} to {to} ({entry})': 'Loads {from} to {to} ({entry})',
    'Run 1 slot that succeeded again': 'Run 1 slot that succeeded again',
    'Run {count} slots that succeeded again':
      'Run {count} slots that succeeded again',
    'The plan changed. Review it and start again.':
      'The plan changed. Review it and start again.',
    'Failed to plan the backfill': 'Failed to plan the backfill',
    'Failed to start the backfill': 'Failed to start the backfill',
    'Start 1 run': 'Start 1 run',
    'Start {count} runs': 'Start {count} runs',
    'Show backfill runs': 'Show backfill runs',
    'Runs of backfill {id}': 'Runs of backfill {id}',
    'Show all runs': 'Show all runs',
    'Runs 1 slot': 'Runs 1 slot',
    'Runs {count} slots': 'Runs {count} slots',
    'Failed to cancel the backfill': 'Failed to cancel the backfill',
    'Failed to resume the backfill': 'Failed to resume the backfill',
  },
  'zh-CN': {
    Backfills: '回填',
    'Backfill {id}': '回填 {id}',
    'Running: {done} of {total} slots done':
      '运行中：{total} 个时段中已完成 {done} 个',
    'Running: {done} of {total} slots done, {failed} failed':
      '运行中：{total} 个时段中已完成 {done} 个，{failed} 个失败',
    'Finishing: all {total} slots are done':
      '即将结束：全部 {total} 个时段已完成',
    'Waiting for a scheduler: {done} of {total} slots done':
      '等待调度器：{total} 个时段中已完成 {done} 个',
    'No scheduler is running, so the backfill queues no run.':
      '没有正在运行的调度器，因此回填不会将任何运行加入队列。',
    'Succeeded: all {total} slots': '成功：全部 {total} 个时段',
    'Succeeded: {ran} slots ran, {skipped} skipped':
      '成功：运行了 {ran} 个时段，跳过 {skipped} 个',
    'Failed: {failed} of {total} slots failed':
      '失败：{total} 个时段中有 {failed} 个失败',
    'Stopped: {failed} failed, {notRun} not run':
      '已停止：{failed} 个失败，{notRun} 个未运行',
    'Cancelled: {cancelled} slots were not run':
      '已取消：{cancelled} 个时段未运行',
    'Not run': '未运行',
    Cancelled: '已取消',
    Slots: '时段',
    'Run past slots of the schedule': '运行计划中过去的时段',
    'Could not read the last success: {message}': '无法读取上次成功：{message}',
    'Since the last success': '自上次成功以来',
    'Since the last success, {time}': '自上次成功以来（{time}）',
    'Fire times': '触发时间',
    'No slot succeeded in the last {days} days.':
      '最近 {days} 天内没有成功的时段。',
    'First fire time': '起始触发时间',
    'Last fire time': '结束触发时间',
    'Times are in {zone}.': '时间按 {zone} 计。',
    Options: '选项',
    '{count} at a time, stop after a failed slot':
      '每次 {count} 个，时段失败后停止',
    '{count} at a time, continue after a failed slot':
      '每次 {count} 个，时段失败后继续',
    "The DAG's default profile": 'DAG 的默认配置',
    '{name} (protected)': '{name}（受保护）',
    '{name} (admin only)': '{name}（仅管理员）',
    'No slots in this state.': '没有处于此状态的时段。',
    'Slot {time}': '时段 {time}',
    'Older definition': '较早的定义',
    'Backfill {done}/{total}': '回填 {done}/{total}',
    'Backfill {done}/{total}, {failed} failed':
      '回填 {done}/{total}，{failed} 个失败',
    'Backfill failed: {failed} slots': '回填失败：{failed} 个时段',
    'Page {page} of {pages}': '第 {page} 页，共 {pages} 页',
    'Submitted {time}': '提交于 {time}',
    'Submitted {time} by {actor}': '由 {actor} 提交于 {time}',
    'Resumed once': '已恢复 1 次',
    'Resumed {count} times': '已恢复 {count} 次',
    'Fill gaps': '填补空缺',
    'Fill gaps and redo failed': '填补空缺并重做失败的时段',
    'Redo everything': '全部重做',
    'Named slots': '指定的时段',
    '{count} at a time': '每次 {count} 个',
    'Stops after a slot fails': '某个时段失败后停止',
    'Goes on after a slot fails': '某个时段失败后继续',
    'View runs': '查看运行',
    'Open backfill': '打开回填',
    'Open workflow': '打开工作流',
    'Cancel backfill': '取消回填',
    'Cancel the backfill?': '要取消回填吗？',
    'Its queued runs are withdrawn and the slots it has not reached are cancelled. Runs in progress finish unless you stop them.':
      '已排队的运行将被撤回，尚未处理的时段将被取消。正在进行的运行会继续完成，除非你停止它们。',
    'Also stop the run in progress': '同时停止正在进行的运行',
    'Also stop the {count} runs in progress':
      '同时停止 {count} 个正在进行的运行',
    'Keep running': '继续运行',
    'Resume backfill': '恢复回填',
    'Runs again the slots that did not succeed, with the current definition of the DAG and new runs.':
      '使用 DAG 的当前定义，以新的运行重新运行未成功的时段。',
    'Runs at once': '同时运行数',
    'The queue runs {limit} at a time.': '队列每次运行 {limit} 个。',
    'After a slot fails': '某个时段失败后',
    'Stop: for loads that build on the slot before':
      '停止：适用于依赖前一个时段的加载',
    'Continue: for slots that do not depend on each other':
      '继续：适用于彼此独立的时段',
    'Planning…': '正在规划…',
    'Nothing to resume: every slot succeeded or was skipped.':
      '无需恢复：所有时段均已成功或被跳过。',
    'The plan changed. Review it and resume again.':
      '计划已更改。请检查后再次恢复。',
    'Resume 1 run': '恢复 1 个运行',
    'Resume {count} runs': '恢复 {count} 个运行',
    'About {duration}': '约 {duration}',
    'Backfill running: {done} of {total} slots done':
      '回填运行中：{total} 个时段中已完成 {done} 个',
    '{count} slots passed from {from} to {to} while {why}':
      '{why}期间（{from} 至 {to}）错过了 {count} 个时段',
    '1 slot passed at {at} while {why}': '{why}期间错过了 {at} 的 1 个时段',
    'this DAG was suspended': 'DAG 暂停',
    'the scheduler was paused': '调度器暂停',
    'the scheduler was down': '调度器停止',
    'this DAG could not run': 'DAG 无法运行',
    'The missed slots were not dismissed.': '未能忽略错过的时间槽。',
    'Please try again or check the server connection.':
      '请重试，或检查与服务器的连接。',
    'Backfill running: {done} of {total} slots done, {failed} failed':
      '回填运行中：{total} 个时段中已完成 {done} 个，{failed} 个失败',
    'Backfill failed: {failed} slots did not succeed':
      '回填失败：{failed} 个时段未成功',
    'No backfills yet.': '还没有回填。',
    'A backfill runs the past slots of this schedule.':
      '回填会运行此计划过去的时段。',
    'A backfill runs the past slots of this schedule. Start one from the slots above.':
      '回填会运行此计划过去的时段。可从上方的时段开始回填。',
    'Backfill not found': '未找到回填',
    'Failed to load the backfill': '加载回填失败',
    'Failed to load the backfills': '加载回填列表失败',
    'Slot states': '时段状态',
    'Failed to load the slots': '加载时段失败',
    'Last 90 days': '过去 90 天',
    Range: '范围',
    'The schedule fires no slot in this range.': '该计划在此范围内没有时段。',
    'Showing the newest {shown} of {total} slots. Pick a shorter range to see the rest.':
      '显示 {total} 个时段中最新的 {shown} 个。选择更短的范围以查看其余时段。',
    'Pick missing': '选择缺失的',
    'Pick missing and failed': '选择缺失和失败的',
    'Backfill 1 slot': '回填 1 个时段',
    'Backfill {count} slots': '回填 {count} 个时段',
    'Backfill this range': '回填此范围',
    'Runs the {count} slots you picked, oldest first.':
      '从最早的开始，运行你选择的 {count} 个时段。',
    'Runs the past slots of the range, oldest first.':
      '从最早的开始，运行该范围内过去的时段。',
    'What to run': '运行内容',
    'Fix the highlighted params before starting.': '请先修正标出的参数再开始。',
    'Nothing to run: every slot is done or held.':
      '没有要运行的内容：所有时段均已完成或被占用。',
    'Slots {from} to {to}': '时段 {from} 至 {to}',
    'Loads {from} to {to} ({entry})': '加载 {from} 至 {to}（{entry}）',
    'Run 1 slot that succeeded again': '重新运行 1 个已成功的时段',
    'Run {count} slots that succeeded again': '重新运行 {count} 个已成功的时段',
    'The plan changed. Review it and start again.':
      '计划已更改。请检查后再次开始。',
    'Failed to plan the backfill': '规划回填失败',
    'Failed to start the backfill': '启动回填失败',
    'Start 1 run': '启动 1 个运行',
    'Start {count} runs': '启动 {count} 个运行',
    'Show backfill runs': '显示回填运行',
    'Runs of backfill {id}': '回填 {id} 的运行',
    'Show all runs': '显示全部运行',
    'Runs 1 slot': '运行 1 个时段',
    'Runs {count} slots': '运行 {count} 个时段',
    'Failed to cancel the backfill': '取消回填失败',
    'Failed to resume the backfill': '恢复回填失败',
  },
  ja: {
    Backfills: 'バックフィル',
    'Backfill {id}': 'バックフィル {id}',
    'Running: {done} of {total} slots done':
      '実行中: {total} 件のスロットのうち {done} 件が完了',
    'Running: {done} of {total} slots done, {failed} failed':
      '実行中: {total} 件のスロットのうち {done} 件が完了、{failed} 件が失敗',
    'Finishing: all {total} slots are done':
      '終了処理中: {total} 件すべてのスロットが完了',
    'Waiting for a scheduler: {done} of {total} slots done':
      'スケジューラー待ち: {total} 件のスロットのうち {done} 件が完了',
    'No scheduler is running, so the backfill queues no run.':
      'スケジューラーが動いていないため、バックフィルは実行をキューに入れません。',
    'Succeeded: all {total} slots': '成功: {total} 件すべてのスロット',
    'Succeeded: {ran} slots ran, {skipped} skipped':
      '成功: {ran} 件のスロットを実行、{skipped} 件をスキップ',
    'Failed: {failed} of {total} slots failed':
      '失敗: {total} 件のスロットのうち {failed} 件が失敗',
    'Stopped: {failed} failed, {notRun} not run':
      '停止: {failed} 件が失敗、{notRun} 件が未実行',
    'Cancelled: {cancelled} slots were not run':
      'キャンセル: {cancelled} 件のスロットは実行されませんでした',
    'Not run': '未実行',
    Cancelled: 'キャンセル済み',
    Slots: 'スロット',
    'Run past slots of the schedule': 'スケジュールの過去のスロットを実行',
    'Could not read the last success: {message}':
      '最後の成功を読み込めませんでした: {message}',
    'Since the last success': '最後の成功以降',
    'Since the last success, {time}': '最後の成功以降（{time}）',
    'Fire times': '実行時刻',
    'No slot succeeded in the last {days} days.':
      '過去 {days} 日間に成功したスロットはありません。',
    'First fire time': '最初の実行時刻',
    'Last fire time': '最後の実行時刻',
    'Times are in {zone}.': '時刻は {zone} です。',
    Options: 'オプション',
    '{count} at a time, stop after a failed slot':
      '同時に {count} 件、スロットが失敗したら停止',
    '{count} at a time, continue after a failed slot':
      '同時に {count} 件、スロットが失敗しても続行',
    "The DAG's default profile": 'DAG の既定のプロファイル',
    '{name} (protected)': '{name}（保護済み）',
    '{name} (admin only)': '{name}（管理者のみ）',
    'No slots in this state.': 'この状態のスロットはありません。',
    'Slot {time}': 'スロット {time}',
    'Older definition': '以前の定義',
    'Backfill {done}/{total}': 'バックフィル {done}/{total}',
    'Backfill {done}/{total}, {failed} failed':
      'バックフィル {done}/{total}、失敗 {failed}',
    'Backfill failed: {failed} slots': 'バックフィル失敗: {failed} スロット',
    'Page {page} of {pages}': '{pages} ページ中 {page} ページ',
    'Submitted {time}': '{time} に開始',
    'Submitted {time} by {actor}': '{actor} が {time} に開始',
    'Resumed once': '1 回再開',
    'Resumed {count} times': '{count} 回再開',
    'Fill gaps': '欠けたスロットを埋める',
    'Fill gaps and redo failed': '欠けたスロットを埋め、失敗をやり直す',
    'Redo everything': 'すべてやり直す',
    'Named slots': '指定したスロット',
    '{count} at a time': '同時に {count} 件',
    'Stops after a slot fails': 'スロットが失敗したら停止',
    'Goes on after a slot fails': 'スロットが失敗しても続行',
    'View runs': '実行を表示',
    'Open backfill': 'バックフィルを開く',
    'Open workflow': 'ワークフローを開く',
    'Cancel backfill': 'バックフィルをキャンセル',
    'Cancel the backfill?': 'バックフィルをキャンセルしますか？',
    'Its queued runs are withdrawn and the slots it has not reached are cancelled. Runs in progress finish unless you stop them.':
      'キュー内の実行は取り消され、まだ処理していないスロットはキャンセルされます。実行中のものは、停止しない限り最後まで実行されます。',
    'Also stop the run in progress': '実行中の実行も停止する',
    'Also stop the {count} runs in progress': '実行中の {count} 件も停止する',
    'Keep running': '続ける',
    'Resume backfill': 'バックフィルを再開',
    'Runs again the slots that did not succeed, with the current definition of the DAG and new runs.':
      '成功しなかったスロットを、DAG の現在の定義と新しい実行でもう一度実行します。',
    'Runs at once': '同時実行数',
    'The queue runs {limit} at a time.':
      'キューは同時に {limit} 件を実行します。',
    'After a slot fails': 'スロットが失敗したとき',
    'Stop: for loads that build on the slot before':
      '停止: 前のスロットを前提とするロード向け',
    'Continue: for slots that do not depend on each other':
      '続行: 互いに独立したスロット向け',
    'Planning…': '計画中…',
    'Nothing to resume: every slot succeeded or was skipped.':
      '再開するものはありません: すべてのスロットが成功したかスキップされました。',
    'The plan changed. Review it and resume again.':
      '計画が変わりました。確認してからもう一度再開してください。',
    'Resume 1 run': '1 件の実行を再開',
    'Resume {count} runs': '{count} 件の実行を再開',
    'About {duration}': '約 {duration}',
    'Backfill running: {done} of {total} slots done':
      'バックフィル実行中: {total} 件のスロットのうち {done} 件が完了',
    '{count} slots passed from {from} to {to} while {why}':
      '{why}（{from}〜{to}）に {count} スロットが過ぎました',
    '1 slot passed at {at} while {why}':
      '{why}に {at} の 1 スロットが過ぎました',
    'this DAG was suspended': 'DAG の一時停止中',
    'the scheduler was paused': 'スケジューラーの一時停止中',
    'the scheduler was down': 'スケジューラーの停止中',
    'this DAG could not run': 'DAG が実行できない間',
    'The missed slots were not dismissed.':
      '見逃したスロットを非表示にできませんでした。',
    'Please try again or check the server connection.':
      'もう一度試すか、サーバーとの接続を確認してください。',
    'Backfill running: {done} of {total} slots done, {failed} failed':
      'バックフィル実行中: {total} 件のスロットのうち {done} 件が完了、{failed} 件が失敗',
    'Backfill failed: {failed} slots did not succeed':
      'バックフィル失敗: {failed} 件のスロットが成功しませんでした',
    'No backfills yet.': 'バックフィルはまだありません。',
    'A backfill runs the past slots of this schedule.':
      'バックフィルは、このスケジュールの過去のスロットを実行します。',
    'A backfill runs the past slots of this schedule. Start one from the slots above.':
      'バックフィルは、このスケジュールの過去のスロットを実行します。上のスロットから開始できます。',
    'Backfill not found': 'バックフィルが見つかりません',
    'Failed to load the backfill': 'バックフィルを読み込めませんでした',
    'Failed to load the backfills': 'バックフィルの一覧を読み込めませんでした',
    'Slot states': 'スロットの状態',
    'Failed to load the slots': 'スロットを読み込めませんでした',
    'Last 90 days': '過去 90 日間',
    Range: '範囲',
    'The schedule fires no slot in this range.':
      'この範囲にスケジュールのスロットはありません。',
    'Showing the newest {shown} of {total} slots. Pick a shorter range to see the rest.':
      '{total} 件のスロットのうち新しい {shown} 件を表示しています。残りを見るには範囲を短くしてください。',
    'Pick missing': '欠けているものを選択',
    'Pick missing and failed': '欠けているものと失敗を選択',
    'Backfill 1 slot': '1 件のスロットをバックフィル',
    'Backfill {count} slots': '{count} 件のスロットをバックフィル',
    'Backfill this range': 'この範囲をバックフィル',
    'Runs the {count} slots you picked, oldest first.':
      '選んだ {count} 件のスロットを古い順に実行します。',
    'Runs the past slots of the range, oldest first.':
      '範囲内の過去のスロットを古い順に実行します。',
    'What to run': '実行するもの',
    'Fix the highlighted params before starting.':
      '開始する前に、示されたパラメーターを直してください。',
    'Nothing to run: every slot is done or held.':
      '実行するものはありません: すべてのスロットが完了済みか保留中です。',
    'Slots {from} to {to}': 'スロット {from} から {to}',
    'Loads {from} to {to} ({entry})': '{from} から {to} を読み込み（{entry}）',
    'Run 1 slot that succeeded again':
      '成功した 1 件のスロットをもう一度実行する',
    'Run {count} slots that succeeded again':
      '成功した {count} 件のスロットをもう一度実行する',
    'The plan changed. Review it and start again.':
      '計画が変わりました。確認してからもう一度開始してください。',
    'Failed to plan the backfill': 'バックフィルを計画できませんでした',
    'Failed to start the backfill': 'バックフィルを開始できませんでした',
    'Start 1 run': '1 件の実行を開始',
    'Start {count} runs': '{count} 件の実行を開始',
    'Show backfill runs': 'バックフィルの実行を表示',
    'Runs of backfill {id}': 'バックフィル {id} の実行',
    'Show all runs': 'すべての実行を表示',
    'Runs 1 slot': '1 件のスロットを実行',
    'Runs {count} slots': '{count} 件のスロットを実行',
    'Failed to cancel the backfill': 'バックフィルをキャンセルできませんでした',
    'Failed to resume the backfill': 'バックフィルを再開できませんでした',
  },
  de: {
    Backfills: 'Nachholen',
    'Backfill {id}': 'Nachholen {id}',
    'Running: {done} of {total} slots done':
      'Läuft: {done} von {total} Terminen erledigt',
    'Running: {done} of {total} slots done, {failed} failed':
      'Läuft: {done} von {total} Terminen erledigt, {failed} fehlgeschlagen',
    'Finishing: all {total} slots are done':
      'Abschluss: alle {total} Termine erledigt',
    'Waiting for a scheduler: {done} of {total} slots done':
      'Wartet auf einen Scheduler: {done} von {total} Terminen erledigt',
    'No scheduler is running, so the backfill queues no run.':
      'Kein Scheduler läuft, daher stellt das Nachholen keinen Lauf ein.',
    'Succeeded: all {total} slots': 'Erfolgreich: alle {total} Termine',
    'Succeeded: {ran} slots ran, {skipped} skipped':
      'Erfolgreich: {ran} Termine gelaufen, {skipped} übersprungen',
    'Failed: {failed} of {total} slots failed':
      'Fehlgeschlagen: {failed} von {total} Terminen',
    'Stopped: {failed} failed, {notRun} not run':
      'Gestoppt: {failed} fehlgeschlagen, {notRun} nicht gelaufen',
    'Cancelled: {cancelled} slots were not run':
      'Abgebrochen: {cancelled} Termine nicht ausgeführt',
    'Not run': 'Nicht gelaufen',
    Cancelled: 'Abgebrochen',
    Slots: 'Termine',
    'Run past slots of the schedule':
      'Vergangene Termine des Zeitplans ausführen',
    'Could not read the last success: {message}':
      'Letzter Erfolg nicht lesbar: {message}',
    'Since the last success': 'Seit dem letzten Erfolg',
    'Since the last success, {time}': 'Seit dem letzten Erfolg, {time}',
    'Fire times': 'Startzeiten',
    'No slot succeeded in the last {days} days.':
      'In den letzten {days} Tagen war kein Termin erfolgreich.',
    'First fire time': 'Erste Startzeit',
    'Last fire time': 'Letzte Startzeit',
    'Times are in {zone}.': 'Zeiten in {zone}.',
    Options: 'Optionen',
    '{count} at a time, stop after a failed slot':
      '{count} gleichzeitig, nach Fehler anhalten',
    '{count} at a time, continue after a failed slot':
      '{count} gleichzeitig, nach Fehler weitermachen',
    "The DAG's default profile": 'Standardprofil des DAG',
    '{name} (protected)': '{name} (geschützt)',
    '{name} (admin only)': '{name} (nur Admin)',
    'No slots in this state.': 'Keine Termine in diesem Zustand.',
    'Slot {time}': 'Termin {time}',
    'Older definition': 'Ältere Definition',
    'Backfill {done}/{total}': 'Nachholen {done}/{total}',
    'Backfill {done}/{total}, {failed} failed':
      'Nachholen {done}/{total}, {failed} fehlgeschlagen',
    'Backfill failed: {failed} slots':
      'Nachholen fehlgeschlagen: {failed} Termine',
    'Page {page} of {pages}': 'Seite {page} von {pages}',
    'Submitted {time}': 'Eingereicht {time}',
    'Submitted {time} by {actor}': 'Eingereicht {time} von {actor}',
    'Resumed once': 'Einmal fortgesetzt',
    'Resumed {count} times': '{count}-mal fortgesetzt',
    'Fill gaps': 'Lücken füllen',
    'Fill gaps and redo failed':
      'Lücken füllen und Fehlgeschlagene wiederholen',
    'Redo everything': 'Alles erneut ausführen',
    'Named slots': 'Benannte Termine',
    '{count} at a time': '{count} gleichzeitig',
    'Stops after a slot fails': 'Hält nach einem fehlgeschlagenen Termin an',
    'Goes on after a slot fails':
      'Macht nach einem fehlgeschlagenen Termin weiter',
    'View runs': 'Läufe ansehen',
    'Open backfill': 'Nachholen öffnen',
    'Open workflow': 'Ablauf öffnen',
    'Cancel backfill': 'Nachholen abbrechen',
    'Cancel the backfill?': 'Nachholen abbrechen?',
    'Its queued runs are withdrawn and the slots it has not reached are cancelled. Runs in progress finish unless you stop them.':
      'Seine wartenden Läufe werden zurückgezogen und noch nicht erreichte Termine abgebrochen. Laufende Läufe enden normal, außer du stoppst sie.',
    'Also stop the run in progress': 'Auch den laufenden Lauf stoppen',
    'Also stop the {count} runs in progress':
      'Auch die {count} laufenden Läufe stoppen',
    'Keep running': 'Weiterlaufen lassen',
    'Resume backfill': 'Nachholen fortsetzen',
    'Runs again the slots that did not succeed, with the current definition of the DAG and new runs.':
      'Führt die nicht erfolgreichen Termine erneut aus, mit der aktuellen Definition und neuen Läufen.',
    'Runs at once': 'Läufe gleichzeitig',
    'The queue runs {limit} at a time.':
      'Die Warteschlange führt {limit} gleichzeitig aus.',
    'After a slot fails': 'Nach einem fehlgeschlagenen Termin',
    'Stop: for loads that build on the slot before':
      'Anhalten: für Ladevorgänge, die auf dem vorherigen Termin aufbauen',
    'Continue: for slots that do not depend on each other':
      'Weitermachen: für Termine, die nicht voneinander abhängen',
    'Planning…': 'Wird geplant…',
    'Nothing to resume: every slot succeeded or was skipped.':
      'Nichts fortzusetzen: Jeder Termin war erfolgreich oder wurde übersprungen.',
    'The plan changed. Review it and resume again.':
      'Der Plan hat sich geändert. Bitte prüfen und erneut fortsetzen.',
    'Resume 1 run': '1 Lauf fortsetzen',
    'Resume {count} runs': '{count} Läufe fortsetzen',
    'About {duration}': 'Etwa {duration}',
    'Backfill running: {done} of {total} slots done':
      'Nachholen läuft: {done} von {total} Terminen erledigt',
    '{count} slots passed from {from} to {to} while {why}':
      '{count} Termine verstrichen von {from} bis {to}, während {why}',
    '1 slot passed at {at} while {why}':
      '1 Termin verstrich um {at}, während {why}',
    'this DAG was suspended': 'dieser DAG angehalten war',
    'the scheduler was paused': 'der Scheduler pausiert war',
    'the scheduler was down': 'der Scheduler aus war',
    'this DAG could not run': 'dieser DAG nicht laufen konnte',
    'The missed slots were not dismissed.':
      'Die verpassten Termine wurden nicht verworfen.',
    'Please try again or check the server connection.':
      'Bitte erneut versuchen oder Serververbindung prüfen.',
    'Backfill running: {done} of {total} slots done, {failed} failed':
      'Nachholen läuft: {done} von {total} Terminen erledigt, {failed} fehlgeschlagen',
    'Backfill failed: {failed} slots did not succeed':
      'Nachholen fehlgeschlagen: {failed} Termine nicht erfolgreich',
    'No backfills yet.': 'Noch nichts nachgeholt.',
    'A backfill runs the past slots of this schedule.':
      'Nachholen führt die vergangenen Termine dieses Zeitplans aus.',
    'A backfill runs the past slots of this schedule. Start one from the slots above.':
      'Nachholen führt die vergangenen Termine dieses Zeitplans aus. Starte es über die Termine oben.',
    'Backfill not found': 'Nachholen nicht gefunden',
    'Failed to load the backfill': 'Nachholen konnte nicht geladen werden',
    'Failed to load the backfills':
      'Nachholvorgänge konnten nicht geladen werden',
    'Slot states': 'Terminzustände',
    'Failed to load the slots': 'Termine konnten nicht geladen werden',
    'Last 90 days': 'Letzte 90 Tage',
    Range: 'Zeitraum',
    'The schedule fires no slot in this range.':
      'Der Zeitplan hat in diesem Zeitraum keinen Termin.',
    'Showing the newest {shown} of {total} slots. Pick a shorter range to see the rest.':
      'Die neuesten {shown} von {total} Terminen werden angezeigt. Für den Rest kürzeren Zeitraum wählen.',
    'Pick missing': 'Fehlende wählen',
    'Pick missing and failed': 'Fehlende und fehlgeschlagene wählen',
    'Backfill 1 slot': '1 Termin nachholen',
    'Backfill {count} slots': '{count} Termine nachholen',
    'Backfill this range': 'Diesen Zeitraum nachholen',
    'Runs the {count} slots you picked, oldest first.':
      'Führt die {count} gewählten Termine aus, älteste zuerst.',
    'Runs the past slots of the range, oldest first.':
      'Führt die vergangenen Termine des Zeitraums aus, älteste zuerst.',
    'What to run': 'Was ausgeführt wird',
    'Fix the highlighted params before starting.':
      'Vor dem Start die markierten Parameter korrigieren.',
    'Nothing to run: every slot is done or held.':
      'Nichts auszuführen: Jeder Termin ist erledigt oder angehalten.',
    'Slots {from} to {to}': 'Termine {from} bis {to}',
    'Loads {from} to {to} ({entry})': 'Lädt {from} bis {to} ({entry})',
    'Run 1 slot that succeeded again':
      '1 erfolgreichen Termin erneut ausführen',
    'Run {count} slots that succeeded again':
      '{count} erfolgreiche Termine erneut ausführen',
    'The plan changed. Review it and start again.':
      'Der Plan hat sich geändert. Bitte prüfen und erneut starten.',
    'Failed to plan the backfill': 'Nachholen konnte nicht geplant werden',
    'Failed to start the backfill': 'Nachholen konnte nicht gestartet werden',
    'Start 1 run': '1 Lauf starten',
    'Start {count} runs': '{count} Läufe starten',
    'Show backfill runs': 'Nachhol-Läufe anzeigen',
    'Runs of backfill {id}': 'Läufe von Nachholen {id}',
    'Show all runs': 'Alle Läufe anzeigen',
    'Runs 1 slot': 'Führt 1 Termin aus',
    'Runs {count} slots': 'Führt {count} Termine aus',
    'Failed to cancel the backfill':
      'Nachholen konnte nicht abgebrochen werden',
    'Failed to resume the backfill':
      'Nachholen konnte nicht fortgesetzt werden',
  },
  // A no-break space (\u00a0) comes before ":" and "?", as French writes
  // them. A count that can be 0 or 1 never sits in front of a noun it would
  // have to agree with: French writes those two in the singular.
  fr: {
    Backfills: 'Rattrapages',
    'Backfill {id}': 'Rattrapage {id}',
    'Running: {done} of {total} slots done':
      'En cours – créneaux terminés\u00a0: {done} sur {total}',
    'Running: {done} of {total} slots done, {failed} failed':
      'En cours – créneaux terminés\u00a0: {done} sur {total}, en échec\u00a0: {failed}',
    'Finishing: all {total} slots are done':
      'Finalisation – tous les créneaux sont terminés ({total})',
    'Waiting for a scheduler: {done} of {total} slots done':
      'En attente d’un planificateur – créneaux terminés\u00a0: {done} sur {total}',
    'No scheduler is running, so the backfill queues no run.':
      'Aucun planificateur n’est actif, le rattrapage ne met donc aucune exécution en file d’attente.',
    'Succeeded: all {total} slots': 'Réussi – tous les créneaux ({total})',
    'Succeeded: {ran} slots ran, {skipped} skipped':
      'Réussi – créneaux exécutés\u00a0: {ran}, ignorés\u00a0: {skipped}',
    'Failed: {failed} of {total} slots failed':
      'Échec – créneaux en échec\u00a0: {failed} sur {total}',
    'Stopped: {failed} failed, {notRun} not run':
      'Arrêté – en échec\u00a0: {failed}, non exécutés\u00a0: {notRun}',
    'Cancelled: {cancelled} slots were not run':
      'Annulé – créneaux non exécutés\u00a0: {cancelled}',
    'Not run': 'Non exécuté',
    Cancelled: 'Annulé',
    Slots: 'Créneaux',
    'Run past slots of the schedule':
      'Exécuter les créneaux passés de la planification',
    'Could not read the last success: {message}':
      'Impossible de lire la dernière réussite\u00a0: {message}',
    'Since the last success': 'Depuis la dernière réussite',
    'Since the last success, {time}': 'Depuis la dernière réussite ({time})',
    'Fire times': 'Heures de déclenchement',
    'No slot succeeded in the last {days} days.':
      'Aucun créneau n’a réussi au cours des {days} derniers jours.',
    'First fire time': 'Première heure de déclenchement',
    'Last fire time': 'Dernière heure de déclenchement',
    'Times are in {zone}.': 'Les heures sont exprimées dans le fuseau {zone}.',
    Options: 'Options',
    '{count} at a time, stop after a failed slot':
      '{count} à la fois, arrêt après un créneau en échec',
    '{count} at a time, continue after a failed slot':
      '{count} à la fois, poursuite après un créneau en échec',
    "The DAG's default profile": 'Profil par défaut du DAG',
    '{name} (protected)': '{name} (protégé)',
    '{name} (admin only)': '{name} (administrateurs uniquement)',
    'No slots in this state.': 'Aucun créneau dans cet état.',
    'Slot {time}': 'Créneau {time}',
    'Older definition': 'Définition antérieure',
    'Backfill {done}/{total}': 'Rattrapage {done}/{total}',
    'Backfill {done}/{total}, {failed} failed':
      'Rattrapage {done}/{total}, {failed} en échec',
    'Backfill failed: {failed} slots':
      'Échec du rattrapage – créneaux en échec\u00a0: {failed}',
    'Page {page} of {pages}': 'Page {page} sur {pages}',
    'Submitted {time}': 'Soumis {time}',
    'Submitted {time} by {actor}': 'Soumis {time} par {actor}',
    'Resumed once': 'Repris une fois',
    'Resumed {count} times': 'Repris {count} fois',
    'Fill gaps': 'Combler les créneaux manquants',
    'Fill gaps and redo failed':
      'Combler les créneaux manquants et refaire ceux en échec',
    'Redo everything': 'Tout refaire',
    'Named slots': 'Créneaux désignés',
    '{count} at a time': '{count} à la fois',
    'Stops after a slot fails': 'S’arrête après l’échec d’un créneau',
    'Goes on after a slot fails': 'Continue après l’échec d’un créneau',
    'View runs': 'Voir les exécutions',
    'Open backfill': 'Ouvrir le rattrapage',
    'Open workflow': 'Ouvrir le workflow',
    'Cancel backfill': 'Annuler le rattrapage',
    'Cancel the backfill?': 'Annuler le rattrapage\u00a0?',
    'Its queued runs are withdrawn and the slots it has not reached are cancelled. Runs in progress finish unless you stop them.':
      'Ses exécutions en file d’attente sont retirées et les créneaux qu’il n’a pas encore atteints sont annulés. Les exécutions en cours se terminent, sauf si vous les arrêtez.',
    'Also stop the run in progress': 'Arrêter aussi l’exécution en cours',
    'Also stop the {count} runs in progress':
      'Arrêter aussi les {count} exécutions en cours',
    'Keep running': 'Laisser continuer',
    'Resume backfill': 'Reprendre le rattrapage',
    'Runs again the slots that did not succeed, with the current definition of the DAG and new runs.':
      'Exécute de nouveau les créneaux qui n’ont pas réussi, avec la définition actuelle du DAG et de nouvelles exécutions.',
    'Runs at once': 'Exécutions simultanées',
    'The queue runs {limit} at a time.':
      'La file d’attente en exécute {limit} à la fois.',
    'After a slot fails': 'Après l’échec d’un créneau',
    'Stop: for loads that build on the slot before':
      'Arrêter\u00a0: pour les chargements qui dépendent du créneau précédent',
    'Continue: for slots that do not depend on each other':
      'Continuer\u00a0: pour les créneaux indépendants les uns des autres',
    'Planning…': 'Calcul du plan…',
    'Nothing to resume: every slot succeeded or was skipped.':
      'Rien à reprendre\u00a0: chaque créneau a réussi ou a été ignoré.',
    'The plan changed. Review it and resume again.':
      'Le plan a changé. Vérifiez-le, puis relancez la reprise.',
    'Resume 1 run': 'Reprendre 1 exécution',
    'Resume {count} runs': 'Reprendre les exécutions ({count})',
    'About {duration}': 'Environ {duration}',
    'Backfill running: {done} of {total} slots done':
      'Rattrapage en cours – créneaux terminés\u00a0: {done} sur {total}',
    '{count} slots passed from {from} to {to} while {why}':
      '{count} créneaux sont passés du {from} au {to} pendant que {why}',
    '1 slot passed at {at} while {why}':
      '1 créneau est passé le {at} pendant que {why}',
    'this DAG was suspended': 'ce DAG était suspendu',
    'the scheduler was paused': 'le planificateur était en pause',
    'the scheduler was down': 'le planificateur était arrêté',
    'this DAG could not run': 'ce DAG ne pouvait pas s’exécuter',
    'The missed slots were not dismissed.':
      'Impossible de masquer les créneaux manqués.',
    'Please try again or check the server connection.':
      'Réessayez ou vérifiez la connexion au serveur.',
    'Backfill running: {done} of {total} slots done, {failed} failed':
      'Rattrapage en cours – créneaux terminés\u00a0: {done} sur {total}, en échec\u00a0: {failed}',
    'Backfill failed: {failed} slots did not succeed':
      'Échec du rattrapage – créneaux sans succès\u00a0: {failed}',
    'No backfills yet.': 'Aucun rattrapage pour l’instant.',
    'A backfill runs the past slots of this schedule.':
      'Un rattrapage exécute les créneaux passés de cette planification.',
    'A backfill runs the past slots of this schedule. Start one from the slots above.':
      'Un rattrapage exécute les créneaux passés de cette planification. Lancez-en un depuis les créneaux ci-dessus.',
    'Backfill not found': 'Rattrapage introuvable',
    'Failed to load the backfill': 'Impossible de charger le rattrapage',
    'Failed to load the backfills': 'Impossible de charger les rattrapages',
    'Slot states': 'États des créneaux',
    'Failed to load the slots': 'Impossible de charger les créneaux',
    'Last 90 days': '90 derniers jours',
    Range: 'Plage',
    'The schedule fires no slot in this range.':
      'La planification ne déclenche aucun créneau sur cette plage.',
    'Showing the newest {shown} of {total} slots. Pick a shorter range to see the rest.':
      'Affichage des {shown} créneaux les plus récents sur {total}. Choisissez une plage plus courte pour voir les autres.',
    'Pick missing': 'Sélectionner les manquants',
    'Pick missing and failed':
      'Sélectionner les créneaux manquants et en échec',
    'Backfill 1 slot': 'Rattraper 1 créneau',
    'Backfill {count} slots': 'Rattraper {count} créneaux',
    'Backfill this range': 'Rattraper cette plage',
    'Runs the {count} slots you picked, oldest first.':
      'Exécute les créneaux choisis ({count}), du plus ancien au plus récent.',
    'Runs the past slots of the range, oldest first.':
      'Exécute les créneaux passés de la plage, du plus ancien au plus récent.',
    'What to run': 'À exécuter',
    'Fix the highlighted params before starting.':
      'Corrigez les paramètres signalés avant de démarrer.',
    'Nothing to run: every slot is done or held.':
      'Rien à exécuter\u00a0: chaque créneau est terminé ou déjà en cours.',
    'Slots {from} to {to}': 'Créneaux du {from} au {to}',
    'Loads {from} to {to} ({entry})': 'Chargement du {from} au {to} ({entry})',
    'Run 1 slot that succeeded again':
      'Exécuter de nouveau 1 créneau déjà réussi',
    'Run {count} slots that succeeded again':
      'Exécuter de nouveau {count} créneaux déjà réussis',
    'The plan changed. Review it and start again.':
      'Le plan a changé. Vérifiez-le, puis relancez.',
    'Failed to plan the backfill':
      'Impossible de calculer le plan du rattrapage',
    'Failed to start the backfill': 'Impossible de démarrer le rattrapage',
    'Start 1 run': 'Démarrer 1 exécution',
    'Start {count} runs': 'Démarrer les exécutions ({count})',
    'Show backfill runs': 'Afficher les exécutions du rattrapage',
    'Runs of backfill {id}': 'Exécutions du rattrapage {id}',
    'Show all runs': 'Afficher toutes les exécutions',
    'Runs 1 slot': 'Exécute 1 créneau',
    'Runs {count} slots': 'Exécute {count} créneaux',
    'Failed to cancel the backfill': 'Impossible d’annuler le rattrapage',
    'Failed to resume the backfill': 'Impossible de reprendre le rattrapage',
  },
});
