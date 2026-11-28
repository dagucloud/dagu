// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  BuiltinTLSReason,
  ConfigSourceKind,
  HostEnvironment,
  type components,
} from '@/api/v1/schema';
import { I18nText } from '@/i18n/I18nText';
import { CommandBlock } from './parts';

type CoordinatorReadiness = components['schemas']['CoordinatorJoinReadiness'];
type Report = components['schemas']['JoinReadinessReport'];

const ALL_INTERFACES = '0.0.0.0';

const CONFIG_LINES = `coordinator:\n  host: ${ALL_INTERFACES}`;
const CHART_VALUES = `extraEnv:\n  - name: DAGU_COORDINATOR_HOST\n    value: "${ALL_INTERFACES}"`;

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
                text="This coordinator did not report: {error}"
                values={{ error: c.error ?? '' }}
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
          <I18nText text="Each change needs a restart of Dagu. This dialog checks again when the server is back." />
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
  if (report.environment === HostEnvironment.container) {
    return (
      <>
        {listens}
        <p>
          <I18nText text="Recreate the container with these options:" />
        </p>
        <CommandBlock
          text={`-p ${port}:${port} -e DAGU_COORDINATOR_HOST=${ALL_INTERFACES}`}
          label="Copy the options"
        />
      </>
    );
  }
  if (
    source?.name &&
    (source.kind === ConfigSourceKind.env ||
      source.kind === ConfigSourceKind.flag)
  ) {
    return (
      <>
        {listens}
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
          label="Copy the setting"
        />
      </>
    );
  }
  return (
    <>
      {listens}
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
  switch (reason) {
    case BuiltinTLSReason.ownCertificates:
      return (
        <p>
          <I18nText text="Invites need the built-in CA, but this coordinator uses peer.cert_file and peer.key_file. Remove both to use invites, or connect each worker with your own certificates." />
        </p>
      );
    case BuiltinTLSReason.caUnavailable:
      return (
        <p>
          <I18nText text="Invites need the built-in CA, but this coordinator could not create or read it. Its start warning names the cause." />
        </p>
      );
    default:
      return (
        <p>
          <I18nText text="Invites need the built-in CA, which this coordinator does not have." />
        </p>
      );
  }
}
