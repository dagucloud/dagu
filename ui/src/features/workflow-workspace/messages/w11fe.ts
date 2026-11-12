// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The run that the inputs of a step run come from (run picker, cached values
// with their run, marks of the graph) and the test runs (timeline, canvas
// notice, run chip).
export default defineMessages({
  en: {
    'Inputs from': 'Inputs from',
    'Run to take the inputs from': 'Run to take the inputs from',
    Automatic: 'Automatic',
    'No run': 'No run',
    'missing: {steps}': 'missing: {steps}',
    'still running': 'still running',
    'Cached values': 'Cached values',
    'Cached values (from run {id}, {time})':
      'Cached values (from run {id}, {time})',
    'Cached values (from runs {ids}, {time})':
      'Cached values (from runs {ids}, {time})',
    'cached from run {id}': 'cached from run {id}',
    'cached from run {id}, {time}': 'cached from run {id}, {time}',
    'Cached values from run {id}': 'Cached values from run {id}',
    'Cached values from run {id}, {time}':
      'Cached values from run {id}, {time}',
    'Outputs reused from run {id}, {time}':
      'Outputs reused from run {id}, {time}',
    'The step changed since': 'The step changed since',
    'run {id} (no longer available)': 'run {id} (no longer available)',
    'Test run': 'Test run',
    'Test run, not kept. Back to latest': 'Test run, not kept. Back to latest',
    'Test run, not kept: it disappears {minutes} minutes after it ends':
      'Test run, not kept: it disappears {minutes} minutes after it ends',
    'Test run, not kept: it disappears at {time}':
      'Test run, not kept: it disappears at {time}',
    'Hide test runs': 'Hide test runs',
    'Show test runs': 'Show test runs',
    'Filter runs by trigger': 'Filter runs by trigger',
    'Triggered by': 'Triggered by',
    'Only one trigger type in these runs':
      'Only one trigger type in these runs',
    'No test run in these runs': 'No test run in these runs',
    'No cached time in these runs': 'No cached time in these runs',
    Reused: 'Reused',
    'Reused time: {time}': 'Reused time: {time}',
    'Time that the reused steps took in their source runs. It is not part of the duration of this run.':
      'Time that the reused steps took in their source runs. It is not part of the duration of this run.',
    'run {id}': 'run {id}',
    'cache of run {id}': 'cache of run {id}',
    'source run no longer available': 'source run no longer available',
    'Took this long in run {id}': 'Took this long in run {id}',
    'Reused from run {id}': 'Reused from run {id}',
    'In that run: {start} → {finish}': 'In that run: {start} → {finish}',
  },
  'zh-CN': {
    'Inputs from': '输入来自',
    'Run to take the inputs from': '提供输入的运行',
    Automatic: '自动',
    'No run': '无运行',
    'missing: {steps}': '缺少：{steps}',
    'still running': '仍在运行',
    'Cached values': '缓存的值',
    'Cached values (from run {id}, {time})':
      '缓存的值（来自运行 {id}，{time}）',
    'Cached values (from runs {ids}, {time})':
      '缓存的值（来自运行 {ids}，{time}）',
    'cached from run {id}': '缓存自运行 {id}',
    'cached from run {id}, {time}': '缓存自运行 {id}，{time}',
    'Cached values from run {id}': '缓存的值，来自运行 {id}',
    'Cached values from run {id}, {time}': '缓存的值，来自运行 {id}，{time}',
    'Outputs reused from run {id}, {time}': '输出复用自运行 {id}，{time}',
    'The step changed since': '此后该步骤已更改',
    'run {id} (no longer available)': '运行 {id}（已不存在）',
    'Test run': '测试运行',
    'Test run, not kept. Back to latest': '测试运行，不保留。返回最新运行',
    'Test run, not kept: it disappears {minutes} minutes after it ends':
      '测试运行，不保留：结束 {minutes} 分钟后消失',
    'Test run, not kept: it disappears at {time}':
      '测试运行，不保留：将于 {time} 消失',
    'Hide test runs': '隐藏测试运行',
    'Show test runs': '显示测试运行',
    'Filter runs by trigger': '按触发方式筛选运行',
    'Triggered by': '触发方式',
    'Only one trigger type in these runs': '这些运行只有一种触发方式',
    'No test run in these runs': '这些运行中没有测试运行',
    'No cached time in these runs': '这些运行中没有缓存时间',
    Reused: '已复用',
    'Reused time: {time}': '复用耗时：{time}',
    'Time that the reused steps took in their source runs. It is not part of the duration of this run.':
      '被复用的步骤在来源运行中所用的时间。它不计入本次运行的时长。',
    'run {id}': '运行 {id}',
    'cache of run {id}': '运行 {id} 的缓存',
    'source run no longer available': '来源运行已不存在',
    'Took this long in run {id}': '在运行 {id} 中所用的时间',
    'Reused from run {id}': '复用自运行 {id}',
    'In that run: {start} → {finish}': '在该运行中：{start} → {finish}',
  },
  ja: {
    'Inputs from': '入力の元',
    'Run to take the inputs from': '入力を取る実行',
    Automatic: '自動',
    'No run': '実行なし',
    'missing: {steps}': '不足: {steps}',
    'still running': '実行中',
    'Cached values': 'キャッシュした値',
    'Cached values (from run {id}, {time})':
      'キャッシュした値（実行 {id}、{time}）',
    'Cached values (from runs {ids}, {time})':
      'キャッシュした値（実行 {ids}、{time}）',
    'cached from run {id}': '実行 {id} からキャッシュ',
    'cached from run {id}, {time}': '実行 {id} からキャッシュ、{time}',
    'Cached values from run {id}': '実行 {id} からキャッシュした値',
    'Cached values from run {id}, {time}':
      '実行 {id} からキャッシュした値、{time}',
    'Outputs reused from run {id}, {time}': '実行 {id} の出力を再利用、{time}',
    'The step changed since': 'その後ステップが変更されています',
    'run {id} (no longer available)': '実行 {id}（存在しません）',
    'Test run': 'テスト実行',
    'Test run, not kept. Back to latest':
      'テスト実行（保存されません）。最新の実行に戻る',
    'Test run, not kept: it disappears {minutes} minutes after it ends':
      'テスト実行（保存されません）: 終了の {minutes} 分後に消えます',
    'Test run, not kept: it disappears at {time}':
      'テスト実行（保存されません）: {time} に消えます',
    'Hide test runs': 'テスト実行を隠す',
    'Show test runs': 'テスト実行を表示',
    'Filter runs by trigger': 'トリガーで実行を絞り込む',
    'Triggered by': 'トリガー',
    'Only one trigger type in these runs':
      'これらの実行のトリガーは1種類だけです',
    'No test run in these runs': 'これらの実行にテスト実行はありません',
    'No cached time in these runs': 'これらの実行にキャッシュ時間はありません',
    Reused: '再利用',
    'Reused time: {time}': '再利用した時間: {time}',
    'Time that the reused steps took in their source runs. It is not part of the duration of this run.':
      '再利用したステップが元の実行で要した時間です。この実行の所要時間には含まれません。',
    'run {id}': '実行 {id}',
    'cache of run {id}': '実行 {id} のキャッシュ',
    'source run no longer available': '元の実行は存在しません',
    'Took this long in run {id}': '実行 {id} で要した時間',
    'Reused from run {id}': '実行 {id} から再利用',
    'In that run: {start} → {finish}': 'その実行: {start} → {finish}',
  },
});
