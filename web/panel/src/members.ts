import type { components } from '../../shared/src/generated/resource';
import {
  ResourceError,
  type Failure,
  type PanelContext,
  type ResourcePackage,
} from './resources';
export type MemberInstallation = components['schemas']['MemberInstallation'];
export type MemberInput = {
  packageId: string;
  versionId: string;
  action: 'install' | 'enable' | 'disable' | 'remove';
  key: string;
};
export type MemberIdentity = {
  tenantId: string;
  ownerId: string;
  clientId: string;
};
export type MemberItem = MemberInstallation & {
  name: string;
  version: string;
  localVersion: string | null;
  localEnabled: boolean;
  restartRequired: boolean;
  syncPending: boolean;
  renewalState?: 'current' | 'offline' | 'denied';
};
const id = (value: unknown): value is string =>
  typeof value === 'string' &&
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
const record = (v: unknown): v is Record<string, unknown> =>
  !!v && typeof v === 'object' && !Array.isArray(v);
function item(value: unknown): MemberItem {
  if (
    !record(value) ||
    !id(value.packageId) ||
    !id(value.versionId) ||
    !id(value.clientId) ||
    typeof value.digest !== 'string' ||
    !/^sha256:[0-9a-f]{64}$/.test(value.digest) ||
    !Number.isSafeInteger(value.revision) ||
    Number(value.revision) < 1 ||
    !Number.isSafeInteger(value.releaseSequence) ||
    Number(value.releaseSequence) < 1 ||
    !['l1', 'l2', 'l3', 'l4'].includes(String(value.level)) ||
    !['enabled', 'disabled', 'removed'].includes(String(value.desired)) ||
    !['applying', 'applied', 'failed'].includes(String(value.state)) ||
    value.reportSource !== 'member' ||
    typeof value.name !== 'string' ||
    typeof value.version !== 'string' ||
    !(value.localVersion === null || typeof value.localVersion === 'string') ||
    typeof value.localEnabled !== 'boolean' ||
    typeof value.restartRequired !== 'boolean' ||
    typeof value.syncPending !== 'boolean' ||
    (value.renewalState !== undefined &&
      !['current', 'offline', 'denied'].includes(String(value.renewalState))) ||
    (value.reportedRevision !== undefined &&
      (!Number.isSafeInteger(value.reportedRevision) ||
        Number(value.reportedRevision) < 1 ||
        Number(value.reportedRevision) > Number(value.revision)))
  )
    throw new ResourceError('invalid');
  return value as MemberItem;
}
export function memberFailure(error: unknown): Failure {
  if (error instanceof ResourceError) return error.reason;
  const reason = record(error) ? error.reason : undefined;
  return typeof reason === 'string' &&
    ['auth', 'forbidden', 'verification', 'setup', 'conflict'].includes(reason)
    ? (reason as Failure)
    : 'unavailable';
}
export async function readMembers(context: PanelContext, signal: AbortSignal) {
  if (
    context.mode !== 'connect' ||
    context.protocol !== 1 ||
    !context.memberResources
  )
    throw new ResourceError('unavailable');
  const response = await context.memberResources.list(signal);
  if (
    !record(response) ||
    !record(response.identity) ||
    !id(response.identity.tenantId) ||
    !id(response.identity.ownerId) ||
    !id(response.identity.clientId) ||
    !Array.isArray(response.items) ||
    response.items.length > 10000 ||
    response.reportSource !== 'member' ||
    typeof response.offline !== 'boolean'
  )
    throw new ResourceError('invalid');
  const identity = response.identity as MemberIdentity,
    seen = new Set();
  const items = response.items.map(item);
  for (const row of items) {
    if (row.clientId !== identity.clientId || seen.has(row.packageId))
      throw new ResourceError('invalid');
    seen.add(row.packageId);
  }
  return {
    identity,
    items,
    offline:
      response.offline || items.some((row) => row.renewalState === 'offline'),
  };
}
export async function applyMember(
  context: PanelContext,
  input: MemberInput,
  identity: MemberIdentity,
  signal: AbortSignal,
) {
  if (context.mode !== 'connect' || !context.memberResources)
    throw new ResourceError('unavailable');
  const row = item(await context.memberResources.apply(input, signal));
  if (
    row.clientId !== identity.clientId ||
    row.packageId !== input.packageId ||
    row.versionId !== input.versionId
  )
    throw new ResourceError('invalid');
  return row;
}
export function memberActions(
  pack: ResourcePackage,
  identity: MemberIdentity,
): MemberInput['action'][] {
  if (
    pack.tenantId !== identity.tenantId ||
    pack.level === 'l0' ||
    (pack.level === 'l4' &&
      (pack.ownerId !== identity.ownerId || pack.visibility !== 'private'))
  )
    return [];
  const actions: MemberInput['action'][] =
    pack.level === 'l1'
      ? ['install', 'enable']
      : pack.level === 'l2'
        ? ['install', 'enable', 'disable']
        : ['install', 'enable', 'disable', 'remove'];
  return pack.enabled
    ? actions
    : actions.filter((a) => a === 'disable' || a === 'remove');
}
export function memberState(
  row: MemberItem,
): 'applying' | 'applied' | 'failed' {
  return row.state === 'applied' &&
    (row.reportedRevision !== row.revision ||
      row.syncPending ||
      row.restartRequired)
    ? 'applying'
    : row.state;
}
