import type { JSONSchema } from '@/lib/schema-utils';
import React, {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react';

// Get schema URL from config (getConfig() is available at module load time)
declare function getConfig(): { basePath: string; version: string };
const SCHEMA_URL = `${getConfig().basePath}/assets/dag.schema.json?v=${getConfig().version}`;

interface SchemaContextValue {
  schema: JSONSchema | null;
  loading: boolean;
  error: Error | null;
  reload: () => void;
}

const SchemaContext = createContext<SchemaContextValue>({
  schema: null,
  loading: true,
  error: null,
  reload: () => {},
});

// Cache the dereferenced schema at module level
let cachedSchema: JSONSchema | null = null;
let cachePromise: Promise<JSONSchema> | null = null;

async function loadAndDereferenceSchema(): Promise<JSONSchema> {
  if (cachedSchema) {
    return cachedSchema;
  }

  if (cachePromise) {
    return cachePromise;
  }

  cachePromise = (async () => {
    // schema-utils loads on demand: it brings the YAML parser, which the
    // initial bundle does not need.
    const [response, { dereferenceSchema }] = await Promise.all([
      fetch(SCHEMA_URL),
      import('@/lib/schema-utils'),
    ]);
    if (!response.ok) {
      throw new Error(`Failed to fetch schema: ${response.statusText}`);
    }
    const rawSchema = await response.json();
    cachedSchema = dereferenceSchema(rawSchema as JSONSchema);
    return cachedSchema;
  })();

  return cachePromise;
}

export function SchemaProvider({ children }: { children: React.ReactNode }) {
  const [schema, setSchema] = useState<JSONSchema | null>(cachedSchema);
  const [loading, setLoading] = useState(!cachedSchema);
  const [error, setError] = useState<Error | null>(null);

  const loadSchema = async () => {
    setLoading(true);
    setError(null);
    try {
      const loadedSchema = await loadAndDereferenceSchema();
      setSchema(loadedSchema);
    } catch (err) {
      setError(err instanceof Error ? err : new Error('Failed to load schema'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!cachedSchema) {
      loadSchema();
    }
  }, []);

  const reload = () => {
    cachedSchema = null;
    cachePromise = null;
    loadSchema();
  };

  const value = useMemo(
    () => ({
      schema,
      loading,
      error,
      reload,
    }),
    [schema, loading, error]
  );

  return (
    <SchemaContext.Provider value={value}>{children}</SchemaContext.Provider>
  );
}

export function useSchema() {
  return useContext(SchemaContext);
}
