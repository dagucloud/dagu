// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The run history of a service DAG (spec 100): its panel, its events and
// its rows in the lists.
export default defineMessages({
  en: {
    'Last year': 'Last year',
    'Last 3 years': 'Last 3 years',
    'Run history': 'Run history',
    'the run history': 'the run history',
    'the availability of the runs': 'the availability of the runs',
    'the events of the run history': 'the events of the run history',
    'No runs in this window': 'No runs in this window',
    '{failures} failed of {count} runs': '{failures} failed of {count} runs',
    Gap: 'Gap',
    'no run for {length}': 'no run for {length}',
    'Running since {time}': 'Running since {time}',
    'Open run {id}': 'Open run {id}',
    'The runs before {time} are not kept.':
      'The runs before {time} are not kept.',
    'Runs in the last {window}': 'Runs in the last {window}',
    'Longest gap': 'Longest gap',
    'Wait {seconds} s': 'Wait {seconds} s',
    '{count} failed in a row': '{count} failed in a row',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}':
      'Runs in the last {window}: {count}, failed {failures}, success rate {availability}',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}, uptime {uptime}':
      'Runs in the last {window}: {count}, failed {failures}, success rate {availability}, uptime {uptime}',
    'Uptime {value}': 'Uptime {value}',
    'Success rate {value}': 'Success rate {value}',
    'Runs {count} · Failed {failures}': 'Runs {count} · Failed {failures}',
    Up: 'Up',
  },
  'zh-CN': {
    'Last year': '最近 1 年',
    'Last 3 years': '最近 3 年',
    'Run history': '运行历史',
    'the run history': '运行历史',
    'the availability of the runs': '运行的可用率',
    'the events of the run history': '运行历史的事件',
    'No runs in this window': '此时间段内没有运行',
    '{failures} failed of {count} runs': '{count} 次运行中失败 {failures} 次',
    Gap: '空档',
    'no run for {length}': '{length} 内没有运行',
    'Running since {time}': '自 {time} 起运行中',
    'Open run {id}': '打开运行 {id}',
    'The runs before {time} are not kept.': '{time} 之前的运行未保留。',
    'Runs in the last {window}': '过去 {window}内的运行',
    'Longest gap': '最长空档',
    'Wait {seconds} s': '等待 {seconds} 秒',
    '{count} failed in a row': '连续失败 {count} 次',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}':
      '过去 {window}内的运行：{count}，失败 {failures}，成功率 {availability}',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}, uptime {uptime}':
      '过去 {window}内的运行：{count}，失败 {failures}，成功率 {availability}，在线率 {uptime}',
    'Uptime {value}': '在线率 {value}',
    'Success rate {value}': '成功率 {value}',
    'Runs {count} · Failed {failures}': '运行 {count} · 失败 {failures}',
    Up: '在线',
  },
  ja: {
    'Last year': '過去 1 年',
    'Last 3 years': '過去 3 年',
    'Run history': '実行履歴',
    'the run history': '実行履歴',
    'the availability of the runs': '実行の可用性',
    'the events of the run history': '実行履歴のイベント',
    'No runs in this window': 'この期間に実行はありません',
    '{failures} failed of {count} runs': '{count} 件中 {failures} 件が失敗',
    Gap: '空白',
    'no run for {length}': '{length} の間、実行なし',
    'Running since {time}': '{time} から実行中',
    'Open run {id}': '実行 {id} を開く',
    'The runs before {time} are not kept.':
      '{time} より前の実行は保持されていません。',
    'Runs in the last {window}': '過去 {window}の実行',
    'Longest gap': '最長の空白',
    'Wait {seconds} s': '{seconds} 秒待機',
    '{count} failed in a row': '{count} 回連続で失敗',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}':
      '過去 {window}の実行：{count}、失敗 {failures}、成功率 {availability}',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}, uptime {uptime}':
      '過去 {window}の実行：{count}、失敗 {failures}、成功率 {availability}、稼働時間率 {uptime}',
    'Uptime {value}': '稼働時間率 {value}',
    'Success rate {value}': '成功率 {value}',
    'Runs {count} · Failed {failures}': '実行 {count} · 失敗 {failures}',
    Up: '稼働中',
  },
  de: {
    'Last year': 'Letztes Jahr',
    'Last 3 years': 'Letzte 3 Jahre',
    'Run history': 'Laufverlauf',
    'the run history': 'der Laufverlauf',
    'the availability of the runs': 'die Verfügbarkeit der Läufe',
    'the events of the run history': 'die Ereignisse des Laufverlaufs',
    'No runs in this window': 'Keine Läufe in diesem Zeitraum',
    '{failures} failed of {count} runs':
      '{failures} von {count} Läufen fehlgeschlagen',
    Gap: 'Lücke',
    'no run for {length}': 'kein Lauf seit {length}',
    'Running since {time}': 'Läuft seit {time}',
    'Open run {id}': 'Lauf {id} öffnen',
    'The runs before {time} are not kept.':
      'Die Läufe vor {time} werden nicht aufbewahrt.',
    'Runs in the last {window}': 'Läufe der letzten {window}',
    'Longest gap': 'Längste Lücke',
    'Wait {seconds} s': '{seconds} s warten',
    '{count} failed in a row': '{count} in Folge fehlgeschlagen',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}':
      'Läufe in den letzten {window}: {count}, fehlgeschlagen {failures}, Erfolgsquote {availability}',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}, uptime {uptime}':
      'Läufe in den letzten {window}: {count}, fehlgeschlagen {failures}, Erfolgsquote {availability}, Verfügbarkeit {uptime}',
    'Uptime {value}': 'Verfügbarkeit {value}',
    'Success rate {value}': 'Erfolgsquote {value}',
    'Runs {count} · Failed {failures}':
      'Läufe {count} · Fehlgeschlagen {failures}',
    Up: 'Aktiv',
  },
  fr: {
    'Last year': '12 derniers mois',
    'Last 3 years': '3 dernières années',
    'Run history': 'Historique des exécutions',
    'the run history': 'l’historique des exécutions',
    'the availability of the runs': 'la disponibilité des exécutions',
    'the events of the run history':
      'les événements de l’historique des exécutions',
    'No runs in this window': 'Aucune exécution sur cette période',
    '{failures} failed of {count} runs':
      'Exécutions en échec\u00a0: {failures} sur {count}',
    Gap: 'Lacune',
    'no run for {length}': 'aucune exécution pendant {length}',
    'Running since {time}': 'En cours depuis le {time}',
    'Open run {id}': 'Ouvrir l’exécution {id}',
    'The runs before {time} are not kept.':
      'Avant le {time}, les exécutions ne sont pas conservées.',
    'Runs in the last {window}': 'Exécutions sur les dernières {window}',
    'Longest gap': 'Lacune la plus longue',
    'Wait {seconds} s': 'Attendre {seconds} s',
    '{count} failed in a row': '{count} échecs de suite',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}':
      'Exécutions sur les dernières {window} : {count}, échecs {failures}, taux de réussite {availability}',
    'Runs in the last {window}: {count}, failed {failures}, success rate {availability}, uptime {uptime}':
      'Exécutions sur les dernières {window} : {count}, échecs {failures}, taux de réussite {availability}, disponibilité {uptime}',
    'Uptime {value}': 'Disponibilité {value}',
    'Success rate {value}': 'Taux de réussite {value}',
    'Runs {count} · Failed {failures}':
      'Exécutions {count} · Échecs {failures}',
    Up: 'En service',
  },
});
