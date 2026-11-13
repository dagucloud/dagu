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
    'No slots in this state.': 'No slots in this state.',
    'Slot {time}': 'Slot {time}',
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
    "The DAG's defaults": "The DAG's defaults",
    'Nothing to run: every slot is done or held.':
      'Nothing to run: every slot is done or held.',
    'Slots {from} to {to}': 'Slots {from} to {to}',
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
    'No slots in this state.': '没有处于此状态的时段。',
    'Slot {time}': '时段 {time}',
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
    "The DAG's defaults": 'DAG 的默认值',
    'Nothing to run: every slot is done or held.':
      '没有要运行的内容：所有时段均已完成或被占用。',
    'Slots {from} to {to}': '时段 {from} 至 {to}',
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
    'No slots in this state.': 'この状態のスロットはありません。',
    'Slot {time}': 'スロット {time}',
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
    "The DAG's defaults": 'DAG の既定値',
    'Nothing to run: every slot is done or held.':
      '実行するものはありません: すべてのスロットが完了済みか保留中です。',
    'Slots {from} to {to}': 'スロット {from} から {to}',
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
});
