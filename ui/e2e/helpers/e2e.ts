// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';

export type StackConfig = {
  stateDir: string;
  binPath: string;
  local: {
    baseURL: string;
    apiBaseURL: string;
    dagsDir: string;
  };
  remote: {
    baseURL: string;
    apiBaseURL: string;
    dagsDir: string;
  };
  auth: {
    adminUsername: string;
    adminPassword: string;
  };
  queues: {
    shared: string;
    balance: string;
  };
  workers: string[];
};

export type UserRole = 'admin' | 'manager' | 'developer' | 'operator' | 'viewer';
type WorkspaceAccessPayload = {
  all: boolean;
  grants: Array<{
    workspace: string;
    role: UserRole;
  }>;
};
export type RunStatusLabel =
  | 'not_started'
  | 'running'
  | 'failed'
  | 'aborted'
  | 'succeeded'
  | 'queued'
  | 'partially_succeeded'
  | 'waiting'
  | 'rejected';

type LoginResponse = {
  token: string;
  user: {
    id: string;
    username: string;
    role: UserRole;
  };
};

export type DAGRunDetails = {
  dagRunId: string;
  name: string;
  status: RunStatusLabel;
  statusCode: number;
  statusLabel: RunStatusLabel;
  workerId?: string;
  sourceFileName?: string;
};

export type LocalRunStatusRecord = {
  dagRunId: string;
  status: number;
  workerId?: string;
  pid?: number;
};

type RawDAGRunDetails = {
  dagRunId: string;
  name: string;
  status: number;
  statusLabel?: string;
  workerId?: string;
  sourceFileName?: string;
};

type DAGRunDetailsResponse = {
  dagRunDetails: RawDAGRunDetails;
};

type QueueDetails = {
  name: string;
  runningCount: number;
  queuedCount: number;
  running: Array<{ name: string; dagRunId: string }>;
};

type WorkersResponse = {
  workers: Array<{
    id: string;
    busyPollers: number;
    labels: Record<string, string>;
    runningTasks: Array<{ dagName: string; dagRunId: string }>;
  }>;
};

type LogResponse = {
  content: string;
};

type ExecFileSyncError = Error & {
  status?: number | null;
  stderr?: string | Buffer;
};

const TOKEN_KEY = 'dagu_auth_token';
const WORKSPACE_SCOPE_STORAGE_KEY = 'dagu-selected-workspace-scope';
const LEGACY_WORKSPACE_STORAGE_KEY = 'dagu-selected-workspace';
const LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY = 'dagu_cockpit_workspace';
const repoRoot = path.resolve(__dirname, '../../..');
const stackScriptPath = path.resolve(repoRoot, 'scripts/e2e/start-stack.sh');
const stackFilePath =
  process.env.DAGU_E2E_STACK_FILE ??
  path.resolve(
    process.env.DAGU_E2E_STATE_DIR ?? path.resolve(repoRoot, 'ui/test-results/e2e-stack'),
    'stack.json'
  );

let cachedStack: StackConfig | null = null;

export function hasRBACLicenseSourceConfigured(): boolean {
  return Boolean(
    process.env.DAGU_LICENSE_PRIVKEY_B64 ||
      process.env.DAGU_LICENSE ||
      process.env.DAGU_LICENSE_KEY ||
      process.env.DAGU_LICENSE_FILE
  );
}

export async function loadStack(): Promise<StackConfig> {
  if (cachedStack) {
    return cachedStack;
  }

  const raw = await fs.readFile(stackFilePath, 'utf8');
  cachedStack = JSON.parse(raw) as StackConfig;
  return cachedStack;
}

export function uniqueName(prefix: string): string {
  const timestamp = Date.now().toString(36);
  const random = Math.random().toString(36).slice(2, 8);
  return `${prefix}-${timestamp}-${random}`;
}

export async function writeLocalDAG(name: string, spec: string): Promise<string> {
  const stack = await loadStack();
  const fileName = `${name}.yaml`;
  await fs.writeFile(path.join(stack.local.dagsDir, fileName), `${spec.trim()}\n`, 'utf8');
  return fileName;
}

export async function writeRemoteDAG(name: string, spec: string): Promise<string> {
  const stack = await loadStack();
  const fileName = `${name}.yaml`;
  await fs.writeFile(path.join(stack.remote.dagsDir, fileName), `${spec.trim()}\n`, 'utf8');
  return fileName;
}

export async function loginViaUI(
  page: Page,
  username: string,
  password: string
): Promise<void> {
  await page.goto('/login');
  await page.getByLabel('Username').fill(username);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign In' }).click();
  await expect(page).not.toHaveURL(/\/login$/);
}

export async function clearSession(page: Page): Promise<void> {
  await page.goto('/login');
  await page.evaluate((tokenKey) => {
    localStorage.removeItem(tokenKey);
  }, TOKEN_KEY);
  await page.reload();
}

export async function useDefaultWorkspaceScope(page: Page): Promise<void> {
  await page.evaluate(
    ([scopeKey, legacyKey, cockpitLegacyKey]) => {
      localStorage.setItem(scopeKey, JSON.stringify({ scope: 'default' }));
      localStorage.removeItem(legacyKey);
      localStorage.removeItem(cockpitLegacyKey);
    },
    [
      WORKSPACE_SCOPE_STORAGE_KEY,
      LEGACY_WORKSPACE_STORAGE_KEY,
      LEGACY_COCKPIT_WORKSPACE_STORAGE_KEY,
    ]
  );
}

export async function loginViaAPI(
  request: APIRequestContext,
  username: string,
  password: string
): Promise<string> {
  const response = await request.post('/api/v1/auth/login', {
    data: {
      username,
      password,
    },
  });

  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as LoginResponse;
  expect(body.token).toBeTruthy();
  return body.token;
}

export async function createUser(
  request: APIRequestContext,
  token: string,
  user: {
    username: string;
    password: string;
    role: UserRole;
    workspaceAccess?: WorkspaceAccessPayload;
  }
): Promise<void> {
  const response = await request.post('/api/v1/users?remoteNode=local', {
    headers: authHeaders(token),
    data: {
      ...user,
      workspaceAccess: user.workspaceAccess ?? { all: true, grants: [] },
    },
  });

  const failureBody = response.ok() ? '' : await response.text();
  expect(
    response.ok(),
    `createUser failed with ${response.status()}: ${failureBody}`
  ).toBeTruthy();
}

export async function waitForDAGAvailable(
  request: APIRequestContext,
  token: string,
  fileName: string,
  remoteNode: string = 'local',
  timeout: number = 15_000
): Promise<void> {
  await expect
    .poll(
      async () => {
        const response = await request.get(
          `/api/v1/dags/${encodeURIComponent(fileName)}?remoteNode=${encodeURIComponent(remoteNode)}`,
          {
            headers: authHeaders(token),
          }
        );
        return response.ok();
      },
      {
        timeout,
      }
    )
    .toBeTruthy();
}

export async function waitForSchedulerDAGRegistered(
  dagName: string,
  timeout: number = 15_000
): Promise<void> {
  const stack = await loadStack();
  const schedulerStatePath = path.join(stack.stateDir, 'local/runtime/data/scheduler/state.json');

  await expect
    .poll(
      async () => {
        try {
          const raw = await fs.readFile(schedulerStatePath, 'utf8');
          const parsed = JSON.parse(raw) as {
            dags?: Record<string, unknown>;
            data?: { dags?: Record<string, unknown> };
          };
          // Support both legacy flat format and the persis-envelope format
          // ({"id","encoding","data":{...state...},...}) used by the new store.
          const state = parsed.data ?? parsed;
          return Boolean(state.dags?.[dagName]);
        } catch {
          return false;
        }
      },
      {
        timeout,
      }
    )
    .toBeTruthy();
}

export async function startDAG(
  request: APIRequestContext,
  token: string,
  fileName: string,
  body: Record<string, unknown> = {},
  remoteNode: string = 'local'
): Promise<string> {
  const response = await request.post(
    `/api/v1/dags/${encodeURIComponent(fileName)}/start?remoteNode=${encodeURIComponent(
      remoteNode
    )}`,
    {
      headers: authHeaders(token),
      data: body,
    }
  );

  expect(response.ok()).toBeTruthy();
  const payload = (await response.json()) as { dagRunId?: string };
  expect(payload.dagRunId).toBeTruthy();
  return payload.dagRunId as string;
}

export async function enqueueDAG(
  request: APIRequestContext,
  token: string,
  fileName: string,
  body: Record<string, unknown> = {},
  remoteNode: string = 'local'
): Promise<string> {
  const response = await request.post(
    `/api/v1/dags/${encodeURIComponent(fileName)}/enqueue?remoteNode=${encodeURIComponent(
      remoteNode
    )}`,
    {
      headers: authHeaders(token),
      data: body,
    }
  );

  expect(response.ok()).toBeTruthy();
  const payload = (await response.json()) as { dagRunId?: string };
  expect(payload.dagRunId).toBeTruthy();
  return payload.dagRunId as string;
}

export async function getDAGRun(
  request: APIRequestContext,
  token: string,
  name: string,
  dagRunId: string,
  remoteNode: string = 'local'
): Promise<DAGRunDetails> {
  const response = await request.get(
    `/api/v1/dag-runs/${encodeURIComponent(name)}/${encodeURIComponent(
      dagRunId
    )}?remoteNode=${encodeURIComponent(remoteNode)}`,
    {
      headers: authHeaders(token),
    }
  );

  expect(response.ok()).toBeTruthy();
  return normalizeDAGRunDetails(((await response.json()) as DAGRunDetailsResponse).dagRunDetails);
}

export async function listDAGRuns(
  request: APIRequestContext,
  token: string,
  name: string,
  remoteNode: string = 'local'
): Promise<Array<{ dagRunId: string; status: RunStatusLabel; statusCode: number; workerId?: string }>> {
  const response = await request.get(
    `/api/v1/dag-runs/${encodeURIComponent(name)}?remoteNode=${encodeURIComponent(
      remoteNode
    )}&limit=100`,
    {
      headers: authHeaders(token),
    }
  );

  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as {
    dagRuns?: Array<{ dagRunId: string; status: number; statusLabel?: string; workerId?: string }>;
  };
  return (body.dagRuns ?? []).map((run) => ({
    dagRunId: run.dagRunId,
    status: normalizeRunStatus(run.status, run.statusLabel),
    statusCode: run.status,
    workerId: run.workerId,
  }));
}

export async function waitForRunStatus(
  request: APIRequestContext,
  token: string,
  name: string,
  dagRunId: string,
  expectedStatuses: RunStatusLabel[],
  remoteNode: string = 'local',
  timeout: number = 60_000
): Promise<DAGRunDetails> {
  let latest: DAGRunDetails | null = null;

  await expect
    .poll(
      async () => {
        latest = await getDAGRun(request, token, name, dagRunId, remoteNode);
        return expectedStatuses.includes(latest.status);
      },
      {
        timeout,
      }
    )
    .toBeTruthy();

  return latest as DAGRunDetails;
}

export async function waitForWorkerSet(
  request: APIRequestContext,
  token: string,
  expectedWorkerIds: string[],
  timeout: number = 30_000
): Promise<WorkersResponse> {
  let latest: WorkersResponse | null = null;

  await expect
    .poll(
      async () => {
        latest = await getWorkers(request, token);
        return expectedWorkerIds.every((workerId) =>
          latest?.workers.some((worker) => worker.id === workerId)
        );
      },
      {
        timeout,
      }
    )
    .toBeTruthy();

  return latest as WorkersResponse;
}

export async function getWorkers(
  request: APIRequestContext,
  token: string
): Promise<WorkersResponse> {
  const response = await request.get('/api/v1/workers?remoteNode=local', {
    headers: authHeaders(token),
  });
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as WorkersResponse;
}

export async function getQueue(
  request: APIRequestContext,
  token: string,
  queueName: string
): Promise<QueueDetails> {
  const response = await request.get(`/api/v1/queues/${encodeURIComponent(queueName)}?remoteNode=local`, {
    headers: authHeaders(token),
  });
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as QueueDetails;
}

export async function waitForQueueCounts(
  request: APIRequestContext,
  token: string,
  queueName: string,
  expected: { runningCount: number; queuedCount: number },
  timeout: number = 30_000
): Promise<void> {
  await expect
    .poll(
      async () => {
        const queue = await getQueue(request, token, queueName);
        return {
          runningCount: queue.runningCount,
          queuedCount: queue.queuedCount,
        };
      },
      {
        timeout,
      }
    )
    .toEqual(expected);
}

export async function getStepStdout(
  request: APIRequestContext,
  token: string,
  dagName: string,
  dagRunId: string,
  stepName: string
): Promise<string> {
  const response = await request.get(
    `/api/v1/dag-runs/${encodeURIComponent(dagName)}/${encodeURIComponent(
      dagRunId
    )}/steps/${encodeURIComponent(stepName)}/log?remoteNode=local&stream=stdout`,
    {
      headers: authHeaders(token),
    }
  );
  expect(response.ok()).toBeTruthy();
  return ((await response.json()) as LogResponse).content;
}

export async function enqueueRunFromUI(page: Page, fileName: string): Promise<string> {
  const responsePromise = page.waitForResponse(
    (response) =>
      response.request().method() === 'POST' &&
      response.url().includes(`/api/v1/dags/${encodeURIComponent(fileName)}/enqueue`)
  );

  await page.getByRole('button', { name: 'Enqueue' }).first().click();

  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();

  const enqueueToggle = dialog.getByRole('checkbox', { name: 'Enqueue' });
  if ((await enqueueToggle.getAttribute('data-state')) !== 'checked') {
    await enqueueToggle.click();
  }

  await dialog.getByRole('button', { name: /^Enqueue$/ }).click();

  const response = await responsePromise;
  expect(response.ok()).toBeTruthy();

  const body = (await response.json()) as { dagRunId?: string };
  expect(body.dagRunId).toBeTruthy();
  await expect(dialog).toBeHidden();

  return body.dagRunId as string;
}

export async function selectRemoteNode(page: Page, nodeName: string): Promise<void> {
  const trigger = page
    .locator('aside')
    .getByRole('combobox', { name: 'Remote node' });
  await trigger.click();
  await page.getByRole('option', { name: nodeName }).click();
  await expect(trigger).toContainText(nodeName);
}

export async function stopService(
  serviceName: string,
  signal: 'TERM' | 'KILL' = 'TERM'
): Promise<void> {
  const stack = await loadStack();
  execFileSync(stackScriptPath, ['stop-service', serviceName, signal], {
    cwd: repoRoot,
    env: {
      ...process.env,
      DAGU_E2E_STATE_DIR: stack.stateDir,
      DAGU_E2E_BIN: stack.binPath,
    },
    stdio: 'pipe',
  });
}

export async function startService(serviceName: string): Promise<void> {
  const stack = await loadStack();
  execFileSync(stackScriptPath, ['start-service', serviceName], {
    cwd: repoRoot,
    env: {
      ...process.env,
      DAGU_E2E_STATE_DIR: stack.stateDir,
      DAGU_E2E_BIN: stack.binPath,
    },
    stdio: 'pipe',
  });
}

export function killProcess(
  pid: number | string,
  signal: 'TERM' | 'KILL' = 'KILL'
): void {
  try {
    execFileSync('kill', [`-${signal}`, `${pid}`], {
      cwd: repoRoot,
      stdio: 'pipe',
    });
  } catch (error) {
    if (isNoSuchProcessError(error)) {
      return;
    }
    throw error;
  }
}

export async function getLatestLocalRunStatusRecord(
  dagName: string,
  dagRunId: string
): Promise<LocalRunStatusRecord | null> {
  const stack = await loadStack();
  const dagRunsDir = path.join(stack.stateDir, 'local/runtime/data/dag-runs', dagName);

  let output = '';
  try {
    output = execFileSync('find', [dagRunsDir, '-name', 'status.jsonl'], {
      cwd: repoRoot,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    });
  } catch {
    return null;
  }

  const statusFiles = output
    .split('\n')
    .map((file) => file.trim())
    .filter(Boolean);

  for (const statusFile of statusFiles) {
    const lines = (await fs.readFile(statusFile, 'utf8'))
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean);

    for (let index = lines.length - 1; index >= 0; index -= 1) {
      try {
        const record = JSON.parse(lines[index]) as LocalRunStatusRecord;
        if (record.dagRunId === dagRunId) {
          return record;
        }
      } catch {
        continue;
      }
    }
  }

  return null;
}

function authHeaders(token: string): Record<string, string> {
  return {
    Authorization: `Bearer ${token}`,
    'Content-Type': 'application/json',
  };
}

function isNoSuchProcessError(error: unknown): boolean {
  if (!(error instanceof Error)) {
    return false;
  }

  const { status, stderr } = error as ExecFileSyncError;
  const stderrText =
    typeof stderr === 'string' ? stderr : Buffer.isBuffer(stderr) ? stderr.toString('utf8') : '';

  return status === 1 && /no such process/i.test(stderrText);
}

function normalizeDAGRunDetails(run: RawDAGRunDetails): DAGRunDetails {
  const status = normalizeRunStatus(run.status, run.statusLabel);
  return {
    dagRunId: run.dagRunId,
    name: run.name,
    status,
    statusCode: run.status,
    statusLabel: status,
    workerId: run.workerId,
    sourceFileName: run.sourceFileName,
  };
}

function normalizeRunStatus(statusCode: number, statusLabel?: string): RunStatusLabel {
  if (statusLabel) {
    return statusLabel as RunStatusLabel;
  }

  switch (statusCode) {
    case 0:
      return 'not_started';
    case 1:
      return 'running';
    case 2:
      return 'failed';
    case 3:
      return 'aborted';
    case 4:
      return 'succeeded';
    case 5:
      return 'queued';
    case 6:
      return 'partially_succeeded';
    case 7:
      return 'waiting';
    case 8:
      return 'rejected';
    default:
      throw new Error(`unknown DAG run status code: ${statusCode}`);
  }
}
