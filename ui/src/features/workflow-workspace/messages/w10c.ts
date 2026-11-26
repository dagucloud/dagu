// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The marks of the reused and cached inputs of a run (INPUT column,
// graph card, run details), and the scope of the step view tabs.
export default defineMessages({
  en: {
    'from run {id}': 'from run {id}',
    'cached {time}': 'cached {time}',
    'Outputs reused from run {id}': 'Outputs reused from run {id}',
    'From your cache': 'From your cache',
    'From your cache ({time})': 'From your cache ({time})',
    'Inputs used': 'Inputs used',
    'Inputs used ({count})': 'Inputs used ({count})',
    'Show values': 'Show values',
    'Hide values': 'Hide values',
    'The outputs of a step of a sub-run are not available here.':
      'The outputs of a step of a sub-run are not available here.',
  },
  'zh-CN': {
    'from run {id}': '来自运行 {id}',
    'cached {time}': '缓存于 {time}',
    'Outputs reused from run {id}': '输出复用自运行 {id}',
    'From your cache': '来自你的缓存',
    'From your cache ({time})': '来自你的缓存（{time}）',
    'Inputs used': '使用的输入',
    'Inputs used ({count})': '使用的输入（{count}）',
    'Show values': '显示值',
    'Hide values': '隐藏值',
    'The outputs of a step of a sub-run are not available here.':
      '此处无法显示子运行中步骤的输出。',
  },
  ja: {
    'from run {id}': '実行 {id} から',
    'cached {time}': '{time} にキャッシュ',
    'Outputs reused from run {id}': '実行 {id} の出力を再利用',
    'From your cache': 'あなたのキャッシュから',
    'From your cache ({time})': 'あなたのキャッシュから（{time}）',
    'Inputs used': '使用した入力',
    'Inputs used ({count})': '使用した入力（{count}）',
    'Show values': '値を表示',
    'Hide values': '値を隠す',
    'The outputs of a step of a sub-run are not available here.':
      'サブ実行のステップの出力はここでは表示できません。',
  },
  de: {
    'from run {id}': 'aus Lauf {id}',
    'cached {time}': 'zwischengespeichert {time}',
    'Outputs reused from run {id}': 'Ausgaben aus Lauf {id} übernommen',
    'From your cache': 'Aus deinem Zwischenspeicher',
    'From your cache ({time})': 'Aus deinem Zwischenspeicher ({time})',
    'Inputs used': 'Verwendete Eingaben',
    'Inputs used ({count})': 'Verwendete Eingaben ({count})',
    'Show values': 'Werte anzeigen',
    'Hide values': 'Werte ausblenden',
    'The outputs of a step of a sub-run are not available here.':
      'Die Ausgaben eines Schritts in einem Unterlauf sind hier nicht verfügbar.',
  },
  fr: {
    'from run {id}': 'de l’exécution {id}',
    'cached {time}': 'mis en cache {time}',
    'Outputs reused from run {id}': 'Sorties réutilisées de l’exécution {id}',
    'From your cache': 'Depuis votre cache',
    'From your cache ({time})': 'Depuis votre cache ({time})',
    'Inputs used': 'Entrées utilisées',
    'Inputs used ({count})': 'Entrées utilisées ({count})',
    'Show values': 'Afficher les valeurs',
    'Hide values': 'Masquer les valeurs',
    'The outputs of a step of a sub-run are not available here.':
      'Les sorties d’une étape de sous-exécution ne sont pas disponibles ici.',
  },
});
