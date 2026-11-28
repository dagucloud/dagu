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
import { CommandBlock, reasonText } from './parts';

type CoordinatorReadiness = components['schemas']['CoordinatorJoinReadiness'];
type Report = components['schemas']['JoinReadinessReport'];

const ALL_INTERFACES = '0.0.0.0';
const DEFAULT_HOST_VARIABLE = 'DAGU_COORDINATOR_HOST';

const CONFIG_LINES = `coordinator:\n  host: ${ALL_INTERFACES}`;
const CHART_VALUES = `extraEnv:\n  - name: ${DEFAULT_HOST_VARIABLE}\n    value: "${ALL_INTERFACES}"`;

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

// The listen change for how Dagu was started. In a container, the port
// must be published too, and a flag or a file that sets coordinator.host
// wins over the variable, so the change names it.
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
  if (report.environment === HostEnvironment.kubernetes) {
    return (
      <>
        {listens}
        <p>
          <I18nText text="Set coordinator.host to 0.0.0.0 through the chart's values, then upgrade the release:" />
        </p>
        <CommandBlock text={CHART_VALUES} label="Copy the values" />
      </>
    );
  }
  const sourceChange = <SourceChange report={report} />;
  if (report.environment === HostEnvironment.container) {
    const variable =
      source?.kind === ConfigSourceKind.env && source.name
        ? source.name
        : DEFAULT_HOST_VARIABLE;
    const setsHost =
      source?.kind === ConfigSourceKind.flag ||
      source?.kind === ConfigSourceKind.file;
    return (
      <>
        {listens}
        {setsHost && sourceChange}
        <p>
          <I18nText text="Recreate the container with these options:" />
        </p>
        <CommandBlock
          text={
            setsHost
              ? `-p ${port}:${port}`
              : `-p ${port}:${port} -e ${variable}=${ALL_INTERFACES}`
          }
          label="Copy the options"
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
                : 'The flag {name} sets coordinator.host. Give it 0.0.0.0 where Dagu starts, or remove it:'
            }
            values={{ name: source.name }}
          />
        </p>
        <CommandBlock
          text={`${source.name}=${ALL_INTERFACES}`}
          label="Copy the setting"
        />
      </>
    );
  }
  // The file that set the value wins over the default config file, as
  // admin.yaml does over config.yaml.
  if (source?.kind === ConfigSourceKind.file && source.name) {
    return (
      <>
        <p>
          <I18nText
            text="{file} sets coordinator.host. Change it there to 0.0.0.0:"
            values={{ file: source.name }}
          />
        </p>
        <CommandBlock text={CONFIG_LINES} label="Copy the lines" />
      </>
    );
  }
  return (
    <>
      <p>
        <I18nText
          text="Add these lines to {file}:"
          values={{ file: report.configFile }}
        />
      </p>
      <CommandBlock text={CONFIG_LINES} label="Copy the lines" />
    </>
  );
}

function CAChange({ reason }: { reason?: BuiltinTLSReason }) {
  const text =
    reason === BuiltinTLSReason.ownCertificates
      ? reasonText(WorkerAccessReason.own_certificates)
      : reason === BuiltinTLSReason.caUnavailable
        ? reasonText(WorkerAccessReason.ca_unavailable)
        : undefined;
  return (
    <p>
      <I18nText
        text={
          text ??
          'Invites need the built-in CA, which this coordinator does not have.'
        }
      />
    </p>
  );
}
