// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The Schedule column of the Workflows page: the wording of a schedule, the
// list of all schedules and the webhook marker.
export default defineMessages({
  en: {
    'every minute': 'every minute',
    'every {n} min': 'every {n} min',
    'every {n} h': 'every {n} h',
    hourly: 'hourly',
    'hourly at :{minute}': 'hourly at :{minute}',
    'daily at {time}': 'daily at {time}',
    'weekdays at {time}': 'weekdays at {time}',
    once: 'once',
    'Show all {count} schedules': 'Show all {count} schedules',
    'profile {name}': 'profile {name}',
    'Webhook enabled': 'Webhook enabled',
    'Webhook disabled': 'Webhook disabled',
  },
  'zh-CN': {
    'every minute': '每分钟',
    'every {n} min': '每 {n} 分钟',
    'every {n} h': '每 {n} 小时',
    hourly: '每小时',
    'hourly at :{minute}': '每小时 :{minute}',
    'daily at {time}': '每天 {time}',
    'weekdays at {time}': '工作日 {time}',
    once: '一次',
    'Show all {count} schedules': '显示全部 {count} 个计划',
    'profile {name}': '配置 {name}',
    'Webhook enabled': 'Webhook 已启用',
    'Webhook disabled': 'Webhook 已停用',
  },
  ja: {
    'every minute': '毎分',
    'every {n} min': '{n} 分ごと',
    'every {n} h': '{n} 時間ごと',
    hourly: '毎時',
    'hourly at :{minute}': '毎時 :{minute}',
    'daily at {time}': '毎日 {time}',
    'weekdays at {time}': '平日 {time}',
    once: '1 回のみ',
    'Show all {count} schedules': '{count} 件のスケジュールをすべて表示',
    'profile {name}': 'プロファイル {name}',
    'Webhook enabled': 'Webhook 有効',
    'Webhook disabled': 'Webhook 無効',
  },
});
