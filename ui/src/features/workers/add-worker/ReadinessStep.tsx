// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  BuiltinTLSReason,
  ConfigSourceKind,
  HostEnvironment,
  WorkerAccessReason,
  type components,
} from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import { CommandBlock, ReasonLinks, reasonText } from './parts';

type CoordinatorReadiness = components['schemas']['CoordinatorJoinReadiness'];
type Report = components['schemas']['JoinReadinessReport'];

const ALL_INTERFACES = '0.0.0.0';
const DEFAULT_HOST_VARIABLE = 'DAGU_COORDINATOR_HOST';

const CHART_README_URL =
  'https://github.com/dagucloud/dagu/blob/main/charts/dagu/README.md#distributed';

/** Whether every coordinator reports that a worker on another host can join. */
export function allReady(coordinators: CoordinatorReadiness[]): boolean {
  return coordinators.length > 0 && coordinators.every((c) => c.report?.ready);
}

/**
 * Step 1: for each coordinator that is not ready, the change to make for how
 * Dagu was started. A container or a pod keeps its port reminder when ready,
 * since Dagu cannot see what is published outside it.
 */
export function ReadinessBody({
  coordinators,
}: {
  coordinators: CoordinatorReadiness[];
}) {
  const named = coordinators.length > 1;
  return (
    <>
      {coordinators.map((c) => (
        <div key={c.instanceId} className="space-y-2 text-sm">
          {named && (
            <p className="font-mono text-xs text-muted-foreground">
              {c.host}:{c.port}
            </p>
          )}
          {c.report ? (
            <ReportBody report={c.report} />
          ) : (
            <p>
              <I18nText
                text={
                  reasonText(c.errorReason) ??
                  'This coordinator did not report. Start or upgrade it, then check again.'
                }
              />
              <ReasonLinks reason={c.errorReason} />
            </p>
          )}
        </div>
      ))}
    </>
  );
}

function ReportBody({ report }: { report: Report }) {
  const port = String(report.port);
  const environment = report.environment ?? HostEnvironment.host;
  return (
    <>
      {report.loopbackOnly && <ListenChange report={report} />}
      {!report.builtinTLS && <CAChange reason={report.builtinTLSReason} />}
      {!report.ready && (
        <p className="text-muted-foreground">
          <I18nText text="Each change needs a restart of Dagu. This dialog checks again when the server is back; if it does not, use Check again." />
        </p>
      )}
      {/* The container's listen change already publishes the port. */}
      {environment === HostEnvironment.container && !report.loopbackOnly && (
        <p className="text-muted-foreground">
          <I18nText
            text="Publish port {port} for the container, as with -p {port}:{port}. Dagu cannot see which ports a container publishes."
            values={{ port }}
          />
        </p>
      )}
      {environment === HostEnvironment.kubernetes && (
        <p className="text-muted-foreground">
          <I18nText text="Workers outside the cluster need the coordinator Service to be of type LoadBalancer or NodePort, as the chart's coordinator.service.type sets." />
        </p>
      )}
    </>
  );
}

// The listen change for how Dagu was started. coordinator.host comes from
// a flag, else a variable, else a config file, so a container's -e wins
// over a file but not over a flag.
function ListenChange({ report }: { report: Report }) {
  const port = String(report.port);
  const source = report.hostSource;
  const listens = (
    <p>
      <I18nText
        text="The coordinator listens on {host}, so only this machine can connect."
        values={{ host: report.listenHost }}
      />
    </p>
  );
  const sourceChange = <SourceChange report={report} />;
  if (report.environment === HostEnvironment.kubernetes) {
    // A flag or a variable that the pod sets wins.
    const pinned =
      source?.kind === ConfigSourceKind.flag ||
      source?.kind === ConfigSourceKind.env;
    return (
      <>
        {listens}
        {pinned ? (
          sourceChange
        ) : (
          // T16 sees a pod, not how it was deployed, so the chart is one
          // case. An upgrade cannot change an existing claim's access mode.
          <p>
            <I18nText text="Set DAGU_COORDINATOR_HOST to 0.0.0.0 on the coordinator's workload, then restart it. A coordinator in its own pod needs storage that every Dagu pod can write. With the Helm chart, use deploymentMode: distributed, which sets the variable. Move the data to a ReadWriteMany claim, then set persistence.existingClaim to it and persistence.accessMode to ReadWriteMany before you upgrade." />{' '}
            <a
              href={CHART_README_URL}
              target="_blank"
              rel="noreferrer"
              className="underline underline-offset-2"
            >
              README
            </a>
          </p>
        )}
      </>
    );
  }
  if (report.environment === HostEnvironment.container) {
    const flag = source?.kind === ConfigSourceKind.flag;
    const variable =
      source?.kind === ConfigSourceKind.env && source.name
        ? source.name
        : DEFAULT_HOST_VARIABLE;
    return (
      <>
        {listens}
        {flag && sourceChange}
        <p>
          <I18nText text="Recreate the container with these options:" />
        </p>
        <CommandBlock
          text={
            flag
              ? `-p ${port}:${port}`
              : `-p ${port}:${port} -e ${variable}=${ALL_INTERFACES}`
          }
          label="Copy the change"
        />
      </>
    );
  }
  return (
    <>
      {listens}
      {sourceChange}
    </>
  );
}

// Where coordinator.host came from, and the change there.
function SourceChange({ report }: { report: Report }) {
  const source = report.hostSource;
  if (
    source?.name &&
    (source.kind === ConfigSourceKind.env ||
      source.kind === ConfigSourceKind.flag)
  ) {
    return (
      <>
        <p>
          <I18nText
            text={
              source.kind === ConfigSourceKind.env
                ? 'The variable {name} sets coordinator.host. Set it to 0.0.0.0 where Dagu starts:'
                : 'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts:'
            }
            values={{ name: source.name }}
          />
        </p>
        <CommandBlock
          text={`${source.name}=${ALL_INTERFACES}`}
          label="Copy the change"
        />
      </>
    );
  }
  // The file that set the value, such as admin.yaml over config.yaml, else
  // the config file Dagu reads. The key goes into its coordinator block, so
  // nothing is offered to append.
  const file =
    source?.kind === ConfigSourceKind.file && source.name
      ? source.name
      : report.configFile;
  return (
    <p>
      <I18nText
        text="In {file}, set host to 0.0.0.0 inside the coordinator block, adding the block if the file has none."
        values={{ file }}
      />
    </p>
  );
}

function CAChange({ reason }: { reason?: BuiltinTLSReason }) {
  const access =
    reason === BuiltinTLSReason.ownCertificates
      ? WorkerAccessReason.own_certificates
      : reason === BuiltinTLSReason.caUnavailable
        ? WorkerAccessReason.ca_unavailable
        : undefined;
  return (
    <p>
      <I18nText
        text={
          reasonText(access) ??
          'Invites need the built-in CA, which this coordinator does not have.'
        }
      />
      <ReasonLinks reason={access} />
    </p>
  );
}
