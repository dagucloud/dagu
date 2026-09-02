// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Ban, X } from 'lucide-react';
import { useState } from 'react';
import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { useErrorModal } from '@/components/ui/error-modal';
import { useSimpleToast } from '@/components/ui/simple-toast';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dagName: string;
  dagRunId: string;
  stepName: string;
  onSettled: () => void;
};

export function RejectDAGRunDialog({
  open,
  onOpenChange,
  dagName,
  dagRunId,
  stepName,
  onSettled,
}: Props) {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const { showError } = useErrorModal();
  const { showToast } = useSimpleToast();
  const [reason, setReason] = useState('');

  const handleOpenChange = (nextOpen: boolean) => {
    if (!nextOpen) {
      setReason('');
    }
    onOpenChange(nextOpen);
  };

  const reject = async () => {
    const rejectionReason = reason || undefined;
    handleOpenChange(false);
    try {
      const { error } = await client.POST(
        '/dag-runs/{name}/{dagRunId}/steps/{stepName}/reject',
        {
          params: {
            path: { name: dagName, dagRunId, stepName },
            query: { remoteNode },
          },
          body: { reason: rejectionReason },
        }
      );

      if (error) {
        showError(
          'Failed to reject DAG run',
          error.message || `Failed to reject: ${stepName}`
        );
      } else {
        showToast('DAG run rejected');
      }
    } catch (error) {
      showError(
        'Failed to reject DAG run',
        error instanceof Error ? error.message : 'Network request failed'
      );
    } finally {
      onSettled();
    }
  };

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-[450px]">
        <DialogHeader>
          <DialogTitle><I18nText text={"Reject DAG Run"} /></DialogTitle>
        </DialogHeader>
        <div className="py-2">
          <label htmlFor="rejection-reason" className="sr-only">
            <I18nText text={"Rejection reason (optional)"} />
          </label>
          <I18nProps><textarea
            id="rejection-reason"
            className="w-full px-3 py-1 text-sm border border-border rounded bg-background focus:outline-none focus:border-ring resize-none"
            placeholder="Reason (optional)..."
            rows={2}
            value={reason}
            onChange={(event) => setReason(event.target.value)}
          /></I18nProps>
        </div>
        <DialogFooter>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => handleOpenChange(false)}
          >
            <X className="h-4 w-4" /> <I18nText text={"Cancel"} />
          </Button>
          <Button variant="default" size="sm" onClick={() => void reject()}>
            <Ban className="h-4 w-4" /> <I18nText text={"Reject"} />
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
