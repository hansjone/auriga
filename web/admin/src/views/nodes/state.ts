import type { Node } from '../../admin/api';

export function nodeProgress(node?: Node) {
  return node?.status ?? 'waiting';
}

export function retirementBlock(node: Node) {
  if (node.status === 'revoked') return 'retired';
  if (node.status !== 'draining' || node.schedulable) return 'drainFirst';
  if (node.usage?.instancesTotal === undefined) return 'emptyUnknown';
  if (
    node.usage.instancesTotal !== 0 ||
    (node.usage.instancesRunning ?? 0) !== 0
  )
    return 'notEmpty';
  return undefined;
}
