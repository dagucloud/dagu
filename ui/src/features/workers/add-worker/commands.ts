// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules and commands here mirror the MCP action's add_worker.go. The
// shared vectors in __fixtures__/commands.json hold both to the same text.

import { goTrimSpace, holdsInvite } from './address';

/** The ways to run a worker, one tab each. */
export enum WorkerTab {
  linux = 'linux',
  docker = 'docker',
  kubernetes = 'kubernetes',
}

/** What every command carries. Labels are the field's text when valid. */
export interface CommandInput {
  address: string;
  labels: string;
  inviteId: string;
  invite: string;
  /** The image for Docker and Kubernetes, from publishedImage. */
  image?: string;
}

const IMAGE = 'ghcr.io/dagucloud/dagu';
const INVITE_ENV = 'DAGU_WORKER_INVITE';
// The image's data directory.
const DATA_DIR = '/var/lib/dagu';
// How much of the invite ID names the manifest's objects.
const MANIFEST_SUFFIX_LENGTH = 8;

// The label rules a worker applies at start.
const LABEL_KEY = /^[a-zA-Z0-9][a-zA-Z0-9_.-]*$/;
const LABEL_VALUE = /^[a-zA-Z0-9][a-zA-Z0-9_./-]*$/;
const MAX_LABEL_KEY = 63;
const MAX_LABEL_VALUE = 255;
// The labels a worker sets from its own host.
const BUILTIN_LABEL_KEYS = ['os', 'arch'];

// A published release or prerelease tag, and the versions of builds that
// no release is tagged with.
const PUBLISHED_VERSION = /^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9_.-]*)?$/;
const DEV_BUILD = /^0\.0\.0$|-[0-9]+-g[0-9a-f]+$|-next$/;
const MAX_IMAGE_TAG = 128;

/**
 * The image of the server's own release, or undefined for a build that
 * has no published image. A worker never runs `latest`, which may be a
 * release that cannot join.
 */
export function publishedImage(version: string): string | undefined {
  const tag = version.replace(/^v/, '');
  if (
    !PUBLISHED_VERSION.test(tag) ||
    DEV_BUILD.test(tag) ||
    tag.length > MAX_IMAGE_TAG
  ) {
    return undefined;
  }
  return `${IMAGE}:${tag}`;
}

/** Why a labels input is refused: a static message and its values. */
export interface LabelsProblem {
  text: string;
  values: Record<string, string | number>;
}

function labelRuleBroken(key: string, value: string): boolean {
  return (
    !LABEL_KEY.test(key) ||
    key.length > MAX_LABEL_KEY ||
    value.length > MAX_LABEL_VALUE ||
    (value !== '' && !LABEL_VALUE.test(value))
  );
}

/**
 * Checks the labels field as a worker checks its labels at start. Each
 * comma-separated entry is one `key` or `key=value` label, and empty text
 * is no label. Valid labels go into the commands as canonical `key` or
 * `key=value`, without the space or line breaks around the key and value,
 * as the MCP action writes them. A problem names a label by its position,
 * never by its text, which may be a pasted invite.
 */
export function normalizeLabels(
  text: string
): { labels: string } | { problem: LabelsProblem } {
  if (text === '') return { labels: '' };
  const keys = new Set<string>();
  const entries = text.split(',');
  const labels: string[] = [];
  for (const [i, entry] of entries.entries()) {
    const values = { position: i + 1 };
    const fail = (message: string) => ({ problem: { text: message, values } });
    if (holdsInvite(entry)) {
      return fail(
        'Label {position} holds an invite. The invite goes only into the commands.'
      );
    }
    const trimmed = goTrimSpace(entry);
    if (trimmed === '') {
      return fail('Label {position} is empty. Write one key or key=value.');
    }
    const eq = trimmed.indexOf('=');
    const key = goTrimSpace(eq < 0 ? trimmed : trimmed.slice(0, eq));
    const value = eq < 0 ? '' : goTrimSpace(trimmed.slice(eq + 1));
    if (labelRuleBroken(key, value)) {
      return fail(
        'Label {position} breaks the worker label rule: a key takes letters, digits, -, _, and ., at most 63, and a value also /, at most 255, each starting with a letter or digit.'
      );
    }
    const lower = key.toLowerCase();
    if (BUILTIN_LABEL_KEYS.includes(lower)) {
      return {
        problem: {
          text: 'Label {position} sets {key}, which a worker sets from its own host.',
          values: { ...values, key: lower },
        },
      };
    }
    if (keys.has(lower)) {
      return fail(
        'Label {position} sets a key an earlier label sets; keys compare without case.'
      );
    }
    keys.add(lower);
    labels.push(eq < 0 ? key : `${key}=${value}`);
  }
  return { labels: labels.join(',') };
}

/** Quotes a word for POSIX shells and zsh, leaving plain words as they are. */
export function shellQuote(word: string): string {
  return /^[A-Za-z0-9_@%+=:,./-]+$/.test(word)
    ? word
    : `'${word.replace(/'/g, `'\\''`)}'`;
}

const GO_ESCAPES: Record<string, string> = {
  '\u0007': '\\a',
  '\b': '\\b',
  '\f': '\\f',
  '\n': '\\n',
  '\r': '\\r',
  '\t': '\\t',
  '\v': '\\v',
  '"': '\\"',
  '\\': '\\\\',
};

// Quotes a string as Go's strconv.Quote does: a printable character stays,
// and any other is escaped, such as a no-break space as \u00a0.
function goQuote(text: string): string {
  let out = '"';
  for (const ch of text) {
    const code = ch.codePointAt(0) ?? 0;
    const escape = GO_ESCAPES[ch];
    if (escape) {
      out += escape;
    } else if (/^[\p{L}\p{M}\p{N}\p{P}\p{S} ]$/u.test(ch)) {
      out += ch;
    } else if (code < 0x20 || code === 0x7f) {
      out += `\\x${code.toString(16).padStart(2, '0')}`;
    } else if (code < 0x10000) {
      out += `\\u${code.toString(16).padStart(4, '0')}`;
    } else {
      out += `\\U${code.toString(16).padStart(8, '0')}`;
    }
  }
  return `${out}"`;
}

/**
 * The command or manifest that the tab shows, exactly as it is copied, or
 * undefined for a tab that runs an image when there is none.
 */
export function workerCommand(
  tab: WorkerTab,
  input: CommandInput
): string | undefined {
  const args = workerArgs(input);
  if (tab === WorkerTab.linux) {
    return shellCommand([...args, `--worker.invite=${input.invite}`]);
  }
  if (!input.image) return undefined;
  return tab === WorkerTab.docker
    ? dockerCommand(args, input.invite, input.image)
    : kubernetesManifest(args, input.inviteId, input.invite, input.image);
}

function workerArgs({ address, labels }: CommandInput): string[] {
  return [
    'dagu',
    'worker',
    `--worker.coordinators=${address}`,
    ...(labels ? [`--worker.labels=${labels}`] : []),
  ];
}

function shellCommand(args: string[]): string {
  return args.map(shellQuote).join(' ');
}

// The named volume keeps the worker's data directory across container
// restarts.
function dockerCommand(args: string[], invite: string, image: string): string {
  return shellCommand([
    'docker',
    'run',
    '-d',
    '--restart',
    'unless-stopped',
    '-v',
    `dagu-worker:${DATA_DIR}`,
    '-e',
    `${INVITE_ENV}=${invite}`,
    image,
    ...args,
  ]);
}

// The first lowercase letters and digits of the invite ID, which a
// Kubernetes name and label value may hold.
function manifestSuffix(inviteId: string): string {
  return (inviteId.toLowerCase().match(/[a-z0-9]/g) ?? [])
    .slice(0, MANIFEST_SUFFIX_LENGTH)
    .join('');
}

// Each invite names its own Secret and Deployment, so a second worker added
// in the same namespace does not replace the first. A container restarted
// in its pod keeps the emptyDir; a new pod starts with an empty one and
// joins again with the reusable invite. Only the Secret holds the invite,
// and $$ keeps a $ in an argument from expanding a variable.
function kubernetesManifest(
  args: string[],
  inviteId: string,
  invite: string,
  image: string
): string {
  const name = `dagu-worker-${manifestSuffix(inviteId)}`;
  const secret = `dagu-worker-invite-${manifestSuffix(inviteId)}`;
  const quoted = args.map((arg) => goQuote(arg.replace(/\$/g, '$$$$')));
  return `apiVersion: v1
kind: Secret
metadata:
  name: ${secret}
stringData:
  invite: ${invite}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${name}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${name}
  template:
    metadata:
      labels:
        app: ${name}
    spec:
      containers:
        - name: worker
          image: ${image}
          args: [${quoted.join(', ')}]
          env:
            - name: ${INVITE_ENV}
              valueFrom:
                secretKeyRef:
                  name: ${secret}
                  key: invite
          volumeMounts:
            - name: data
              mountPath: ${DATA_DIR}
      volumes:
        - name: data
          emptyDir: {}
`;
}

/**
 * What to run when no worker arrives, for the tab's environment: the check
 * of how the worker host reaches the coordinator, from an empty data
 * directory or a fresh container, or for Kubernetes, the worker's log. Undefined for a tab that runs an image when there is none.
 */
export function checkCommand(
  tab: WorkerTab,
  input: Pick<CommandInput, 'address' | 'inviteId' | 'image'>
): string | undefined {
  if (tab === WorkerTab.kubernetes) {
    return `kubectl logs deployment/dagu-worker-${manifestSuffix(input.inviteId)}`;
  }
  const args = [
    'dagu',
    'worker',
    '--check',
    `--worker.coordinators=${input.address}`,
  ];
  // A new empty data directory and no inherited DAGU* variable, such as
  // either spelling of the invite or a data directory, so the check dials
  // on a host where the worker already joined. The rest of the shell's
  // environment, such as its proxies, stays. Dagu runs only once the
  // directory exists, since an empty DAGU_HOME reads the usual config.
  if (tab === WorkerTab.linux) {
    return `d="$(mktemp -d)" && env $(env | sed -n 's/^\\(DAGU[A-Za-z0-9_.]*\\)=.*/-u \\1/p') DAGU_HOME="$d" ${shellCommand(args)}`;
  }
  if (!input.image) return undefined;
  return shellCommand(['docker', 'run', '--rm', input.image, ...args]);
}
