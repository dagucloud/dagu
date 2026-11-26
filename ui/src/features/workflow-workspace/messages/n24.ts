// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// Retrying the nodes of steps with nodes that did not succeed (RFC 013
// §Retry).
export default defineMessages({
  en: {
    'Retry failed nodes ({count})': 'Retry failed nodes ({count})',
    'The {count} nodes that did not succeed run again, and nodes that succeeded keep their results.':
      'The {count} nodes that did not succeed run again, and nodes that succeeded keep their results.',
    'The 1 node that did not succeed runs again, and nodes that succeeded keep their results.':
      'The 1 node that did not succeed runs again, and nodes that succeeded keep their results.',
    Removed: 'Removed',
  },
  'zh-CN': {
    'Retry failed nodes ({count})': '重试失败的节点 ({count})',
    'The {count} nodes that did not succeed run again, and nodes that succeeded keep their results.':
      '未成功的 {count} 个节点会重新运行，已成功的节点保留其结果。',
    'The 1 node that did not succeed runs again, and nodes that succeeded keep their results.':
      '未成功的 1 个节点会重新运行，已成功的节点保留其结果。',
    Removed: '已移除',
  },
  ja: {
    'Retry failed nodes ({count})': '失敗したノードを再試行 ({count})',
    'The {count} nodes that did not succeed run again, and nodes that succeeded keep their results.':
      '成功しなかった {count} 件のノードを再実行し、成功したノードは結果を保持します。',
    'The 1 node that did not succeed runs again, and nodes that succeeded keep their results.':
      '成功しなかった 1 件のノードを再実行し、成功したノードは結果を保持します。',
    Removed: '削除済み',
  },
  de: {
    'Retry failed nodes ({count})':
      'Fehlgeschlagene Knoten wiederholen ({count})',
    'The {count} nodes that did not succeed run again, and nodes that succeeded keep their results.':
      'Die {count} Knoten, die nicht erfolgreich waren, laufen erneut, und erfolgreiche Knoten behalten ihre Ergebnisse.',
    'The 1 node that did not succeed runs again, and nodes that succeeded keep their results.':
      'Der eine Knoten, der nicht erfolgreich war, läuft erneut, und erfolgreiche Knoten behalten ihre Ergebnisse.',
    Removed: 'Entfernt',
  },
  fr: {
    'Retry failed nodes ({count})': 'Réessayer les nœuds en échec ({count})',
    'The {count} nodes that did not succeed run again, and nodes that succeeded keep their results.':
      'Les {count} nœuds qui n’ont pas réussi sont réexécutés, et ceux qui ont réussi conservent leurs résultats.',
    'The 1 node that did not succeed runs again, and nodes that succeeded keep their results.':
      'Le nœud qui n’a pas réussi est réexécuté, et ceux qui ont réussi conservent leurs résultats.',
    Removed: 'Supprimé',
  },
});
