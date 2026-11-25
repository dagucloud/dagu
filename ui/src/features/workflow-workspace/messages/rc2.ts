// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The step run dialog: what the server found changed for each previous step
// since the run that gives its outputs, and the consent to reuse it anyway.
export default defineMessages({
  en: {
    'changed since run {id}: {changes}': 'changed since run {id}: {changes}',
    'cannot compare with run {id}': 'cannot compare with run {id}',
    'Since run {id}, {changes} changed.': 'Since run {id}, {changes} changed.',
    'the step': 'the step',
    'an upstream step': 'an upstream step',
    'the parameters': 'the parameters',
    'the runtime profile': 'the runtime profile',
    'the base config': 'the base config',
    'the top-level settings': 'the top-level settings',
    'outputs of a changed step': 'outputs of a changed step',
    'Step only reuses the outputs of the changed steps as they are.':
      'Step only reuses the outputs of the changed steps as they are.',
    'Could not check the earlier runs: {message}':
      'Could not check the earlier runs: {message}',
    'Check again': 'Check again',
    'The run did not start because something changed after the check:':
      'The run did not start because something changed after the check:',
  },
  'zh-CN': {
    'changed since run {id}: {changes}': '自运行 {id} 起已更改：{changes}',
    'cannot compare with run {id}': '无法与运行 {id} 比较',
    'Since run {id}, {changes} changed.': '自运行 {id} 起，{changes}已更改。',
    'the step': '此步骤',
    'an upstream step': '某个上游步骤',
    'the parameters': '参数',
    'the runtime profile': '运行时配置',
    'the base config': '基础配置',
    'the top-level settings': '顶层设置',
    'outputs of a changed step': '已更改步骤的输出',
    'Step only reuses the outputs of the changed steps as they are.':
      '“仅此步骤”会原样复用已更改步骤的输出。',
    'Could not check the earlier runs: {message}':
      '无法检查之前的运行：{message}',
    'Check again': '重新检查',
    'The run did not start because something changed after the check:':
      '运行未启动，因为检查之后有内容发生了更改：',
  },
  ja: {
    'changed since run {id}: {changes}': '実行 {id} から変更あり: {changes}',
    'cannot compare with run {id}': '実行 {id} と比較できません',
    'Since run {id}, {changes} changed.':
      '実行 {id} 以降、{changes}が変更されました。',
    'the step': 'ステップ',
    'an upstream step': '上流のステップ',
    'the parameters': 'パラメータ',
    'the runtime profile': 'ランタイムプロファイル',
    'the base config': 'ベース設定',
    'the top-level settings': 'トップレベルの設定',
    'outputs of a changed step': '変更されたステップの出力',
    'Step only reuses the outputs of the changed steps as they are.':
      '「このステップのみ」は、変更されたステップの出力をそのまま再利用します。',
    'Could not check the earlier runs: {message}':
      '以前の実行を確認できませんでした: {message}',
    'Check again': '再確認',
    'The run did not start because something changed after the check:':
      '確認の後に変更があったため、実行は開始されませんでした:',
  },
  de: {
    'changed since run {id}: {changes}': 'geändert seit Lauf {id}: {changes}',
    'cannot compare with run {id}': 'nicht vergleichbar mit Lauf {id}',
    'Since run {id}, {changes} changed.':
      'Seit Lauf {id} hat sich Folgendes geändert: {changes}.',
    'the step': 'der Schritt',
    'an upstream step': 'ein vorgelagerter Schritt',
    'the parameters': 'die Parameter',
    'the runtime profile': 'das Laufzeitprofil',
    'the base config': 'die Basiskonfiguration',
    'the top-level settings': 'die Einstellungen der obersten Ebene',
    'outputs of a changed step': 'Ausgaben eines geänderten Schritts',
    'Step only reuses the outputs of the changed steps as they are.':
      '„Nur Schritt“ übernimmt die Ausgaben der geänderten Schritte unverändert.',
    'Could not check the earlier runs: {message}':
      'Frühere Läufe konnten nicht geprüft werden: {message}',
    'Check again': 'Erneut prüfen',
    'The run did not start because something changed after the check:':
      'Der Lauf wurde nicht gestartet, weil sich nach der Prüfung etwas geändert hat:',
  },
});
