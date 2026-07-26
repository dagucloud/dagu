import { components } from '../../../api/v1/schema';

export function getEventHandlers(s: components['schemas']['DAGRunDetails']) {
  const ret: components['schemas']['Node'][] = [];
  if (s.onInit) {
    ret.push(s.onInit);
  }
  if (s.onWait) {
    ret.push(s.onWait);
  }
  if (s.onSuccess) {
    ret.push(s.onSuccess);
  }
  if (s.onFailure) {
    ret.push(s.onFailure);
  }
  if (s.onAbort) {
    ret.push(s.onAbort);
  }
  if (s.onExit) {
    ret.push(s.onExit);
  }
  return ret;
}
