// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * The Monarch grammar of the YAML editors. It replaces the stock grammar of
 * Monaco, which reads the text of a prompt as YAML: a colon, a dash, a number
 * or a `#` in a block scalar turned into key, number or comment colours.
 *
 * What changes from the stock grammar:
 * - The content of a block scalar (`|`, `>`) is one string token, for every
 *   line that is more indented than the key. Only `${...}` references have
 *   their own token.
 * - The value after `key:` is one scalar: a second colon in it is text, not a
 *   key. A quoted string can span several lines and is never "invalid".
 * - `${...}` references in a plain, quoted or block string are a
 *   `variable.reference` token.
 *
 * Limit: a plain scalar that continues on the next lines has no indent rule
 * here, so a line of it with a colon reads as a key. Use `|` or `>` for a
 * prompt of several lines.
 *
 * The module does not import Monaco, so a test can compile the grammar.
 *
 * @module lib/monaco-yaml-language
 */
import type { languages } from 'monaco-editor';

/** A `${...}` reference: no brace, no line break inside. */
const REF = /\$\{[^{}\n]*\}/;
/** One character of a string, or an escaped or lone `$` (not a reference). */
const TEXT_UNIT = String.raw`\\\$|[^$]|\$(?!\{[^{}\n]*\})`;
/** The same for a plain scalar, which ends before a comment: " #". */
const PLAIN_UNIT = String.raw`\\\$|[^$\s]|\$(?!\{[^{}\n]*\})|\s+(?![#\s])`;

// The key of a mapping line: a quoted string, or text up to the first ": ".
const KEY = String.raw`"[^"]*"|'[^']*'|[^\s#'"&*!|>%@\x60\[\]{},?:-][^#]*?|-[^\s#:][^#]*?`;
const HEADER = String.raw`[|>][0-9+-]*`;

/** Pops the stack when the token is the last one of the line. */
function endsLine(token: string): languages.IMonarchLanguageAction {
  return {
    cases: {
      '@eos': { token, next: '@pop' },
      '@default': token,
    },
  } as languages.IMonarchLanguageAction;
}

/** The grammar of the `yaml` language id. */
export const DAGU_YAML_LANGUAGE: languages.IMonarchLanguage = {
  tokenPostfix: '.yaml',
  brackets: [
    { token: 'delimiter.bracket', open: '{', close: '}' },
    { token: 'delimiter.square', open: '[', close: ']' },
  ],
  keywords: [
    'true',
    'True',
    'TRUE',
    'false',
    'False',
    'FALSE',
    'null',
    'Null',
    'NULL',
    '~',
  ],
  numberInteger: /(?:0|[+-]?[0-9]+)/,
  numberFloat: /(?:0|[+-]?[0-9]+)(?:\.[0-9]+)?(?:e[-+][1-9][0-9]*)?/,
  numberOctal: /0o[0-7]+/,
  numberHex: /0x[0-9a-fA-F]+/,
  numberInfinity: /[+-]?\.(?:inf|Inf|INF)/,
  numberNaN: /\.(?:nan|Nan|NAN)/,
  numberDate:
    /\d{4}-\d\d-\d\d([Tt ]\d\d:\d\d:\d\d(\.\d+)?(( ?[+-]\d\d?(:\d\d)?)|Z)?)?/,
  escapes: /\\(?:[btnfr\\"']|[0-7][0-7]?|[0-3][0-7]{2})/,
  tokenizer: {
    root: [
      // A block scalar header ends the line: the lines below it are text.
      // The next state carries the indent that the text must exceed. The
      // state change sits on the header group: a group with no text (here the
      // trailing comment) is not run at the end of the line.
      [
        new RegExp(
          String.raw`^( *)(${KEY})(:)([ \t]+)(${HEADER})([ \t]*(?:#.*)?)$`
        ),
        [
          'white',
          'type',
          'operators',
          'white',
          { token: 'operators', next: '@blockScalar.$1' },
          'comment',
        ],
      ],
      [
        new RegExp(
          String.raw`^( *)(- +)(${KEY})(:)([ \t]+)(${HEADER})([ \t]*(?:#.*)?)$`
        ),
        [
          'white',
          'operators',
          'type',
          'operators',
          'white',
          { token: 'operators', next: '@blockScalar.$1  ' },
          'comment',
        ],
      ],
      [
        new RegExp(String.raw`^( *)(-)([ \t]+)(${HEADER})([ \t]*(?:#.*)?)$`),
        [
          'white',
          'operators',
          'white',
          { token: 'operators', next: '@blockScalar.$1' },
          'comment',
        ],
      ],
      { include: '@whitespace' },
      { include: '@comment' },
      [/%[^ ]+.*$/, 'meta.directive'],
      [/---/, 'operators.directivesEnd'],
      [/\.{3}/, 'operators.documentEnd'],
      [/[-?:](?= )/, 'operators'],
      { include: '@anchor' },
      { include: '@tagHandle' },
      { include: '@flowCollections' },
      [/@numberInteger(?![ \t]*\S+)/, 'number'],
      [/@numberFloat(?![ \t]*\S+)/, 'number.float'],
      [/@numberOctal(?![ \t]*\S+)/, 'number.octal'],
      [/@numberHex(?![ \t]*\S+)/, 'number.hex'],
      [/@numberInfinity(?![ \t]*\S+)/, 'number.infinity'],
      [/@numberNaN(?![ \t]*\S+)/, 'number.nan'],
      [/@numberDate(?![ \t]*\S+)/, 'number.date'],
      // A key with no value on the line, or with a comment only.
      [
        new RegExp(`(${KEY})([ \\t]*)(:)(?=[ \\t]*$|[ \\t]+#)`),
        ['type', 'white', 'operators'],
      ],
      // A key with a value: the value is one scalar.
      [
        new RegExp(`(${KEY})([ \\t]*)(:)(?=[ \\t]+[^\\s#])`),
        ['type', 'white', { token: 'operators', next: '@valueStart' }],
      ],
      [/"/, 'string', '@doubleQuoted'],
      [/'/, 'string', '@singleQuoted'],
      // A plain scalar of a sequence entry or a continued line.
      [/\S/, { token: '@rematch', next: '@plain' }],
    ],

    // The first token of the value after "key: ".
    valueStart: [
      [/[ \t]+#.*$/, { token: 'comment', next: '@pop' }],
      [/[ \t]+/, 'white'],
      [/[&*][^ \t]*/, 'namespace'],
      [/![^ \t]*/, 'namespace'],
      [/\[/, { token: 'delimiter.square', switchTo: '@array' }],
      [/\{/, { token: 'delimiter.bracket', switchTo: '@object' }],
      [/"/, { token: 'string', switchTo: '@doubleQuoted' }],
      [/'/, { token: 'string', switchTo: '@singleQuoted' }],
      [
        /(?:@numberDate|@numberFloat|@numberOctal|@numberHex|@numberInfinity|@numberNaN)(?=[ \t]*(?:#.*)?$)/,
        {
          cases: {
            '@eos': { token: 'number', next: '@pop' },
            '@default': { token: 'number', switchTo: '@plain' },
          },
        },
      ],
      [
        /(?:true|True|TRUE|false|False|FALSE|null|Null|NULL|~)(?=[ \t]*(?:#.*)?$)/,
        {
          cases: {
            '@eos': { token: 'keyword', next: '@pop' },
            '@default': { token: 'keyword', switchTo: '@plain' },
          },
        },
      ],
      [/\S/, { token: '@rematch', switchTo: '@plain' }],
    ],

    // The text of a plain scalar, up to the end of the line or a comment.
    plain: [
      [/[ \t]+#.*$/, { token: 'comment', next: '@pop' }],
      [REF, endsLine('variable.reference')],
      [new RegExp(`(?:${PLAIN_UNIT})+`), endsLine('string')],
    ],

    // The lines of a block scalar. The argument of the state is the indent of
    // the key: a line is text when it is blank or more indented.
    blockScalar: [
      [/^[ \t]*$/, 'white'],
      [/^$S2 +/, 'white'],
      [/^/, { token: '', next: '@pop' }],
      [REF, 'variable.reference'],
      [new RegExp(`(?:${TEXT_UNIT})+`), 'string'],
    ],

    doubleQuoted: [
      [/\$\{[^{}"\\\n]*\}/, 'variable.reference'],
      [/(?:[^\\"$]|\$(?!\{[^{}"\\\n]*\}))+/, 'string'],
      [/@escapes/, 'string.escape'],
      [/\\.?/, 'string.escape'],
      [/"/, 'string', '@pop'],
    ],

    singleQuoted: [
      [/\$\{[^{}'\n]*\}/, 'variable.reference'],
      [/(?:[^'$]|\$(?!\{[^{}'\n]*\}))+/, 'string'],
      [/''/, 'string.escape'],
      [/'/, 'string', '@pop'],
    ],

    // Flow collections: the stock rules, with the quoted states above.
    object: [
      { include: '@whitespace' },
      { include: '@comment' },
      [/\}/, 'delimiter.bracket', '@pop'],
      [/,/, 'delimiter.comma'],
      [/:(?= )/, 'operators'],
      [/(?:".*?"|'.*?'|[^,{[]+?)(?=: )/, 'type'],
      { include: '@flowCollections' },
      [/"/, 'string', '@doubleQuoted'],
      [/'/, 'string', '@singleQuoted'],
      { include: '@tagHandle' },
      { include: '@anchor' },
      { include: '@flowNumber' },
      [REF, 'variable.reference'],
      [/[^},]+/, { cases: { '@keywords': 'keyword', '@default': 'string' } }],
    ],

    array: [
      { include: '@whitespace' },
      { include: '@comment' },
      [/\]/, 'delimiter.square', '@pop'],
      [/,/, 'delimiter.comma'],
      { include: '@flowCollections' },
      [/"/, 'string', '@doubleQuoted'],
      [/'/, 'string', '@singleQuoted'],
      { include: '@tagHandle' },
      { include: '@anchor' },
      { include: '@flowNumber' },
      [REF, 'variable.reference'],
      [/[^\],]+/, { cases: { '@keywords': 'keyword', '@default': 'string' } }],
    ],

    whitespace: [[/[ \t\r\n]+/, 'white']],
    comment: [[/#.*$/, 'comment']],
    flowCollections: [
      [/\[/, 'delimiter.square', '@array'],
      [/\{/, 'delimiter.bracket', '@object'],
    ],
    flowNumber: [
      [/@numberInteger(?=[ \t]*[,\]}])/, 'number'],
      [/@numberFloat(?=[ \t]*[,\]}])/, 'number.float'],
      [/@numberOctal(?=[ \t]*[,\]}])/, 'number.octal'],
      [/@numberHex(?=[ \t]*[,\]}])/, 'number.hex'],
      [/@numberInfinity(?=[ \t]*[,\]}])/, 'number.infinity'],
      [/@numberNaN(?=[ \t]*[,\]}])/, 'number.nan'],
      [/@numberDate(?=[ \t]*[,\]}])/, 'number.date'],
    ],
    tagHandle: [[/![^ ]*/, 'namespace']],
    anchor: [[/[&*][^ ]+/, 'namespace']],
  },
};
