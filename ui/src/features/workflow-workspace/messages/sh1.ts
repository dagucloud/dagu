// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The History tab of the Run panel: the steps of the workflow by run, and
// the runs of one step with its success rate and usual time.
export default defineMessages({
  en: {
    'History of {step}': 'History of {step}',
    'Runs of {step}': 'Runs of {step}',
    'Steps by run': 'Steps by run',
    '{percent}% succeeded': '{percent}% succeeded',
    '{succeeded} of {count} finished run succeeded':
      '{succeeded} of {count} finished run succeeded',
    '{succeeded} of {count} finished runs succeeded':
      '{succeeded} of {count} finished runs succeeded',
    'usually {duration}': 'usually {duration}',
    'Failing since {time}': 'Failing since {time}',
    'No finished runs of this step': 'No finished runs of this step',
    Longest: 'Longest',
    'Open the log of run {id}': 'Open the log of run {id}',
    'This step repeats without a limit: one run holds every repeat.':
      'This step repeats without a limit: one run holds every repeat.',
    'Open Continuous': 'Open Continuous',
    'The status is the one of this step, not of its sub-runs.':
      'The status is the one of this step, not of its sub-runs.',
    Show: 'Show',
    'Failures only': 'Failures only',
    'No failures in these runs': 'No failures in these runs',
  },
  'zh-CN': {
    'History of {step}': '{step} 的历史',
    'Runs of {step}': '{step} 的运行记录',
    'Steps by run': '各次运行的步骤',
    '{percent}% succeeded': '成功率 {percent}%',
    '{succeeded} of {count} finished run succeeded':
      '{count} 次已结束的运行中有 {succeeded} 次成功',
    '{succeeded} of {count} finished runs succeeded':
      '{count} 次已结束的运行中有 {succeeded} 次成功',
    'usually {duration}': '通常 {duration}',
    'Failing since {time}': '自{time}起一直失败',
    'No finished runs of this step': '此步骤还没有已结束的运行',
    Longest: '最长',
    'Open the log of run {id}': '打开运行 {id} 的日志',
    'This step repeats without a limit: one run holds every repeat.':
      '此步骤无限重复：一次运行包含其所有重复。',
    'Open Continuous': '打开持续运行',
    'The status is the one of this step, not of its sub-runs.':
      '状态是此步骤本身的状态，而不是其子运行的状态。',
    Show: '显示',
    'Failures only': '仅失败',
    'No failures in these runs': '这些运行中没有失败',
  },
  ja: {
    'History of {step}': '{step} の履歴',
    'Runs of {step}': '{step} の実行履歴',
    'Steps by run': '実行ごとのステップ',
    '{percent}% succeeded': '成功率 {percent}%',
    '{succeeded} of {count} finished run succeeded':
      '終了した {count} 回の実行のうち {succeeded} 回成功',
    '{succeeded} of {count} finished runs succeeded':
      '終了した {count} 回の実行のうち {succeeded} 回成功',
    'usually {duration}': '通常 {duration}',
    'Failing since {time}': '{time}から失敗中',
    'No finished runs of this step':
      'このステップの終了した実行はまだありません',
    Longest: '最長',
    'Open the log of run {id}': '実行 {id} のログを開く',
    'This step repeats without a limit: one run holds every repeat.':
      'このステップは上限なく繰り返します。1 回の実行にすべての繰り返しが含まれます。',
    'Open Continuous': '継続実行を開く',
    'The status is the one of this step, not of its sub-runs.':
      'ステータスはこのステップ自身のもので、サブ実行のものではありません。',
    Show: '表示',
    'Failures only': '失敗のみ',
    'No failures in these runs': 'これらの実行に失敗はありません',
  },
  de: {
    'History of {step}': 'Verlauf von {step}',
    'Runs of {step}': 'Läufe von {step}',
    'Steps by run': 'Schritte je Lauf',
    '{percent}% succeeded': '{percent} % erfolgreich',
    '{succeeded} of {count} finished run succeeded':
      '{succeeded} von {count} beendeten Lauf erfolgreich',
    '{succeeded} of {count} finished runs succeeded':
      '{succeeded} von {count} beendeten Läufen erfolgreich',
    'usually {duration}': 'meist {duration}',
    'Failing since {time}': 'Fehlerhaft seit {time}',
    'No finished runs of this step': 'Keine beendeten Läufe dieses Schritts',
    Longest: 'Längster',
    'Open the log of run {id}': 'Protokoll von Lauf {id} öffnen',
    'This step repeats without a limit: one run holds every repeat.':
      'Dieser Schritt wiederholt sich ohne Grenze: Ein Lauf enthält alle Wiederholungen.',
    'Open Continuous': 'Fortlaufend öffnen',
    'The status is the one of this step, not of its sub-runs.':
      'Der Status gilt für diesen Schritt, nicht für seine Unterläufe.',
    Show: 'Anzeigen',
    'Failures only': 'Nur Fehler',
    'No failures in these runs': 'Keine Fehler in diesen Läufen',
  },
  fr: {
    'History of {step}': 'Historique de {step}',
    'Runs of {step}': 'Exécutions de {step}',
    'Steps by run': 'Étapes par exécution',
    '{percent}% succeeded': '{percent}\u00a0% de réussite',
    '{succeeded} of {count} finished run succeeded':
      'Exécutions réussies\u00a0: {succeeded} sur {count} exécution terminée',
    '{succeeded} of {count} finished runs succeeded':
      'Exécutions réussies\u00a0: {succeeded} sur {count} exécutions terminées',
    'usually {duration}': 'habituellement {duration}',
    'Failing since {time}': 'En échec depuis l’exécution lancée {time}',
    'No finished runs of this step':
      'Aucune exécution terminée pour cette étape',
    Longest: 'Durée la plus longue',
    'Open the log of run {id}': 'Ouvrir le journal de l’exécution {id}',
    'This step repeats without a limit: one run holds every repeat.':
      'Cette étape se répète sans limite\u00a0: une seule exécution contient toutes les répétitions.',
    'Open Continuous': 'Ouvrir l’onglet Continu',
    'The status is the one of this step, not of its sub-runs.':
      'Le statut est celui de cette étape, pas celui de ses sous-exécutions.',
    Show: 'Afficher',
    'Failures only': 'Échecs uniquement',
    'No failures in these runs': 'Aucun échec dans ces exécutions',
  },
});
