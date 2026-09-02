import { useState, useEffect, useContext } from 'react';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import {
  components,
  CreateRemoteNodeRequestAuthType,
  UpdateRemoteNodeRequestAuthType,
} from '@/api/v1/schema';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Switch } from '@/components/ui/switch';
import { AlertCircle, Check, Plus, X } from 'lucide-react';
import { I18nText } from '@/i18n/I18nText';
import { I18nProps } from '@/i18n/I18nProps';

type RemoteNodeResponse = components['schemas']['RemoteNodeResponse'];

type RemoteNodeFormModalProps = {
  open: boolean;
  node?: RemoteNodeResponse;
  onClose: () => void;
  onSuccess: () => void;
};

export function RemoteNodeFormModal({
  open,
  node,
  onClose,
  onSuccess,
}: RemoteNodeFormModalProps) {
  const client = useClient();
  const appBarContext = useContext(AppBarContext);
  const isEditing = !!node;

  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [apiBaseUrl, setApiBaseUrl] = useState('');
  const [authType, setAuthType] = useState<string>('none');
  const [basicAuthUsername, setBasicAuthUsername] = useState('');
  const [basicAuthPassword, setBasicAuthPassword] = useState('');
  const [authToken, setAuthToken] = useState('');
  const [skipTlsVerify, setSkipTlsVerify] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);

  useEffect(() => {
    if (node) {
      setName(node.name);
      setDescription(node.description || '');
      setApiBaseUrl(node.apiBaseUrl);
      setAuthType(node.authType);
      setSkipTlsVerify(node.skipTlsVerify || false);
      setBasicAuthUsername('');
      setBasicAuthPassword('');
      setAuthToken('');
    } else {
      setName('');
      setDescription('');
      setApiBaseUrl('');
      setAuthType('none');
      setBasicAuthUsername('');
      setBasicAuthPassword('');
      setAuthToken('');
      setSkipTlsVerify(false);
    }
    setError(null);
  }, [node, open]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);

    if (!name.trim()) {
      setError('Name is required');
      return;
    }

    if (!apiBaseUrl.trim()) {
      setError('API Base URL is required');
      return;
    }

    if (!/^https?:\/\//.test(apiBaseUrl)) {
      setError('API Base URL must start with http:// or https://');
      return;
    }

    setIsLoading(true);

    try {
      const remoteNode = appBarContext.selectedRemoteNode || 'local';

      if (isEditing) {
        const body: {
          name?: string;
          description?: string;
          apiBaseUrl?: string;
          authType?: UpdateRemoteNodeRequestAuthType;
          basicAuthUsername?: string;
          basicAuthPassword?: string;
          authToken?: string;
          skipTlsVerify?: boolean;
        } = {
          name,
          description,
          apiBaseUrl,
          authType: authType as UpdateRemoteNodeRequestAuthType,
          skipTlsVerify,
        };
        if (authType === 'basic') {
          if (basicAuthUsername) body.basicAuthUsername = basicAuthUsername;
          if (basicAuthPassword) body.basicAuthPassword = basicAuthPassword;
        } else if (authType === 'token') {
          if (authToken) body.authToken = authToken;
        }

        const { error: patchError } = await client.PATCH(
          '/remote-nodes/{remoteNodeId}',
          {
            params: {
              path: { remoteNodeId: node.id },
              query: { remoteNode },
            },
            body,
          }
        );
        if (patchError) {
          throw new Error(patchError.message || 'Failed to update remote node');
        }
      } else {
        const body: {
          name: string;
          description?: string;
          apiBaseUrl: string;
          authType: CreateRemoteNodeRequestAuthType;
          basicAuthUsername?: string;
          basicAuthPassword?: string;
          authToken?: string;
          skipTlsVerify: boolean;
        } = {
          name,
          description,
          apiBaseUrl,
          authType: authType as CreateRemoteNodeRequestAuthType,
          skipTlsVerify,
        };
        if (authType === 'basic') {
          body.basicAuthUsername = basicAuthUsername;
          body.basicAuthPassword = basicAuthPassword;
        } else if (authType === 'token') {
          body.authToken = authToken;
        }

        const { error: postError } = await client.POST('/remote-nodes', {
          params: { query: { remoteNode } },
          body,
        });
        if (postError) {
          throw new Error(postError.message || 'Failed to create remote node');
        }
      }

      onSuccess();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Operation failed');
    } finally {
      setIsLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(isOpen) => !isOpen && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {isEditing ? (
              <I18nText text={'Edit Remote Node'} />
            ) : (
              <I18nText text={'Add Remote Node'} />
            )}
          </DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="space-y-4 mt-2">
          {error && (
            <div className="flex items-center gap-2 p-3 text-sm text-destructive bg-destructive/10 rounded-md">
              <AlertCircle className="h-4 w-4 flex-shrink-0" />
              <span>{error}</span>
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="name" className="text-sm">
              <I18nText text={'Name'} />
            </Label>
            <I18nProps>
              <Input
                id="name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
                autoComplete="off"
                className="h-9"
                placeholder="e.g. production-server"
              />
            </I18nProps>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="description" className="text-sm">
              <I18nText text={'Description'} />
            </Label>
            <I18nProps>
              <Input
                id="description"
                type="text"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                autoComplete="off"
                className="h-9"
                placeholder="Optional description"
              />
            </I18nProps>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="apiBaseUrl" className="text-sm">
              <I18nText text={'API Base URL'} />
            </Label>
            <I18nProps>
              <Input
                id="apiBaseUrl"
                type="text"
                value={apiBaseUrl}
                onChange={(e) => setApiBaseUrl(e.target.value)}
                required
                autoComplete="off"
                className="h-9"
                placeholder="https://dagu.example.com:8080/api/v1"
              />
            </I18nProps>
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="authType" className="text-sm">
              <I18nText text={'Authentication'} />
            </Label>
            <Select value={authType} onValueChange={setAuthType}>
              <SelectTrigger className="h-9">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">
                  <I18nText text={'None'} />
                </SelectItem>
                <SelectItem value="basic">
                  <I18nText text={'Basic Auth'} />
                </SelectItem>
                <SelectItem value="token">
                  <I18nText text={'Bearer Token'} />
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          {authType === 'basic' && (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="basicAuthUsername" className="text-sm">
                  <I18nText text={'Username'} />
                </Label>
                <I18nProps>
                  <Input
                    id="basicAuthUsername"
                    type="text"
                    value={basicAuthUsername}
                    onChange={(e) => setBasicAuthUsername(e.target.value)}
                    autoComplete="off"
                    className="h-9"
                    placeholder={isEditing ? '(unchanged)' : ''}
                  />
                </I18nProps>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="basicAuthPassword" className="text-sm">
                  <I18nText text={'Password'} />
                </Label>
                <I18nProps>
                  <Input
                    id="basicAuthPassword"
                    type="password"
                    value={basicAuthPassword}
                    onChange={(e) => setBasicAuthPassword(e.target.value)}
                    autoComplete="new-password"
                    className="h-9"
                    placeholder={isEditing ? '(unchanged)' : ''}
                  />
                </I18nProps>
              </div>
            </>
          )}

          {authType === 'token' && (
            <div className="space-y-1.5">
              <Label htmlFor="authToken" className="text-sm">
                <I18nText text={'Token'} />
              </Label>
              <I18nProps>
                <Input
                  id="authToken"
                  type="password"
                  value={authToken}
                  onChange={(e) => setAuthToken(e.target.value)}
                  autoComplete="off"
                  className="h-9"
                  placeholder={isEditing ? '(unchanged)' : ''}
                />
              </I18nProps>
            </div>
          )}

          <div className="flex items-center gap-2">
            <Switch
              id="skipTlsVerify"
              checked={skipTlsVerify}
              onCheckedChange={setSkipTlsVerify}
            />
            <Label htmlFor="skipTlsVerify" className="text-sm cursor-pointer">
              <I18nText text={'Skip TLS verification'} />
            </Label>
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              <X className="h-4 w-4" />
              <I18nText text={'Cancel'} />
            </Button>
            <Button type="submit" disabled={isLoading}>
              {isEditing ? (
                <Check className="h-4 w-4" />
              ) : (
                <Plus className="h-4 w-4" />
              )}
              {isLoading ? (
                <I18nText text={'Saving...'} />
              ) : isEditing ? (
                <I18nText text={'Update'} />
              ) : (
                <I18nText text={'Create'} />
              )}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
