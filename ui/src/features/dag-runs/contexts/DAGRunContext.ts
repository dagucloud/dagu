import { createContext } from 'react';
import { Status } from '@/api/v1/schema';

interface DAGRunContextType {
  refresh: () => void;
  name: string;
  dagRunId: string;
  rootStatus?: Status;
}

export const DAGRunContext = createContext<DAGRunContextType>({
  refresh: () => {},
  name: '',
  dagRunId: '',
});
