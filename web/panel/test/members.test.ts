import { describe, it, expect } from 'vitest';
import {
  readMembers,
  applyMember,
  memberActions,
  memberState,
} from '../src/members';
import type { PanelContext, ResourcePackage } from '../src/resources';
const id = (n: number) =>
  `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`;
const identity = { tenantId: id(1), ownerId: id(2), clientId: id(3) };
const row = {
  clientId: id(3),
  packageId: id(4),
  versionId: id(5),
  digest: 'sha256:' + 'a'.repeat(64),
  releaseSequence: 1,
  level: 'l3',
  desired: 'enabled',
  state: 'applied',
  revision: 2,
  reportedRevision: 2,
  reportSource: 'member',
  name: 'example',
  version: '1.0.0',
  localVersion: '1.0.0',
  localEnabled: true,
  restartRequired: false,
  syncPending: false,
} as const;
const signal = new AbortController().signal;
function context(item: unknown = row): PanelContext {
  return {
    mode: 'connect',
    protocol: 1,
    memberResources: {
      list: async () => ({
        identity,
        items: [item],
        reportSource: 'member',
        offline: false,
      }),
      apply: async () => item,
    },
  };
}
describe('desktop member resources', () => {
  it('keeps local version and exact member report independent of requested state', async () => {
    const data = await readMembers(context(), signal);
    expect(data.items[0].localVersion).toBe('1.0.0');
    expect(memberState({ ...data.items[0], reportedRevision: 1 })).toBe(
      'applying',
    );
    expect(memberState(data.items[0])).toBe('applied');
  });
  it('refuses another client row and does not invoke the port in managed mode', async () => {
    await expect(
      readMembers(context({ ...row, clientId: id(9) }), signal),
    ).rejects.toThrow('invalid');
    await expect(
      readMembers({ ...context(), mode: 'managed' }, signal),
    ).rejects.toThrow('unavailable');
    await expect(
      applyMember(
        context({ ...row, versionId: id(9) }),
        { packageId: id(4), versionId: id(5), action: 'install', key: 'key' },
        identity,
        signal,
      ),
    ).rejects.toThrow('invalid');
  });
  it.each(['l0', 'l1', 'l2', 'l3', 'l4'] as const)(
    'limits desktop %s controls',
    (level) => {
      const pack = {
        tenantId: id(1),
        ownerId: id(2),
        level,
        visibility: level === 'l4' ? 'private' : 'tenant',
        enabled: true,
      } as ResourcePackage;
      expect(memberActions(pack, identity)).toEqual(
        level === 'l0'
          ? []
          : level === 'l1'
            ? ['install', 'enable']
            : level === 'l2'
              ? ['install', 'enable', 'disable']
              : ['install', 'enable', 'disable', 'remove'],
      );
    },
  );
});
