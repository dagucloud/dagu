// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The line above the inputs used: where their values come from.
export default defineMessages({
  en: {
    'Values from run {id}, {time}': 'Values from run {id}, {time}',
    'Values from run {id}': 'Values from run {id}',
    'Values from the cache, {time}': 'Values from the cache, {time}',
    'Values from the cache': 'Values from the cache',
  },
  'zh-CN': {
    'Values from run {id}, {time}': '数据来自运行 {id}，{time}',
    'Values from run {id}': '数据来自运行 {id}',
    'Values from the cache, {time}': '数据来自缓存，{time}',
    'Values from the cache': '数据来自缓存',
  },
  ja: {
    'Values from run {id}, {time}': '値の取得元: 実行 {id}、{time}',
    'Values from run {id}': '値の取得元: 実行 {id}',
    'Values from the cache, {time}': '値の取得元: キャッシュ、{time}',
    'Values from the cache': '値の取得元: キャッシュ',
  },
});
