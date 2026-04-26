import { Button } from '@/components/ui/button';
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Check, RotateCcw, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { components } from '../../../../api/v1/schema';
import PushBackHistory from '../common/PushBackHistory';

type Step = components['schemas']['Step'];
type PushBackHistoryEntry = components['schemas']['PushBackHistoryEntry'];

type Props = {
  visible: boolean;
  dismissModal: () => void;
  step: Step;
  pushBackHistory?: PushBackHistoryEntry[];
  onApprove?: (inputs: Record<string, string>) => Promise<void>;
  onPushBack?: (inputs: Record<string, string>) => Promise<void>;
};

export function StepReviewModal({
  visible,
  dismissModal,
  step,
  pushBackHistory,
  onApprove,
  onPushBack,
}: Props) {
  const [inputs, setInputs] = useState<Record<string, string>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const inputFields = step.approval?.input || [];
  const requiredFields = step.approval?.required || [];

  const isValid =
    onApprove ||
    requiredFields.every(
      (field) => inputs[field] && inputs[field].trim() !== ''
    );

  useEffect(() => {
    if (visible) {
      setInputs({});
      setError(null);
      setLoading(false);
    }
  }, [visible]);

  const handleAction = async (
    action: (inputs: Record<string, string>) => Promise<void>,
    errorLabel: string
  ) => {
    if (!isValid) {
      setError('Please fill in all required fields');
      return;
    }
    setLoading(true);
    setError(null);
    try {
      await action(inputs);
      dismissModal();
    } catch (e) {
      const err = e as { message?: string };
      setError(err.message || `Failed to ${errorLabel} step`);
    } finally {
      setLoading(false);
    }
  };

  const handleSubmit = () => {
    if (onApprove) {
      handleAction(onApprove, 'approve');
    } else if (onPushBack) {
      handleAction(onPushBack, 'retry');
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey && !loading) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const actionLabel = onApprove ? 'Approve' : 'Retry';
  const actionIcon = onApprove ? (
    <Check className="h-4 w-4" />
  ) : (
    <RotateCcw className="h-4 w-4" />
  );
  const actionVariant = onApprove ? ('primary' as const) : ('outline' as const);

  return (
    <Dialog open={visible} onOpenChange={dismissModal}>
      <DialogContent className="sm:max-w-[450px]">
        <DialogHeader>
          <DialogTitle className="text-base">
            {actionLabel}: {step.name}
          </DialogTitle>
        </DialogHeader>

        <div className="py-2 space-y-3" onKeyDown={handleKeyDown}>
          {onPushBack && pushBackHistory && pushBackHistory.length > 0 && (
            <PushBackHistory
              history={pushBackHistory}
              title="Previous Push-backs"
            />
          )}

          {inputFields.length > 0 && !onApprove && (
            <div className="space-y-3">
              {inputFields.map((field) => {
                const isRequired = requiredFields.includes(field);
                const fieldId = `review-input-${field}`;
                return (
                  <div key={field}>
                    <label
                      htmlFor={fieldId}
                      className="block text-sm font-medium mb-1"
                    >
                      {field}
                      {isRequired && <span className="text-error ml-1">*</span>}
                    </label>
                    <input
                      id={fieldId}
                      type="text"
                      value={inputs[field] || ''}
                      onChange={(e) =>
                        setInputs((prev) => ({
                          ...prev,
                          [field]: e.target.value,
                        }))
                      }
                      className="w-full px-3 py-1 text-sm border border-border rounded bg-background focus:outline-none focus:border-ring"
                      placeholder={`Enter ${field}`}
                    />
                  </div>
                );
              })}
            </div>
          )}

          {error && (
            <div className="text-sm text-error bg-error-muted p-2 rounded">
              {error}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button
            size="sm"
            variant="ghost"
            onClick={dismissModal}
            disabled={loading}
          >
            <X className="h-4 w-4" />
            Cancel
          </Button>
          <Button
            size="sm"
            variant={actionVariant}
            onClick={handleSubmit}
            disabled={loading || (!isValid && requiredFields.length > 0)}
          >
            {actionIcon}
            {loading ? `${actionLabel}...` : actionLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
