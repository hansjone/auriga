import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile, readFile, rm, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
import { options, collect } from './collect.mjs';

// Reuse the YAML parser already locked as a dependency of the root lint tool.
// These are static topology guards, not proof that Docker published a port.
const { load } = createRequire(import.meta.resolve('eslint'))('js-yaml');
const composePath = new URL(
  '../../deploy/compose/docker-compose.yml',
  import.meta.url,
);
const serviceNetworks = (service) =>
  service.network_mode
    ? []
    : Array.isArray(service.networks)
      ? service.networks
      : Object.keys(service.networks ?? { default: {} });
function assertObservabilityTopology(compose) {
  const { services, networks } = compose;
  const grafana = services.grafana;
  const publishingNetworks = serviceNetworks(grafana).filter(
    (name) =>
      networks[name] &&
      networks[name].internal !== true &&
      (networks[name].driver ?? 'bridge') === 'bridge' &&
      networks[name].external !== true,
  );
  assert.ok(
    publishingNetworks.length > 0,
    'Grafana requires a non-internal bridge for port publishing',
  );
  assert.deepEqual(
    grafana.ports,
    ['127.0.0.1:13000:3000'],
    'Grafana must publish only the documented loopback port',
  );
  assert.equal(
    String(grafana.environment.GF_AUTH_ANONYMOUS_ENABLED),
    'false',
    'anonymous access must remain disabled',
  );
  assert.equal(
    grafana.environment.GF_SECURITY_ADMIN_PASSWORD__FILE,
    '/run/secrets/grafana-admin-password',
  );
  assert.ok(
    grafana.volumes.some(
      (v) =>
        v.type === 'bind' &&
        v.target === '/run/secrets/grafana-admin-password' &&
        v.read_only === true &&
        v.bind?.create_host_path === false,
    ),
  );
  assert.ok(
    serviceNetworks(grafana).some(
      (name) =>
        networks[name]?.internal === true &&
        serviceNetworks(services.loki).includes(name),
    ),
    'Grafana must retain its internal Loki connection',
  );
  for (const name of ['loki', 'alloy']) {
    assert.deepEqual(
      services[name].ports ?? [],
      [],
      `${name} must not publish a host port`,
    );
    assert.equal(services[name].network_mode, undefined);
    assert.ok(
      serviceNetworks(services[name]).every(
        (network) => networks[network]?.internal === true,
      ),
      `${name} must remain on internal networks only`,
    );
  }
  for (const [name, service] of Object.entries(services)) {
    if (name !== 'grafana')
      assert.ok(
        !serviceNetworks(service).some((network) =>
          publishingNetworks.includes(network),
        ),
        'Grafana publishing bridge must remain dedicated',
      );
  }
  for (const name of ['grafana', 'loki', 'alloy'])
    assert.deepEqual(services[name].profiles, ['observability']);
}

test('observability gives Grafana a publishing gateway while keeping backends isolated', async () => {
  assertObservabilityTopology(load(await readFile(composePath, 'utf8')));
});

test('static observability guard rejects the original internal-only topology and unsafe alternatives', async () => {
  const compose = load(await readFile(composePath, 'utf8'));
  const internalOnly = globalThis.structuredClone(compose);
  internalOnly.services.grafana.networks = serviceNetworks(
    compose.services.grafana,
  ).filter((name) => compose.networks[name].internal === true);
  assert.throws(
    () => assertObservabilityTopology(internalOnly),
    /non-internal bridge/,
  );
  for (const mutate of [
    (config) => {
      config.services.grafana.ports = ['0.0.0.0:13000:3000'];
    },
    (config) => {
      config.services.loki.ports = ['127.0.0.1:3100:3100'];
    },
    (config) => {
      config.services.alloy.networks = [...config.services.grafana.networks];
    },
    (config) => {
      config.services.grafana.networks = [
        ...config.services.grafana.networks,
        'default',
      ];
    },
    (config) => {
      config.services.grafana.environment.GF_AUTH_ANONYMOUS_ENABLED = 'true';
    },
  ]) {
    const unsafe = globalThis.structuredClone(compose);
    mutate(unsafe);
    assert.throws(() => assertObservabilityTopology(unsafe));
  }
});

const trace = '4bf92f3577b34da6a3ce929d0e0e4736';

test('Grafana entrypoint rejects missing, empty and directory password files before starting', async (t) => {
  const shell =
    process.env.AURIGA_TEST_SH ??
    (process.platform === 'win32' ? undefined : '/bin/sh');
  if (!shell) return t.skip('set AURIGA_TEST_SH to a POSIX shell on Windows');
  const compose = load(await readFile(composePath, 'utf8'));
  const entrypoint = compose.services.grafana.entrypoint;
  assert.deepEqual(entrypoint.slice(0, 2), ['/bin/sh', '-ec']);
  // Compose $$ escapes to one $ in the actual container command.
  const script = entrypoint[2].replaceAll('$$', '$');
  const root = await mkdtemp(join(tmpdir(), 'auriga-grafana-guard-'));
  try {
    const empty = join(root, 'empty'),
      whitespace = join(root, 'whitespace'),
      valid = join(root, 'valid');
    await writeFile(empty, '');
    await writeFile(whitespace, '\n \t\n');
    await writeFile(valid, 'fixture-private-password');
    for (const file of [
      join(root, 'missing'),
      empty,
      whitespace,
      root,
      valid,
    ]) {
      const result = spawnSync(shell, ['-ec', script], {
        encoding: 'utf8',
        windowsHide: true,
        env: { ...process.env, GF_SECURITY_ADMIN_PASSWORD__FILE: file },
      });
      assert.ifError(result.error);
      // A valid file reaches the image's /run.sh (absent on this test host).
      assert.equal(result.status, file === valid ? 127 : 78, result.stderr);
      assert.ok(!result.stderr.includes('fixture-private-password'));
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
test('read-only collector fixture records exact Docker arguments, local trace selection, plugin time filter and masking', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-collect-'));
  try {
    const plugin = join(root, 'plugin');
    await mkdir(plugin);
    const record = {
      '@timestamp': '2026-10-29T12:00:00Z',
      trace_id: trace,
      service: 'dsh-plugin',
      level: 'ERROR',
      message: 'account_id=alice password=secret-password',
    };
    await writeFile(
      join(plugin, 'auriga-dsh-0.jsonl'),
      JSON.stringify(record) +
        '\n' +
        JSON.stringify({ ...record, '@timestamp': '2026-10-28T00:00:00Z' }) +
        '\n',
    );
    const calls = [];
    const config = options([
      '--out',
      join(root, 'result'),
      '--plugin-dir',
      plugin,
      '--since',
      '2026-10-29T11:00:00Z',
      '--until',
      '2026-10-29T13:00:00Z',
      '--trace-id',
      trace,
    ]);
    const manifest = await collect(config, async (args, accept) => {
      calls.push(args);
      if (args[0] === 'ps') await accept('abcdef123456 edge');
      else {
        await accept('2026-10-29T12:00:00Z ' + JSON.stringify(record));
        await accept(JSON.stringify({ ...record, trace_id: '0'.repeat(32) }));
      }
      return { exit_code: 0, reader_errors: [], truncated: false };
    });
    assert.equal(manifest.exit_code, 0);
    assert.deepEqual(calls[1], [
      'logs',
      '--timestamps',
      '--since',
      config.since,
      '--until',
      config.until,
      'abcdef123456',
    ]);
    assert.equal(manifest.sources[1].selected, 1);
    assert.equal(manifest.sources[2].selected, 1);
    for (const name of ['docker-edge-abcdef123456.log', 'auriga-dsh-0.jsonl']) {
      const text = await readFile(join(config.directory, name), 'utf8');
      assert.ok(!text.includes('secret-password'));
      assert.ok(text.includes('account_id=alice'));
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
test('missing Docker/plugin sources remain partial with individual exit code', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-collect-missing-'));
  try {
    const config = options([
      '--out',
      join(root, 'result'),
      '--plugin-dir',
      join(root, 'missing'),
    ]);
    const manifest = await collect(config, async () => ({
      exit_code: 127,
      reason: 'ENOENT',
      reader_errors: [],
      truncated: false,
    }));
    assert.equal(manifest.exit_code, 2);
    assert.equal(manifest.sources[0].exit_code, 127);
    assert.ok(manifest.sources.some((s) => s.missing));
    assert.equal(
      JSON.parse(
        await readFile(join(config.directory, 'manifest.json'), 'utf8'),
      ).exit_code,
      2,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
test('bad trace, reverse/invalid windows and injection-like projects are rejected', () => {
  assert.throws(() => options(['--trace-id', 'a'.repeat(10000)]));
  assert.throws(() => options(['--project', '--help']));
  assert.throws(() => options(['--since', 'yesterday']));
  assert.throws(() =>
    options([
      '--since',
      '2027-01-01T00:00:00Z',
      '--until',
      '2026-01-01T00:00:00Z',
    ]),
  );
});
