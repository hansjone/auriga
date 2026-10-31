import test from 'node:test';
import assert from 'node:assert/strict';
import { verifyInventory } from './health-inventory.mjs';
import { runPhase1 } from './run.mjs';
import http from 'node:http';

const config = {
  project: 'test-project',
  expectedServices: ['platform', 'agent', 'init'],
  oneShotServices: ['init'],
};
const snapshot = () => ({
  schemaVersion: 1,
  project: 'test-project',
  observedAt: new Date().toISOString(),
  services: [
    {
      name: 'platform-1',
      service: 'platform',
      status: 'running',
      health: 'healthy',
      restarts: 0,
      oom: false,
    },
    {
      name: 'agent-1',
      service: 'agent',
      status: 'running',
      health: null,
      probe: { kind: 'agent-online', status: 'PASS' },
      restarts: 0,
      oom: false,
    },
    {
      name: 'init-1',
      service: 'init',
      status: 'exited',
      exitCode: 0,
      restarts: 0,
      oom: false,
    },
  ],
});
test('complete health inventory checks every service, one-shot completion and explicit readiness', () => {
  const evidence = verifyInventory(config, snapshot());
  assert.equal(evidence.services.length, 3);
  assert.equal(evidence.complete, true);
});

for (const broken of [false, true]) {
  test(`unified health step requires a complete fresh inventory despite legacy declaration: ${broken}`, async (t) => {
    const server = http.createServer((req, res) => {
      res.setHeader('content-type', 'application/json');
      res.end(
        req.url === '/providers' ? '{"items":[]}' : '{"status":"healthy"}',
      );
    });
    await new Promise((done) => server.listen(0, '127.0.0.1', done));
    t.after(
      () =>
        new Promise((done) => {
          server.closeAllConnections();
          server.close(done);
        }),
    );
    const data = snapshot();
    if (broken) data.services.pop();
    const program = `let text='';process.stdin.on('data',x=>text+=x);process.stdin.on('end',()=>{if(JSON.parse(text).operation!=='health-inventory')process.exit(3);const data=${JSON.stringify(data)};data.observedAt=new Date().toISOString();process.stdout.write(JSON.stringify(data));});`;
    const baseUrl = 'http://127.0.0.1:' + server.address().port;
    const report = await runPhase1(
      { baseUrl, only: [1] },
      {
        healthChecks: [{ name: 'IAM', url: '/providers', kind: 'providers' }],
        relay: { baseUrl },
        healthInventoryComplete: true,
        healthInventory: {
          ...config,
          command: [process.execPath, '-e', program],
        },
      },
    );
    assert.equal(report.items[0].status, broken ? 'FAIL' : 'PASS');
    if (!broken) assert.equal(report.items[0].inventory.complete, true);
  });
}
test('health inventory refuses missing, unknown, unhealthy, stale and unprobed observations', () => {
  for (const change of [
    (x) => x.services.pop(),
    (x) =>
      x.services.push({
        ...x.services[0],
        name: 'foreign',
        service: 'foreign',
      }),
    (x) => (x.services[0].health = 'unhealthy'),
    (x) => (x.services[0].oom = true),
    (x) => (x.services[1].probe = undefined),
    (x) => (x.services[1].probe.status = 'FAIL'),
    (x) => (x.services[2].exitCode = 1),
    (x) => (x.observedAt = new Date(Date.now() - 180000).toISOString()),
    (x) => (x.project = 'other-project'),
  ]) {
    const data = snapshot();
    change(data);
    assert.throws(() => verifyInventory(config, data));
  }
});
