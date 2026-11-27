// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Workflows and Runs lists: their rows, groups and saved views.
export default defineMessages({
  en: {
    '1 run in the last 24 hours': '1 run in the last 24 hours',
    '{count} runs in the last 24 hours': '{count} runs in the last 24 hours',
    '{count}+ runs in the last 24 hours': '{count}+ runs in the last 24 hours',
    'Show recent runs': 'Show recent runs',
    'Hide recent runs': 'Hide recent runs',
    'Recent runs of {name}': 'Recent runs of {name}',
    'Could not load the recent runs': 'Could not load the recent runs',
    '{own} + {cached} cached': '{own} + {cached} cached',
    'queued {time}': 'queued {time}',
    'Webhook of {name}': 'Webhook of {name}',
    'Enable webhook': 'Enable webhook',
    'Disable webhook': 'Disable webhook',
    'Could not update the webhook': 'Could not update the webhook',
    'Rename view': 'Rename view',
    'Save the changes of “{name}”': 'Save the changes of “{name}”',
    'View actions': 'View actions',
    'The view keeps its saved filters.': 'The view keeps its saved filters.',
    'Pin group {name}': 'Pin group {name}',
    'Pin group': 'Pin group',
    'Unpin group': 'Unpin group',
    'Gather the workflows by group': 'Gather the workflows by group',
    Show: 'Show',
    'Could not update the schedules': 'Could not update the schedules',
    'Schedules of group {name}': 'Schedules of group {name}',
    'Webhooks of group {name}': 'Webhooks of group {name}',
    '{live} of {total} live': '{live} of {total} live',
    'Are you sure you want to {action} the schedules of the {count} workflows of "{name}"?':
      'Are you sure you want to {action} the schedules of the {count} workflows of "{name}"?',
    'Are you sure you want to {action} the webhooks of the {count} workflows of "{name}"?':
      'Are you sure you want to {action} the webhooks of the {count} workflows of "{name}"?',
  },
  'zh-CN': {
    '1 run in the last 24 hours': '过去 24 小时内有 1 次运行',
    '{count} runs in the last 24 hours': '过去 24 小时内有 {count} 次运行',
    '{count}+ runs in the last 24 hours': '过去 24 小时内有 {count} 次以上运行',
    'Show recent runs': '显示最近的运行',
    'Hide recent runs': '隐藏最近的运行',
    'Recent runs of {name}': '{name} 的最近运行',
    'Could not load the recent runs': '无法加载最近的运行',
    '{own} + {cached} cached': '{own} + 缓存 {cached}',
    'queued {time}': '排队 {time}',
    'Webhook of {name}': '{name} 的 Webhook',
    'Enable webhook': '启用 Webhook',
    'Disable webhook': '停用 Webhook',
    'Could not update the webhook': '无法更新 Webhook',
    'Rename view': '重命名视图',
    'Save the changes of “{name}”': '保存“{name}”的更改',
    'View actions': '视图操作',
    'The view keeps its saved filters.': '视图保留其已保存的筛选条件。',
    'Pin group {name}': '固定分组 {name}',
    'Pin group': '固定分组',
    'Unpin group': '取消固定分组',
    'Gather the workflows by group': '按分组汇总工作流',
    Show: '显示',
    'Could not update the schedules': '无法更新计划',
    'Schedules of group {name}': '分组 {name} 的计划',
    'Webhooks of group {name}': '分组 {name} 的 Webhook',
    '{live} of {total} live': '{live}/{total} 运行中',
    'Are you sure you want to {action} the schedules of the {count} workflows of "{name}"?':
      '确定要{action}“{name}”中 {count} 个工作流的计划吗？',
    'Are you sure you want to {action} the webhooks of the {count} workflows of "{name}"?':
      '确定要{action}“{name}”中 {count} 个工作流的 Webhook 吗？',
  },
  ja: {
    '1 run in the last 24 hours': '過去 24 時間に 1 件の実行',
    '{count} runs in the last 24 hours': '過去 24 時間に {count} 件の実行',
    '{count}+ runs in the last 24 hours': '過去 24 時間に {count} 件以上の実行',
    'Show recent runs': '最近の実行を表示',
    'Hide recent runs': '最近の実行を隠す',
    'Recent runs of {name}': '{name} の最近の実行',
    'Could not load the recent runs': '最近の実行を読み込めませんでした',
    '{own} + {cached} cached': '{own} + キャッシュ {cached}',
    'queued {time}': '待機 {time}',
    'Webhook of {name}': '{name} の Webhook',
    'Enable webhook': 'Webhook を有効にする',
    'Disable webhook': 'Webhook を無効にする',
    'Could not update the webhook': 'Webhook を更新できませんでした',
    'Rename view': 'ビューの名前を変更',
    'Save the changes of “{name}”': '「{name}」の変更を保存',
    'View actions': 'ビューの操作',
    'The view keeps its saved filters.':
      'ビューは保存済みのフィルターをそのまま保持します。',
    'Pin group {name}': 'グループ {name} をピン留め',
    'Pin group': 'グループをピン留め',
    'Unpin group': 'グループのピン留めを解除',
    'Gather the workflows by group': 'ワークフローをグループごとにまとめる',
    Show: '表示',
    'Could not update the schedules': 'スケジュールを更新できませんでした',
    'Schedules of group {name}': 'グループ {name} のスケジュール',
    'Webhooks of group {name}': 'グループ {name} の Webhook',
    '{live} of {total} live': '{total} 件中 {live} 件が有効',
    'Are you sure you want to {action} the schedules of the {count} workflows of "{name}"?':
      '「{name}」の {count} 件のワークフローのスケジュールを{action}しますか？',
    'Are you sure you want to {action} the webhooks of the {count} workflows of "{name}"?':
      '「{name}」の {count} 件のワークフローの Webhook を{action}しますか？',
  },
  de: {
    '1 run in the last 24 hours': '1 Lauf in den letzten 24 Stunden',
    '{count} runs in the last 24 hours':
      '{count} Läufe in den letzten 24 Stunden',
    '{count}+ runs in the last 24 hours':
      '{count}+ Läufe in den letzten 24 Stunden',
    'Show recent runs': 'Letzte Läufe anzeigen',
    'Hide recent runs': 'Letzte Läufe ausblenden',
    'Recent runs of {name}': 'Letzte Läufe von {name}',
    'Could not load the recent runs':
      'Die letzten Läufe konnten nicht geladen werden',
    '{own} + {cached} cached': '{own} + {cached} aus dem Cache',
    'queued {time}': '{time} wartend',
    'Webhook of {name}': 'Webhook von {name}',
    'Enable webhook': 'Webhook einschalten',
    'Disable webhook': 'Webhook abschalten',
    'Could not update the webhook':
      'Der Webhook konnte nicht aktualisiert werden',
    'Rename view': 'Ansicht umbenennen',
    'Save the changes of “{name}”': 'Änderungen an „{name}“ speichern',
    'View actions': 'Ansicht-Aktionen',
    'The view keeps its saved filters.':
      'Die Ansicht behält ihre gespeicherten Filter.',
    'Pin group {name}': 'Gruppe {name} anheften',
    'Pin group': 'Gruppe anheften',
    'Unpin group': 'Gruppe lösen',
    'Gather the workflows by group': 'Abläufe nach Gruppe bündeln',
    Show: 'Anzeigen',
    'Could not update the schedules':
      'Die Zeitpläne konnten nicht aktualisiert werden',
    'Schedules of group {name}': 'Zeitpläne der Gruppe {name}',
    'Webhooks of group {name}': 'Webhooks der Gruppe {name}',
    '{live} of {total} live': '{live} von {total} live',
    'Are you sure you want to {action} the schedules of the {count} workflows of "{name}"?':
      'Zeitpläne der {count} Abläufe von "{name}" wirklich {action}?',
    'Are you sure you want to {action} the webhooks of the {count} workflows of "{name}"?':
      'Webhooks der {count} Abläufe von "{name}" wirklich {action}?',
  },
  fr: {
    '1 run in the last 24 hours':
      '1 exécution au cours des dernières 24 heures',
    '{count} runs in the last 24 hours':
      '{count} exécutions au cours des dernières 24 heures',
    '{count}+ runs in the last 24 hours':
      'Au moins {count} exécutions au cours des dernières 24 heures',
    'Webhook of {name}': 'Webhook de {name}',
    'Enable webhook': 'Activer le webhook',
    'Disable webhook': 'Désactiver le webhook',
    'Could not update the webhook': 'Impossible de mettre à jour le webhook',
    'Rename view': 'Renommer la vue',
    'Save the changes of “{name}”':
      'Enregistrer les modifications de «\u00a0{name}\u00a0»',
    'View actions': 'Actions de la vue',
    'The view keeps its saved filters.':
      'La vue conserve ses filtres enregistrés.',
    'Pin group {name}': 'Épingler le groupe {name}',
    'Pin group': 'Épingler le groupe',
    'Unpin group': 'Désépingler le groupe',
    'Gather the workflows by group': 'Regrouper les workflows par groupe',
    'Could not update the schedules':
      'Impossible de mettre à jour les planifications',
    'Schedules of group {name}': 'Planifications du groupe {name}',
    'Webhooks of group {name}': 'Webhooks du groupe {name}',
    '{live} of {total} live': 'Actifs\u00a0: {live} sur {total}',
    'Are you sure you want to {action} the schedules of the {count} workflows of "{name}"?':
      'Voulez-vous vraiment {action} les planifications des workflows ({count}) du groupe «\u00a0{name}\u00a0»\u00a0?',
    'Are you sure you want to {action} the webhooks of the {count} workflows of "{name}"?':
      'Voulez-vous vraiment {action} les webhooks des workflows ({count}) du groupe «\u00a0{name}\u00a0»\u00a0?',
    'Show recent runs': 'Afficher les exécutions récentes',
    'Hide recent runs': 'Masquer les exécutions récentes',
    'Recent runs of {name}': 'Exécutions récentes de {name}',
    'Could not load the recent runs':
      'Impossible de charger les exécutions récentes',
    '{own} + {cached} cached': '{own} + {cached} en cache',
    'queued {time}': 'en file d’attente {time}',
  },
});
