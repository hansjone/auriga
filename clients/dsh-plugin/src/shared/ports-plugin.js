// @ts-nocheck
import { createPublicKey } from 'node:crypto';
import { lstatSync, readFileSync } from 'node:fs';
import { SHARED_PROFILE } from './host.js';
import { createSharedNodeTransport } from './node-transport.js';
import { createProductionSharedPorts } from './production-ports.js';
import { canonical, fail } from './protocol.js';
import { isPlatformSkillRoots } from './skill-policy.js';
import { loadHostSdk } from './host-sdk.js';

export const name = 'auriga-dsh-shared-ports';
export const provide = 'aurigaSharedPorts';
export const inject = [
  'webServer',
  'sessions',
  'agents',
  'tools',
  'typertGateway',
];
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const audiences = [
  'auriga.shared.enterprise',
  'auriga.shared.model',
  'auriga.shared.message',
];
const required = [
  'resolve',
  'register',
  'lookup',
  'resourceSnapshot',
  'beginTask',
  'issueCapability',
  'finishTask',
  'authorizeCapability',
  'readCredentials',
  'enterpriseOutbound',
];

function keyMap(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value))
    fail('common.validation_failed');
  const keys = new Map();
  for (const [kid, pem] of Object.entries(value)) {
    if (!/^[A-Za-z0-9._-]{1,128}$/.test(kid) || typeof pem !== 'string')
      fail('common.validation_failed');
    const key = createPublicKey(pem);
    if (key.asymmetricKeyType !== 'ed25519') fail('common.validation_failed');
    keys.set(kid, key);
  }
  if (!keys.size) fail('common.unavailable');
  return keys;
}

export async function apply(ctx, config) {
  if (process.platform !== 'linux' || typeof config?.configFile !== 'string')
    fail('common.unavailable');
  const read = () => {
    const stat = lstatSync(config.configFile);
    if (!stat.isFile() || (stat.mode & 0o022) !== 0 || stat.size > 262144)
      fail('common.unavailable');
    const value = JSON.parse(readFileSync(config.configFile, 'utf8'));
    if (
      value.schemaVersion !== 1 ||
      !uuid.test(value.tenantId) ||
      !uuid.test(value.physicalInstanceId)
    )
      fail('common.validation_failed');
    const root = `/var/lib/auriga/pools/${value.physicalInstanceId}`;
    if (
      config.configFile !== root + '/control/shared-host.json' ||
      value.nodeSocket !== root + '/control/shared-node.sock' ||
      !isPlatformSkillRoots(value.skillRoots) ||
      typeof value.model !== 'string' ||
      !value.model ||
      !value.enterprise ||
      typeof value.enterprise !== 'object' ||
      Array.isArray(value.enterprise)
    )
      fail('common.validation_failed');
    return { ...value, profile: SHARED_PROFILE };
  };
  const initial = read();
  const settings = () => {
    const value = read();
    if (
      value.tenantId !== initial.tenantId ||
      value.physicalInstanceId !== initial.physicalInstanceId ||
      canonical(value.identityKeys) !== canonical(initial.identityKeys) ||
      canonical(value.capabilityKeys) !== canonical(initial.capabilityKeys)
    )
      fail('common.unavailable');
    return value;
  };
  if (!lstatSync(initial.nodeSocket).isSocket()) fail('common.unavailable');
  const options = {
    tenantId: initial.tenantId,
    physicalInstanceId: initial.physicalInstanceId,
    identityKeys: keyMap(initial.identityKeys),
    capabilityKeys: Object.fromEntries(
      audiences.map((audience) => [
        audience,
        keyMap(initial.capabilityKeys?.[audience]),
      ]),
    ),
  };
  const bridge = createSharedNodeTransport(initial.nodeSocket);
  let ports;
  try {
    const healthy = await bridge.call('health', {});
    if (
      healthy.protocol !== 'auriga.shared.node.v1' ||
      healthy.tenantId !== initial.tenantId ||
      healthy.physicalInstanceId !== initial.physicalInstanceId ||
      !Array.isArray(healthy.operations) ||
      required.some((operation) => !healthy.operations.includes(operation))
    )
      fail('common.unavailable');
    const sdk = await loadHostSdk();
    ports = createProductionSharedPorts(ctx, {
      settings,
      bridge,
      Peer: sdk.Peer,
      sdk,
      options,
      available: healthy.operations,
    });
    ctx.provide('aurigaSharedPorts', ports);
    ctx.effect(
      () => () => ports.close(),
      'auriga-dsh: node-backed shared ports',
    );
  } catch (error) {
    await ports?.close();
    bridge.close();
    throw error;
  }
}
