import test from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import net from 'node:net';
import { spawn, spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import {
  networkSubmitReady,
  privatePath,
  processIdentity,
  stopProcesses,
  portFree,
  waitUntil,
} from '../../scripts/beta-ui/runner.mjs';

const repo = fileURLToPath(new URL('../../', import.meta.url));

test(
  'real network broker and runtime serve submit only over dedicated mTLS and stop releases every port',
  { skip: process.platform !== 'win32', timeout: 180000 },
  async () => {
    const state = fs.mkdtempSync(path.join(os.tmpdir(), 'auriga-submit-'));
    const children = [];
    const records = [];
    const ports = [];
    const run = (args, cwd) => {
      const result = spawnSync('go', args, {
        cwd,
        encoding: 'utf8',
        windowsHide: true,
        timeout: 90000,
      });
      assert.equal(result.status, 0, result.stdout + result.stderr);
    };
    try {
      privatePath(state, true);
      fs.mkdirSync(path.join(state, 'logs'));
      const sockets = [];
      try {
        for (let i = 0; i < 3; i++) {
          const socket = net.createServer();
          await new Promise((resolve) =>
            socket.listen(0, '127.0.0.1', resolve),
          );
          sockets.push(socket);
          ports.push(socket.address().port);
        }
      } finally {
        await Promise.all(
          sockets.map(
            (socket) => new Promise((resolve) => socket.close(resolve)),
          ),
        );
      }
      const manifest = {
        ports: {
          networkBroker: ports[0],
          networkWebsocket: ports[1],
          networkSubmit: ports[2],
        },
      };
      const token = path.join(state, 'service.jwt');
      fs.writeFileSync(token, 'unit-only-service-ticket');
      const root = path.join(state, 'network').replaceAll('\\', '/');
      const rpc = { Target: '127.0.0.1:1', ServiceTokenFile: token };
      const options = path.join(state, 'options.json');
      fs.writeFileSync(
        options,
        JSON.stringify({
          root,
          submitListen: `127.0.0.1:${ports[2]}`,
          brokerHost: '127.0.0.1',
          brokerURL: `tls://127.0.0.1:${ports[0]}`,
          brokerPort: ports[0],
          websocketPort: ports[1],
          audience: 'auriga-network',
          iam: rpc,
          collab: rpc,
        }),
      );
      run(
        [
          'run',
          './cmd/network-materialize',
          path.join(state, 'network'),
          options,
        ],
        path.join(repo, 'deploy/bootstrap'),
      );
      const gateway = path.join(state, 'gateway.exe');
      const broker = path.join(state, 'broker.exe');
      run(['build', '-o', gateway, '.'], path.join(repo, 'infra/ai-gateway'));
      run(
        ['build', '-o', broker, 'github.com/nats-io/nats-server/v2'],
        path.join(repo, 'infra/ai-gateway'),
      );
      const launch = (name, exe, args) => {
        const fd = fs.openSync(path.join(state, `logs/${name}.log`), 'a');
        let child;
        try {
          child = spawn(exe, args, {
            cwd: state,
            stdio: ['ignore', fd, fd],
            windowsHide: true,
          });
        } finally {
          fs.closeSync(fd);
        }
        children.push(child);
        child.on('error', () => {});
        records.push({
          name,
          pid: child.pid,
          identity: processIdentity(child.pid),
        });
        fs.writeFileSync(
          path.join(state, 'processes.json'),
          JSON.stringify(records),
        );
      };
      launch('network-broker', broker, [
        '-c',
        path.join(state, 'network/broker.conf'),
      ]);
      await waitUntil(
        async () => !(await portFree(ports[0])),
        'broker startup failed',
        10000,
      );
      launch('network', gateway, [
        '--network-config',
        path.join(state, 'network/runtime.json'),
      ]);
      await waitUntil(
        async () => {
          for (const record of records) {
            assert.equal(
              processIdentity(record.pid),
              record.identity,
              `owned ${record.name} exited; private logs: ${state}`,
            );
          }
          return networkSubmitReady(state, manifest);
        },
        `real network submit readiness failed; private logs: ${state}`,
        15000,
      );
      assert.equal(await networkSubmitReady(state, manifest), true);
      fs.renameSync(
        path.join(state, 'network/edge.key'),
        path.join(state, 'network/edge.key.saved'),
      );
      assert.equal(await networkSubmitReady(state, manifest), false);
      fs.renameSync(
        path.join(state, 'network/edge.key.saved'),
        path.join(state, 'network/edge.key'),
      );
      await stopProcesses(state, ports);
      await stopProcesses(state, ports);
      assert.ok((await Promise.all(ports.map(portFree))).every(Boolean));
    } finally {
      await Promise.all(
        children.map(async (child) => {
          if (child.exitCode !== null || !child.pid) return;
          const done = new Promise((resolve) => child.once('exit', resolve));
          child.kill();
          await done;
        }),
      );
      fs.rmSync(state, { recursive: true, force: true });
    }
  },
);
