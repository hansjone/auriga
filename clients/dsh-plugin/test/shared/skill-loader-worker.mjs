import assert from 'node:assert/strict';
import { generateKeyPairSync, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { SourceTextModule, SyntheticModule, createContext } from 'node:vm';
import { SHARED_PROFILE } from '../../src/shared/host.js';
import { canonical, fail } from '../../src/shared/protocol.js';
import {
  isPlatformSkillRoots,
  PLATFORM_SKILL_ROOT,
} from '../../src/shared/skill-policy.js';

// Consume the actual production writer and its verified read-only mount. Do
// not replace the offending producer path with an invented deployment root.
const writer = readFileSync(
  new URL(
    '../../../../infra/agent/internal/app/shared_runtime.go',
    import.meta.url,
  ),
  'utf8',
);
const mounted = readFileSync(
  new URL(
    '../../../../infra/agent/internal/runtime/pool_container.go',
    import.meta.url,
  ),
  'utf8',
);
const renderedRoot = writer.match(
  /config\["nodeSocket"\], config\["skillRoots"\] = target\+"\/shared-node.sock", \[\]string\{"([^"]+)"\}/,
)?.[1];
assert.ok(renderedRoot);
assert.equal(renderedRoot, PLATFORM_SKILL_ROOT);
assert.ok(
  mounted.includes(
    `"Target": "${renderedRoot}", "ReadOnly": true, "RW": false`,
  ),
);
const pool = randomUUID(),
  root = `/var/lib/auriga/pools/${pool}`;
const { publicKey } = generateKeyPairSync('ed25519');
const keys = { fixture: publicKey.export({ type: 'spki', format: 'pem' }) };
const source = readFileSync(
  new URL('../../src/shared/ports-plugin.js', import.meta.url),
  'utf8',
);
async function load(skillRoots) {
  let health = 0;
  const config = {
    schemaVersion: 1,
    tenantId: randomUUID(),
    physicalInstanceId: pool,
    nodeSocket: root + '/control/shared-node.sock',
    skillRoots,
    model: 'fixture',
    enterprise: {},
    identityKeys: keys,
    capabilityKeys: Object.fromEntries(
      [
        'auriga.shared.enterprise',
        'auriga.shared.model',
        'auriga.shared.message',
      ].map((name) => [name, keys]),
    ),
  };
  const context = createContext({ process: { platform: 'linux' } });
  const dependencies = {
    'node:crypto': await import('node:crypto'),
    'node:fs': {
      lstatSync: () => ({
        isFile: () => true,
        isSocket: () => true,
        mode: 0o444,
        size: 100,
      }),
      readFileSync: () => JSON.stringify(config),
    },
    './host.js': { SHARED_PROFILE },
    './skill-policy.js': { isPlatformSkillRoots },
    './node-transport.js': {
      createSharedNodeTransport: () => ({
        async call() {
          health++;
          throw Object.assign(new Error('health reached'), {
            code: 'fixture.health',
          });
        },
        close() {},
      }),
    },
    './production-ports.js': {
      createProductionSharedPorts() {
        assert.fail('unavailable fixture health cannot mount ports');
      },
    },
    './protocol.js': { canonical, fail },
  };
  const module = new SourceTextModule(source, { context });
  await module.link((name) => {
    const values = dependencies[name];
    assert.ok(values, 'unexpected production dependency ' + name);
    return new SyntheticModule(
      Object.keys(values),
      function () {
        for (const [key, value] of Object.entries(values))
          this.setExport(key, value);
      },
      { context },
    );
  });
  await module.evaluate();
  let code;
  try {
    await module.namespace.apply(
      {},
      { configFile: root + '/control/shared-host.json' },
    );
  } catch (error) {
    code = error.code;
  }
  return { code, health };
}
assert.deepEqual(await load([renderedRoot]), {
  code: 'fixture.health',
  health: 1,
});
for (const roots of [
  [],
  [root + '/host/platform-skills/active'],
  ['/tmp/private'],
  [renderedRoot + '/extra'],
  [renderedRoot + '/'],
  [renderedRoot, renderedRoot],
  ['/run/auriga/skills-active/../current'],
  null,
])
  assert.deepEqual(await load(roots), {
    code: 'common.validation_failed',
    health: 0,
  });
