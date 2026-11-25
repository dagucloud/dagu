// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// Retrying the nodes of a run's steps with nodes that did not succeed
// (RFC 013 §Retry).
export default defineMessages({
  en: {
    'Retry failed nodes ({count})': 'Retry failed nodes ({count})',
    'Retry failed nodes?': 'Retry failed nodes?',
    'This retries the run. The {count} nodes that did not succeed run again, and nodes that succeeded keep their results. Other failed steps run again too.':
      'This retries the run. The {count} nodes that did not succeed run again, and nodes that succeeded keep their results. Other failed steps run again too.',
    'Retry started': 'Retry started',
    'Failed to retry the run': 'Failed to retry the run',
    Removed: 'Removed',
  },
  'zh-CN': {
    'Retry failed nodes ({count})': '重试失败的节点 ({count})',
    'Retry failed nodes?': '重试失败的节点？',
    'This retries the run. The {count} nodes that did not succeed run again, and nodes that succeeded keep their results. Other failed steps run again too.':
      '这将重试此运行。未成功的 {count} 个节点会重新运行，已成功的节点保留其结果。其他失败的步骤也会重新运行。',
    'Retry started': '已开始重试',
    'Failed to retry the run': '重试运行失败',
    Removed: '已移除',
  },
  ja: {
    'Retry failed nodes ({count})': '失敗したノードを再試行 ({count})',
    'Retry failed nodes?': '失敗したノードを再試行しますか？',
    'This retries the run. The {count} nodes that did not succeed run again, and nodes that succeeded keep their results. Other failed steps run again too.':
      'この実行を再試行します。成功しなかった {count} 件のノードを再実行し、成功したノードは結果を保持します。失敗した他のステップも再実行されます。',
    'Retry started': '再試行を開始しました',
    'Failed to retry the run': '実行を再試行できませんでした',
    Removed: '削除済み',
  },
  de: {
    'Retry failed nodes ({count})':
      'Fehlgeschlagene Knoten wiederholen ({count})',
    'Retry failed nodes?': 'Fehlgeschlagene Knoten wiederholen?',
    'This retries the run. The {count} nodes that did not succeed run again, and nodes that succeeded keep their results. Other failed steps run again too.':
      'Der Lauf wird wiederholt. Die {count} Knoten, die nicht erfolgreich waren, laufen erneut, und erfolgreiche Knoten behalten ihre Ergebnisse. Andere fehlgeschlagene Schritte laufen ebenfalls erneut.',
    'Retry started': 'Wiederholung gestartet',
    'Failed to retry the run': 'Der Lauf konnte nicht wiederholt werden',
    Removed: 'Entfernt',
  },
});
