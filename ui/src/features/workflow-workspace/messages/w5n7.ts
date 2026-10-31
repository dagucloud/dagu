// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineMessages } from './define';

// The tooltips of the disabled editor toolbar buttons and the save messages.
export default defineMessages({
  en: {
    'Nothing to revert': 'Nothing to revert',
    'Nothing to undo': 'Nothing to undo',
    'Nothing to redo': 'Nothing to redo',
    'No changes to save': 'No changes to save',
    'Make some edits before saving.': 'Make some edits before saving.',
    'Failed to save spec': 'Failed to save spec',
    'Please check the YAML syntax and try again.':
      'Please check the YAML syntax and try again.',
    'The spec was not saved': 'The spec was not saved',
    'Validation errors': 'Validation errors',
    'Please try again.': 'Please try again.',
  },
  'zh-CN': {
    'Nothing to revert': '没有可还原的更改',
    'Nothing to undo': '没有可撤销的更改',
    'Nothing to redo': '没有可重做的更改',
    'No changes to save': '没有要保存的更改',
    'Make some edits before saving.': '请先进行一些编辑，再保存。',
    'Failed to save spec': '保存规范失败',
    'Please check the YAML syntax and try again.': '请检查 YAML 语法后重试。',
    'The spec was not saved': '规范未保存',
    'Validation errors': '验证错误',
    'Please try again.': '请重试。',
  },
  ja: {
    'Nothing to revert': '元に戻す変更はありません',
    'Nothing to undo': '元に戻す操作はありません',
    'Nothing to redo': 'やり直す操作はありません',
    'No changes to save': '保存する変更はありません',
    'Make some edits before saving.': '保存する前に編集してください。',
    'Failed to save spec': '仕様の保存に失敗しました',
    'Please check the YAML syntax and try again.':
      'YAML の構文を確認して、もう一度お試しください。',
    'The spec was not saved': '仕様は保存されませんでした',
    'Validation errors': '検証エラー',
    'Please try again.': 'もう一度お試しください。',
  },
});
