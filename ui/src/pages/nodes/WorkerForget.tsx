// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Button } from '@/components/ui/button';
import ConfirmDialog from '@/components/ui/confirm-dialog';
import { useClient } from '@/hooks/api';
import { I18nTemplate } from '@/i18n/I18nTemplate';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import React, { useState } from 'react';

/**
 * Forgets an offline worker node once the user confirms. The worker is
 * listed again when it connects.
 */
export function WorkerForget({
  name,
  onForgotten,
}: {
  name: string;
  onForgotten: () => void;
}): React.ReactElement {
  const client = useClient();
  const { ts } = useI18n();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);

  async function forget() {
    setConfirming(false);
    setBusy(true);
    setProblem(null);
    try {
      const { error } = await client.DELETE('/nodes/{nodeName}', {
        params: { path: { nodeName: name } },
      });
      if (error) {
        setProblem(error.message || ts("Couldn't forget the worker."));
        return;
      }
      onForgotten();
    } catch {
      setProblem(ts("Couldn't forget the worker."));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="space-y-3">
      <Button
        size="sm"
        variant="outline"
        className="text-error"
        disabled={busy}
        onClick={() => setConfirming(true)}
      >
        <I18nText text={'Forget worker'} />
      </Button>
      {problem && (
        <p role="alert" className="text-xs text-error">
          {problem}
        </p>
      )}
      {confirming && (
        <ConfirmDialog
          visible
          title={ts('Forget worker')}
          buttonText={ts('Forget worker')}
          initialFocus="cancel"
          dismissModal={() => setConfirming(false)}
          onSubmit={() => void forget()}
        >
          <p className="text-sm">
            <I18nTemplate
              text="Forget {name}? It is listed again when the worker connects."
              values={{ name: <strong>{name}</strong> }}
            />
          </p>
        </ConfirmDialog>
      )}
    </section>
  );
}
