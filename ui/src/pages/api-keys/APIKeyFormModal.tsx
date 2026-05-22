import { useState, useEffect, useContext } from 'react';
import { useConfig } from '@/contexts/ConfigContext';
import { TOKEN_KEY } from '@/contexts/AuthContext';
import { AppBarContext } from '@/contexts/AppBarContext';
import {
  APIKeyAllowedSurfaces,
  APIKeyAttributionClass,
  components,
  UserRole,
} from '@/api/v1/schema';
import {
  defaultWorkspaceAccess,
  normalizeWorkspaceAccess,
  WorkspaceAccessEditor,
} from '@/components/WorkspaceAccessEditor';
import { Button } from '@/components/ui/button';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog';
import { Copy, Check } from 'lucide-react';

type APIKey = components['schemas']['APIKey'];
type WorkspaceAccess = components['schemas']['WorkspaceAccess'];
type APIKeySurface = APIKeyAllowedSurfaces;

interface APIKeyFormModalProps {
  open: boolean;
  apiKey?: APIKey;
  onClose: () => void;
  onSuccess: () => void;
}

export function APIKeyFormModal({
  open,
  apiKey,
  onClose,
  onSuccess,
}: APIKeyFormModalProps) {
  const config = useConfig();
  const appBarContext = useContext(AppBarContext);
  const isEditing = !!apiKey;
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [role, setRole] = useState<UserRole>(UserRole.viewer);
  const [workspaceAccess, setWorkspaceAccess] = useState<WorkspaceAccess>(
    defaultWorkspaceAccess()
  );
  const [allowedSurfaces, setAllowedSurfaces] = useState<APIKeySurface[]>([
    APIKeyAllowedSurfaces.rest_api,
    APIKeyAllowedSurfaces.mcp,
  ]);
  const [attributionClass, setAttributionClass] =
    useState<APIKeyAttributionClass>(APIKeyAttributionClass.service_account);
  const [ownerUserId, setOwnerUserId] = useState('');
  const [serviceAccountName, setServiceAccountName] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [createdKey, setCreatedKey] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (open) {
      if (apiKey) {
        setName(apiKey.name);
        setDescription(apiKey.description || '');
        setRole(apiKey.role);
        setWorkspaceAccess(normalizeWorkspaceAccess(apiKey.workspaceAccess));
        setAllowedSurfaces(
          apiKey.allowedSurfaces?.length
            ? apiKey.allowedSurfaces
            : [APIKeyAllowedSurfaces.rest_api, APIKeyAllowedSurfaces.mcp]
        );
        setAttributionClass(
          apiKey.attributionClass || APIKeyAttributionClass.service_account
        );
        setOwnerUserId(apiKey.ownerUserId || '');
        setServiceAccountName(apiKey.serviceAccountName || apiKey.name);
      } else {
        setName('');
        setDescription('');
        setRole(UserRole.viewer);
        setWorkspaceAccess(defaultWorkspaceAccess());
        setAllowedSurfaces([
          APIKeyAllowedSurfaces.rest_api,
          APIKeyAllowedSurfaces.mcp,
        ]);
        setAttributionClass(APIKeyAttributionClass.service_account);
        setOwnerUserId('');
        setServiceAccountName('');
      }
      setError(null);
      setCreatedKey(null);
      setCopied(false);
    }
  }, [open, apiKey]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsLoading(true);
    setError(null);

    if (!workspaceAccess.all && workspaceAccess.grants.length === 0) {
      setError('Select at least one workspace');
      setIsLoading(false);
      return;
    }
    if (allowedSurfaces.length === 0) {
      setError('Select at least one accepted surface');
      setIsLoading(false);
      return;
    }
    if (
      attributionClass === APIKeyAttributionClass.user_owned &&
      ownerUserId.trim() === ''
    ) {
      setError('Owner user ID is required for user-owned keys');
      setIsLoading(false);
      return;
    }

    try {
      const token = localStorage.getItem(TOKEN_KEY);
      const remoteNode = appBarContext.selectedRemoteNode || 'local';
      const url = isEditing
        ? `${config.apiURL}/api-keys/${apiKey.id}?remoteNode=${remoteNode}`
        : `${config.apiURL}/api-keys?remoteNode=${remoteNode}`;

      const response = await fetch(url, {
        method: isEditing ? 'PATCH' : 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          name,
          description: description || undefined,
          role: workspaceAccess.all ? role : UserRole.viewer,
          workspaceAccess,
          allowedSurfaces,
          attributionClass,
          ownerUserId:
            attributionClass === APIKeyAttributionClass.user_owned
              ? ownerUserId.trim()
              : undefined,
          serviceAccountName:
            attributionClass === APIKeyAttributionClass.service_account
              ? serviceAccountName.trim() || undefined
              : undefined,
        }),
      });

      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        throw new Error(
          data.message || `Failed to ${isEditing ? 'update' : 'create'} API key`
        );
      }

      if (!isEditing) {
        const data = await response.json();
        setCreatedKey(data.key);
      } else {
        onSuccess();
        onClose();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'An error occurred');
    } finally {
      setIsLoading(false);
    }
  };

  const handleCopy = async () => {
    if (createdKey) {
      await navigator.clipboard.writeText(createdKey);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const handleDone = () => {
    setCreatedKey(null);
    onSuccess();
    onClose();
  };

  const toggleSurface = (surface: APIKeySurface, checked: boolean) => {
    setAllowedSurfaces((current) => {
      if (checked) {
        return current.includes(surface) ? current : [...current, surface];
      }
      return current.filter((item) => item !== surface);
    });
  };

  // Show the key after creation
  if (createdKey) {
    return (
      <Dialog open={open} onOpenChange={() => handleDone()}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>API Key Created</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="p-3 bg-warning/10 border border-warning/20 rounded-md">
              <p className="text-sm text-warning-foreground">
                Copy this key now. You won&apos;t be able to see it again!
              </p>
            </div>
            <div className="flex items-center gap-2">
              <code className="flex-1 p-2 text-sm bg-muted rounded-md break-all font-mono">
                {createdKey}
              </code>
              <Button variant="outline" size="icon" onClick={handleCopy}>
                {copied ? (
                  <Check className="h-4 w-4" />
                ) : (
                  <Copy className="h-4 w-4" />
                )}
              </Button>
            </div>
          </div>
          <DialogFooter>
            <Button onClick={handleDone}>Done</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    );
  }

  return (
    <Dialog open={open} onOpenChange={onClose}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            {isEditing ? 'Edit API Key' : 'Create API Key'}
          </DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit}>
          <div className="space-y-4 py-4">
            {error && (
              <div className="p-3 text-sm text-destructive bg-destructive/10 rounded-md">
                {error}
              </div>
            )}

            <div className="space-y-2">
              <Label htmlFor="name">Name</Label>
              <Input
                id="name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="My API Key"
                required
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="description">Description (optional)</Label>
              <Input
                id="description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="Used for CI/CD pipeline"
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="role">Role</Label>
              <Select
                value={workspaceAccess.all ? role : UserRole.viewer}
                onValueChange={(v) => setRole(v as UserRole)}
                disabled={!workspaceAccess.all}
              >
                <SelectTrigger>
                  <SelectValue placeholder="Select role" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="admin">Admin - Full access</SelectItem>
                  <SelectItem value="manager">
                    Manager - DAG CRUD, execution, and audit logs
                  </SelectItem>
                  <SelectItem value="developer">
                    Developer - DAG CRUD and execution
                  </SelectItem>
                  <SelectItem value="operator">
                    Operator - DAG execution only
                  </SelectItem>
                  <SelectItem value="viewer">
                    Viewer - Read-only access
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>

            <WorkspaceAccessEditor
              value={workspaceAccess}
              onChange={(next) => {
                setWorkspaceAccess(next);
                if (!next.all) {
                  setRole(UserRole.viewer);
                }
              }}
              workspaces={appBarContext.workspaces ?? []}
            />

            <div className="space-y-2">
              <Label>Accepted Surfaces</Label>
              <div className="grid gap-2 sm:grid-cols-2">
                <label className="flex items-center gap-2 rounded-md border border-border p-2 text-sm">
                  <Checkbox
                    checked={allowedSurfaces.includes(
                      APIKeyAllowedSurfaces.rest_api
                    )}
                    onCheckedChange={(checked) =>
                      toggleSurface(
                        APIKeyAllowedSurfaces.rest_api,
                        checked === true
                      )
                    }
                  />
                  <span>REST API</span>
                </label>
                <label className="flex items-center gap-2 rounded-md border border-border p-2 text-sm">
                  <Checkbox
                    checked={allowedSurfaces.includes(
                      APIKeyAllowedSurfaces.mcp
                    )}
                    onCheckedChange={(checked) =>
                      toggleSurface(APIKeyAllowedSurfaces.mcp, checked === true)
                    }
                  />
                  <span>MCP</span>
                </label>
              </div>
            </div>

            <div className="space-y-2">
              <Label htmlFor="attribution">Attribution</Label>
              <Select
                value={attributionClass}
                onValueChange={(value) =>
                  setAttributionClass(value as APIKeyAttributionClass)
                }
              >
                <SelectTrigger id="attribution">
                  <SelectValue placeholder="Select attribution" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={APIKeyAttributionClass.service_account}>
                    Service account
                  </SelectItem>
                  <SelectItem value={APIKeyAttributionClass.user_owned}>
                    User owned
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>

            {attributionClass === APIKeyAttributionClass.service_account ? (
              <div className="space-y-2">
                <Label htmlFor="serviceAccountName">Service Account Name</Label>
                <Input
                  id="serviceAccountName"
                  value={serviceAccountName}
                  onChange={(e) => setServiceAccountName(e.target.value)}
                  placeholder={name || 'ci-runner'}
                />
              </div>
            ) : (
              <div className="space-y-2">
                <Label htmlFor="ownerUserId">Owner User ID</Label>
                <Input
                  id="ownerUserId"
                  value={ownerUserId}
                  onChange={(e) => setOwnerUserId(e.target.value)}
                  placeholder="User ID"
                  required={
                    attributionClass === APIKeyAttributionClass.user_owned
                  }
                />
                {apiKey?.ownerUsername && (
                  <p className="text-xs text-muted-foreground">
                    Current owner: {apiKey.ownerUsername}
                  </p>
                )}
              </div>
            )}
          </div>

          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={isLoading || !name}>
              {isLoading
                ? 'Saving...'
                : isEditing
                  ? 'Save Changes'
                  : 'Create Key'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
