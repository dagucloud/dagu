import { FlatCompat } from '@eslint/eslintrc';
import js from '@eslint/js';
import typescriptEslint from '@typescript-eslint/eslint-plugin';
import tsParser from '@typescript-eslint/parser';
import { defineConfig, globalIgnores } from 'eslint/config';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const compat = new FlatCompat({
  baseDirectory: __dirname,
  recommendedConfig: js.configs.recommended,
  allConfig: js.configs.all,
});

const componentLibraryImports = {
  group: [
    '@/ui/*',
    '../ui/*',
    '../../ui/*',
    '../../../ui/*',
    '../../../../ui/*',
    './components/ui/*',
    '../components/ui/*',
    '../../components/ui/*',
    '../../../components/ui/*',
    '../../../../components/ui/*',
  ],
  message:
    'Use the canonical component library import style: @/components/ui/*.',
};

// The workspace must not load Mermaid or the classic run view. The barrels
// re-export them, so only leaf modules are allowed. The step-details barrel
// re-exports StepDetailsDrawer.
const dagBarrelImports = {
  regex:
    '(^|/)dags/components(/(common|dag-execution|dag-details|visualization|step-details))?(/index(\\.tsx?)?)?$',
  message:
    'Import the leaf module: the features/dags barrels load the classic run view and Mermaid.',
};

const classicRunViewImports = {
  regex:
    '(^|/)dags/components/(DAGStatus|dag-details/(DAGStatusOverview|NodeStatusTable\\w*|DAGDetailsContent|DAGHeader)|dag-execution/(DAGExecutionHistory|LogSideModal)|visualization/(DAGGraph|Graph)|step-details/StepDetailsDrawer)(\\.tsx?)?$',
  message:
    'The workspace does not mount the classic run view or the Mermaid graph.',
};

// Imports that the workspace and the editor code that it hosts must not use.
// The banned function names are in sharedSyntaxRules: a name pattern here
// would also flag namespace imports of unrelated modules.
const sharedPrimitiveImports = [
  {
    regex: '(^|/)ui/reload-button(\\.tsx?)?$',
    message: 'Use RefreshButton: it has the loading state and the label.',
  },
  {
    regex: '(^|/)common/StatusDot(\\.tsx?)?$',
    message: 'Use StatusDot from @/components/ui/status-dot.',
  },
];

// Near pairs come back when a surface re-implements what a shared module
// owns. Each message names the module to use. A flat config block replaces
// the rule options, so this list is shared by the blocks below.
const NON_ARRAY_FROM =
  '(Array|Buffer|Object|Iterator|YAMLMap|YAMLSeq|Int8Array|Uint8Array|Uint8ClampedArray|Int16Array|Uint16Array|Int32Array|Uint32Array|Float32Array|Float64Array|BigInt64Array|BigUint64Array)';
const SHOW_MESSAGE_CALL =
  ':matches(CallExpression[callee.name=/^show(Error|Toast)$/], CallExpression[callee.property.name=/^show(Error|Toast)$/])';
const sharedSyntaxRules = [
  {
    selector:
      'ImportSpecifier[imported.name=/^(formatDurationMs|formatStepTime)$/]',
    message: 'Use formatElapsed or formatAbsoluteTime from @/lib/dagRunTiming.',
  },
  {
    selector: 'CallExpression[callee.property.name=/^(fromNow|toNow)$/]',
    message: 'Use RelativeTime.',
  },
  {
    selector: `CallExpression[callee.property.name='from']:not([callee.object.name=/^${NON_ARRAY_FROM}$/])`,
    message: 'Use RelativeTime.',
  },
  {
    selector:
      ":matches(MemberExpression[object.name='navigator'], MemberExpression[object.property.name='navigator']):matches([property.name='clipboard'], [property.value='clipboard'])",
    message: 'Use copyText or readClipboardText from @/lib/clipboard.',
  },
  {
    selector:
      "VariableDeclarator:matches([init.name='navigator'], [init.property.name='navigator']) > ObjectPattern > Property[key.name='clipboard']",
    message: 'Use copyText or readClipboardText from @/lib/clipboard.',
  },
  {
    selector: 'MemberExpression[property.name=/^toLocale(Date|Time)?String$/]',
    message: 'Use formatAbsoluteTime from @/lib/dagRunTiming.',
  },
  {
    selector: `${SHOW_MESSAGE_CALL} > :matches(Literal, TemplateLiteral).arguments`,
    message: 'Pass ts() with the text.',
  },
  {
    selector: `${SHOW_MESSAGE_CALL} > ConditionalExpression.arguments > :matches(Literal[value=/./], TemplateLiteral):matches(.consequent, .alternate)`,
    message: 'Pass ts() with the text.',
  },
  {
    selector: 'Literal[value=/\\bfocus-visible:ring-primary\\b/]',
    message: 'Use the shared focus ring: focus-visible:ring-ring.',
  },
  {
    selector: 'TemplateElement[value.raw=/\\bfocus-visible:ring-primary\\b/]',
    message: 'Use the shared focus ring: focus-visible:ring-ring.',
  },
];

export default defineConfig([
  globalIgnores(['**/node_modules/', '**/*.js']),
  {
    extends: compat.extends(
      'eslint:recommended',
      'plugin:@typescript-eslint/recommended'
    ),

    plugins: {
      '@typescript-eslint': typescriptEslint,
    },

    languageOptions: {
      parser: tsParser,
    },

    rules: {
      '@typescript-eslint/no-non-null-assertion': 'off',
      'no-restricted-imports': [
        'error',
        { patterns: [componentLibraryImports] },
      ],
    },
    ignores: ['node_modules', 'dist', 'build', 'coverage', '**/*.js'],
  },
  {
    // A flat config block replaces the rule options, so the shared pattern
    // is repeated here. The peek of the lists shares the limits of the
    // workspace: it shows the canvas, never Mermaid.
    files: ['src/features/workflow-workspace/**', 'src/features/peek/**'],
    rules: {
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            componentLibraryImports,
            dagBarrelImports,
            classicRunViewImports,
            ...sharedPrimitiveImports,
          ],
        },
      ],
    },
  },
  {
    files: [
      'src/features/visual-editor/**',
      'src/features/dags/components/dag-editor/**',
    ],
    rules: {
      'no-restricted-imports': [
        'error',
        { patterns: [componentLibraryImports, ...sharedPrimitiveImports] },
      ],
    },
  },
  {
    // Tests mock the clipboard and write the banned text as probe input.
    files: [
      'src/features/workflow-workspace/**',
      'src/features/peek/**',
      'src/features/visual-editor/**',
      'src/features/dags/components/dag-editor/**',
    ],
    ignores: ['**/__tests__/**', '**/*.test.{ts,tsx}'],
    rules: {
      'no-restricted-syntax': ['error', ...sharedSyntaxRules],
    },
  },
]);
