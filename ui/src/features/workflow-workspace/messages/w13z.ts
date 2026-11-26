// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// Previous steps of a step run: the reuse outline of a card, and the
// dependency tree of the Upstream data column.
export default defineMessages({
  en: {
    'Reused from run {id}: succeeded': 'Reused from run {id}: succeeded',
    'Reused from run {id}: failed': 'Reused from run {id}: failed',
    'Open step {name}': 'Open step {name}',
    'Outputs of {name}': 'Outputs of {name}',
    'shown above': 'shown above',
  },
  'zh-CN': {
    'Reused from run {id}: succeeded': '复用自运行 {id}:成功',
    'Reused from run {id}: failed': '复用自运行 {id}:失败',
    'Open step {name}': '打开步骤 {name}',
    'Outputs of {name}': '{name} 的输出',
    'shown above': '已在上方显示',
  },
  ja: {
    'Reused from run {id}: succeeded': '実行 {id} から再利用: 成功',
    'Reused from run {id}: failed': '実行 {id} から再利用: 失敗',
    'Open step {name}': 'ステップ {name} を開く',
    'Outputs of {name}': '{name} の出力',
    'shown above': '上に表示済み',
  },
  de: {
    'Reused from run {id}: succeeded': 'Aus Lauf {id} übernommen: erfolgreich',
    'Reused from run {id}: failed': 'Aus Lauf {id} übernommen: fehlgeschlagen',
    'Open step {name}': 'Schritt {name} öffnen',
    'Outputs of {name}': 'Ausgaben von {name}',
    'shown above': 'oben angezeigt',
  },
  fr: {
    'Reused from run {id}: succeeded':
      'Réutilisé depuis l’exécution {id}\u00a0: réussi',
    'Reused from run {id}: failed':
      'Réutilisé depuis l’exécution {id}\u00a0: en échec',
    'Open step {name}': 'Ouvrir l’étape {name}',
    'Outputs of {name}': 'Sorties de {name}',
    'shown above': 'voir ci-dessus',
  },
});
