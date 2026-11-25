// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The edit that sets the restart keys of a workflow: why it is refused.
export default defineMessages({
  en: {
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      'A service DAG cannot have a schedule or a retry_policy. Remove it first.',
    'The value must be a positive integer.':
      'The value must be a positive integer.',
  },
  'zh-CN': {
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      '服务型 DAG 不能有计划或 retry_policy。请先删除它。',
    'The value must be a positive integer.': '该值必须是正整数。',
  },
  ja: {
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      'サービス DAG にスケジュールも retry_policy も置けません。先に削除してください。',
    'The value must be a positive integer.': '値は正の整数にしてください。',
  },
  de: {
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      'Ein Dienst-DAG kann weder einen Zeitplan noch eine retry_policy haben. Zuerst entfernen.',
    'The value must be a positive integer.':
      'Der Wert muss eine positive ganze Zahl sein.',
  },
  fr: {
    'A service DAG cannot have a schedule or a retry_policy. Remove it first.':
      'Un DAG de service ne peut avoir ni planification ni retry_policy. Supprimez-la d’abord.',
    'The value must be a positive integer.':
      'La valeur doit être un entier strictement positif.',
  },
});
