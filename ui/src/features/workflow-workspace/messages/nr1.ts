// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Nodes tab: the node runs of a run's steps with nodes (RFC 013), one row
// per node, and the links to it from a step with nodes.
export default defineMessages({
  en: {
    'Loading node runs...': 'Loading node runs...',
    'No node runs recorded yet.': 'No node runs recorded yet.',
    'Could not load the node runs.': 'Could not load the node runs.',
    'Failed only': 'Failed only',
    'The step did not select this node': 'The step did not select this node',
    'Open the run of {node} for {step}': 'Open the run of {node} for {step}',
    'Open Nodes': 'Open Nodes',
    'This step matched 1 node.': 'This step matched 1 node.',
    'This step matched {count} nodes.': 'This step matched {count} nodes.',
  },
  'zh-CN': {
    'Loading node runs...': '正在加载节点运行...',
    'No node runs recorded yet.': '尚未记录节点运行。',
    'Could not load the node runs.': '无法加载节点运行。',
    'Failed only': '仅失败',
    'The step did not select this node': '该步骤未选中此节点',
    'Open the run of {node} for {step}': '打开 {node} 上 {step} 的运行',
    'Open Nodes': '打开节点',
    'This step matched 1 node.': '此步骤匹配了 1 个节点。',
    'This step matched {count} nodes.': '此步骤匹配了 {count} 个节点。',
  },
  ja: {
    'Loading node runs...': 'ノードの実行を読み込み中...',
    'No node runs recorded yet.': 'ノードの実行はまだ記録されていません。',
    'Could not load the node runs.': 'ノードの実行を読み込めませんでした。',
    'Failed only': '失敗のみ',
    'The step did not select this node':
      'このステップはこのノードを選択していません',
    'Open the run of {node} for {step}': '{node} の {step} の実行を開く',
    'Open Nodes': 'ノードを開く',
    'This step matched 1 node.': 'このステップは 1 件のノードに一致しました。',
    'This step matched {count} nodes.':
      'このステップは {count} 件のノードに一致しました。',
  },
  de: {
    'Loading node runs...': 'Knotenläufe werden geladen...',
    'No node runs recorded yet.': 'Noch keine Knotenläufe aufgezeichnet.',
    'Could not load the node runs.':
      'Die Knotenläufe konnten nicht geladen werden.',
    'Failed only': 'Nur fehlgeschlagene',
    'The step did not select this node':
      'Der Schritt hat diesen Knoten nicht ausgewählt',
    'Open the run of {node} for {step}': 'Lauf von {node} für {step} öffnen',
    'Open Nodes': 'Knoten öffnen',
    'This step matched 1 node.': 'Dieser Schritt hat 1 Knoten ausgewählt.',
    'This step matched {count} nodes.':
      'Dieser Schritt hat {count} Knoten ausgewählt.',
  },
  fr: {
    'Loading node runs...': 'Chargement des exécutions de nœud…',
    'No node runs recorded yet.':
      'Aucune exécution de nœud enregistrée pour l’instant.',
    'Could not load the node runs.':
      'Impossible de charger les exécutions de nœud.',
    'Failed only': 'Échecs uniquement',
    'The step did not select this node': 'L’étape n’a pas sélectionné ce nœud',
    'Open the run of {node} for {step}':
      'Ouvrir l’exécution du nœud {node} pour l’étape {step}',
    'Open Nodes': 'Ouvrir l’onglet Nœuds',
    'This step matched 1 node.': 'Cette étape a sélectionné 1 nœud.',
    'This step matched {count} nodes.':
      'Cette étape a sélectionné {count} nœuds.',
  },
});
