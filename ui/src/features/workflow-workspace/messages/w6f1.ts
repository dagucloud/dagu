// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The errors and the toasts of the retry and save actions of the run
// specification (DAGSpecReadOnly).
export default defineMessages({
  en: {
    'Failed to preview edited retry': 'Failed to preview edited retry',
    'Check the edited YAML and try again.':
      'Check the edited YAML and try again.',
    'The server did not return a retry preview.':
      'The server did not return a retry preview.',
    'Check your connection and try again.':
      'Check your connection and try again.',
    'Failed to retry edited DAG': 'Failed to retry edited DAG',
    'Check the edited YAML and preview, then try again.':
      'Check the edited YAML and preview, then try again.',
    'The server did not return a new DAG-run ID.':
      'The server did not return a new DAG-run ID.',
    'New DAG run created: {id}': 'New DAG run created: {id}',
    'Failed to load source DAG': 'Failed to load source DAG',
    'The source DAG file may no longer be editable from the DAGs directory.':
      'The source DAG file may no longer be editable from the DAGs directory.',
    'Source DAG already matches the edited spec':
      'Source DAG already matches the edited spec',
    'Failed to save source DAG': 'Failed to save source DAG',
    'Source DAG saved successfully': 'Source DAG saved successfully',
  },
  'zh-CN': {
    'Failed to preview edited retry': '预览已编辑的重试失败',
    'Check the edited YAML and try again.': '请检查已编辑的 YAML 后重试。',
    'The server did not return a retry preview.': '服务器未返回重试预览。',
    'Check your connection and try again.': '请检查网络连接后重试。',
    'Failed to retry edited DAG': '重试已编辑的 DAG 失败',
    'Check the edited YAML and preview, then try again.':
      '请检查已编辑的 YAML 和预览后重试。',
    'The server did not return a new DAG-run ID.':
      '服务器未返回新的 DAG 运行 ID。',
    'New DAG run created: {id}': '已创建新的 DAG 运行：{id}',
    'Failed to load source DAG': '加载源 DAG 失败',
    'The source DAG file may no longer be editable from the DAGs directory.':
      '源 DAG 文件可能已无法在 DAG 目录中编辑。',
    'Source DAG already matches the edited spec': '源 DAG 已与编辑后的规范一致',
    'Failed to save source DAG': '保存源 DAG 失败',
    'Source DAG saved successfully': '源 DAG 已保存',
  },
  ja: {
    'Failed to preview edited retry':
      '編集したリトライのプレビューに失敗しました',
    'Check the edited YAML and try again.':
      '編集した YAML を確認して、もう一度お試しください。',
    'The server did not return a retry preview.':
      'サーバーがリトライのプレビューを返しませんでした。',
    'Check your connection and try again.':
      '接続を確認して、もう一度お試しください。',
    'Failed to retry edited DAG': '編集した DAG のリトライに失敗しました',
    'Check the edited YAML and preview, then try again.':
      '編集した YAML とプレビューを確認して、もう一度お試しください。',
    'The server did not return a new DAG-run ID.':
      'サーバーが新しい DAG 実行 ID を返しませんでした。',
    'New DAG run created: {id}': '新しい DAG 実行を作成しました: {id}',
    'Failed to load source DAG': 'ソース DAG の読み込みに失敗しました',
    'The source DAG file may no longer be editable from the DAGs directory.':
      'ソース DAG ファイルは DAG ディレクトリから編集できなくなった可能性があります。',
    'Source DAG already matches the edited spec':
      'ソース DAG は編集した仕様と同じです',
    'Failed to save source DAG': 'ソース DAG の保存に失敗しました',
    'Source DAG saved successfully': 'ソース DAG を保存しました',
  },
  de: {
    'Failed to preview edited retry':
      'Vorschau der bearbeiteten Wiederholung fehlgeschlagen',
    'Check the edited YAML and try again.':
      'Bearbeitetes YAML prüfen und erneut versuchen.',
    'The server did not return a retry preview.':
      'Der Server hat keine Vorschau der Wiederholung geliefert.',
    'Check your connection and try again.':
      'Verbindung prüfen und erneut versuchen.',
    'Failed to retry edited DAG':
      'Bearbeiteter DAG konnte nicht wiederholt werden',
    'Check the edited YAML and preview, then try again.':
      'Bearbeitetes YAML und Vorschau prüfen, dann erneut versuchen.',
    'The server did not return a new DAG-run ID.':
      'Der Server hat keine neue Lauf-ID geliefert.',
    'New DAG run created: {id}': 'Neuer Lauf angelegt: {id}',
    'Failed to load source DAG': 'Quell-DAG konnte nicht geladen werden',
    'The source DAG file may no longer be editable from the DAGs directory.':
      'Die Quell-DAG-Datei ist im DAG-Verzeichnis eventuell nicht mehr bearbeitbar.',
    'Source DAG already matches the edited spec':
      'Quell-DAG entspricht bereits der bearbeiteten Definition',
    'Failed to save source DAG': 'Quell-DAG konnte nicht gespeichert werden',
    'Source DAG saved successfully': 'Quell-DAG gespeichert',
  },
  fr: {
    'Failed to preview edited retry':
      'Impossible de générer l’aperçu de la nouvelle tentative modifiée',
    'Check the edited YAML and try again.':
      'Vérifiez le YAML modifié, puis réessayez.',
    'The server did not return a retry preview.':
      'Le serveur n’a pas renvoyé d’aperçu de la nouvelle tentative.',
    'Check your connection and try again.':
      'Vérifiez votre connexion, puis réessayez.',
    'Failed to retry edited DAG': 'Impossible de réessayer le DAG modifié',
    'Check the edited YAML and preview, then try again.':
      'Vérifiez le YAML modifié et l’aperçu, puis réessayez.',
    'The server did not return a new DAG-run ID.':
      'Le serveur n’a pas renvoyé de nouvel ID d’exécution.',
    'New DAG run created: {id}': 'Nouvelle exécution créée\u00a0: {id}',
    'Failed to load source DAG': 'Impossible de charger le DAG source',
    'The source DAG file may no longer be editable from the DAGs directory.':
      'Le fichier du DAG source n’est peut-être plus modifiable depuis le répertoire des DAG.',
    'Source DAG already matches the edited spec':
      'Le DAG source correspond déjà à la spécification modifiée',
    'Failed to save source DAG': 'Impossible d’enregistrer le DAG source',
    'Source DAG saved successfully': 'DAG source enregistré',
  },
});
