// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import { useQuery } from '@/hooks/api';
import { AlertTriangle, Info } from 'lucide-react';
import { useEffect, useMemo } from 'react';
import { Link } from 'react-router-dom';
import { parse as parseYAML } from 'yaml';
import { DagStatusChip } from './DagStatusChip';
import { useWikiLive } from './context';
import { useI18n } from '@/i18n/I18nProvider';
import { I18nText } from '@/i18n/I18nText';
import { I18nTemplate } from '@/i18n/I18nTemplate';

type DAGDetails = components['schemas']['DAGDetails'];

const SECTIONS = [
  'overview',
  'params',
  'schedule',
  'steps',
  'preconditions',
] as const;
type Section = (typeof SECTIONS)[number];
const DEFAULT_SECTIONS: Section[] = ['overview', 'params', 'schedule'];

type BlockConfig = {
  dag: string;
  sections: Section[];
};

function parseBlock(source: string): BlockConfig | string {
  let raw: unknown;
  try {
    raw = parseYAML(source);
  } catch {
    return 'Invalid YAML in dagu-info block';
  }
  if (typeof raw !== 'object' || raw === null) {
    return 'dagu-info block must be a YAML mapping with a dag key';
  }
  const record = raw as Record<string, unknown>;
  const dag = record.dag;
  if (typeof dag !== 'string' || dag.trim() === '') {
    return 'dagu-info block requires a dag name';
  }
  let sections = DEFAULT_SECTIONS;
  if (record.sections !== undefined) {
    if (
      !Array.isArray(record.sections) ||
      record.sections.some(
        (s) => typeof s !== 'string' || !SECTIONS.includes(s as Section)
      )
    ) {
      return `sections must be a list of: ${SECTIONS.join(', ')}`;
    }
    sections = record.sections as Section[];
  }
  return { dag: dag.trim(), sections };
}

function ErrorCard({ message }: { message: string }) {
  return (
    <div className="my-2 flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-xs text-destructive">
      <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
      {message}
    </div>
  );
}

function InfoTable({ rows }: { rows: [string, React.ReactNode][] }) {
  const visible = rows.filter(([, value]) => value !== null && value !== '');
  if (visible.length === 0) return null;
  return (
    <table className="w-full text-xs">
      <tbody>
        {visible.map(([key, value]) => (
          <tr key={key} className="border-b border-border last:border-b-0">
            <td className="py-1 pr-3 align-top text-muted-foreground whitespace-nowrap">
              <I18nText text={key} />
            </td>
            <td className="py-1 break-words">{value}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <div className="mt-2 mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
      {children}
    </div>
  );
}

function DagDetailsSections({
  fileName,
  dagRef,
  sections,
  remoteNode,
}: {
  fileName: string;
  dagRef: string;
  sections: Section[];
  remoteNode: string;
}) {
  const { ts } = useI18n();
  const { data } = useQuery('/dags/{fileName}', {
    params: { path: { fileName }, query: { remoteNode } },
  });
  const dag: DAGDetails | undefined = data?.dag;
  if (!dag) return null;

  const buildErrors = data?.errors ?? [];

  return (
    <>
      {buildErrors.length > 0 && (
        <ErrorCard message={`Definition has errors: ${buildErrors[0]}`} />
      )}
      {sections.includes('overview') && (
        <div>
          <SectionTitle>
            <I18nText text={'Overview'} />
          </SectionTitle>
          <InfoTable
            rows={[
              ['Description', dag.description ?? ''],
              ['Group', dag.group ?? ''],
              ['Type', dag.type ?? ''],
              ['Labels', (dag.labels ?? []).join(', ')],
            ]}
          />
        </div>
      )}
      {sections.includes('params') && (dag.paramDefs?.length ?? 0) > 0 && (
        <div>
          <SectionTitle>
            <I18nText text={'Parameters'} />
          </SectionTitle>
          <table className="w-full text-xs">
            <thead>
              <tr className="border-b border-border text-left text-muted-foreground">
                <th className="py-1 pr-3 font-normal">
                  <I18nText text={'Name'} />
                </th>
                <th className="py-1 pr-3 font-normal">
                  <I18nText text={'Type'} />
                </th>
                <th className="py-1 pr-3 font-normal">
                  <I18nText text={'Default'} />
                </th>
                <th className="py-1 font-normal">
                  <I18nText text={'Description'} />
                </th>
              </tr>
            </thead>
            <tbody>
              {dag.paramDefs?.map((param, i) => (
                <tr
                  key={param.name ?? i}
                  className="border-b border-border last:border-b-0"
                >
                  <td className="py-1 pr-3 font-mono">
                    {param.name ?? `$${i + 1}`}
                    {param.required ? ' *' : ''}
                  </td>
                  <td className="py-1 pr-3">{param.type}</td>
                  <td className="py-1 pr-3 font-mono">
                    {param.default !== undefined ? String(param.default) : ''}
                  </td>
                  <td className="py-1">{param.description ?? ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {sections.includes('params') &&
        (dag.paramDefs?.length ?? 0) === 0 &&
        dag.defaultParams && (
          <div>
            <SectionTitle>
              <I18nText text={'Parameters'} />
            </SectionTitle>
            <code className="text-xs">{dag.defaultParams}</code>
          </div>
        )}
      {sections.includes('schedule') && (dag.schedule?.length ?? 0) > 0 && (
        <div>
          <SectionTitle>
            <I18nText text={'Schedule'} />
          </SectionTitle>
          <div className="text-xs font-mono">
            {dag.schedule?.map((s, i) => (
              <div key={i}>{s.expression}</div>
            ))}
          </div>
        </div>
      )}
      {sections.includes('steps') && (dag.steps?.length ?? 0) > 0 && (
        <div>
          <SectionTitle>
            <I18nText text={'Steps'} />
          </SectionTitle>
          <table className="w-full text-xs">
            <tbody>
              {dag.steps?.map((step) => (
                <tr
                  key={step.name}
                  className="border-b border-border last:border-b-0"
                >
                  <td className="py-1 pr-3 font-mono whitespace-nowrap">
                    {step.name}
                  </td>
                  <td className="py-1 pr-3 text-muted-foreground">
                    {(step.depends ?? []).length > 0
                      ? ts('after {steps}', {
                          steps: (step.depends ?? []).join(', '),
                        })
                      : ''}
                  </td>
                  <td className="py-1">{step.description ?? ''}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {sections.includes('preconditions') &&
        (dag.preconditions?.length ?? 0) > 0 && (
          <div>
            <SectionTitle>
              <I18nText text={'Preconditions'} />
            </SectionTitle>
            <div className="text-xs font-mono">
              {dag.preconditions?.map((c, i) => (
                <div key={i}>{c.condition}</div>
              ))}
            </div>
          </div>
        )}
      <div className="mt-1 text-[10px] text-muted-foreground">
        <Link
          to={`/dags/${encodeURIComponent(fileName)}`}
          className="hover:underline"
        >
          <I18nTemplate text="Open {dag} →" values={{ dag: dagRef }} />
        </Link>
      </div>
    </>
  );
}

type Props = {
  source: string;
};

/** Renders a ```dagu-info fenced block: live DAG definition details. */
export function DaguInfoBlock({ source }: Props) {
  const live = useWikiLive();
  const config = useMemo(() => parseBlock(source), [source]);
  const dagRef = typeof config === 'string' ? null : config.dag;

  useEffect(() => {
    if (!live || !dagRef) return;
    return live.registerRef(dagRef);
  }, [live, dagRef]);

  if (typeof config === 'string') {
    return <ErrorCard message={config} />;
  }
  if (!live) {
    return (
      <pre>
        <code>{source}</code>
      </pre>
    );
  }

  const result = live.lookup(config.dag);
  return (
    <div className="my-2 rounded-md border border-border px-3 py-2">
      <div className="flex items-center gap-1.5">
        <Info className="h-3.5 w-3.5 text-muted-foreground" />
        <span className="text-xs font-medium">{config.dag}</span>
        <DagStatusChip dagRef={config.dag} label="" />
      </div>
      {result.state === 'not-found' && (
        <div className="mt-1 text-xs text-muted-foreground">
          <I18nText text={'DAG not found in this workspace.'} />
        </div>
      )}
      {result.state === 'found' && (
        <DagDetailsSections
          fileName={result.fileName}
          dagRef={config.dag}
          sections={config.sections}
          remoteNode={live.remoteNode}
        />
      )}
    </div>
  );
}
