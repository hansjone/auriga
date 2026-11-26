import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { PassThrough } from 'node:stream';
import { randomUUID } from 'node:crypto';
import { startIpc, connectIpc } from '../ipc.mjs';
import { fixture, command } from './fixtures.mjs';

test(
  'linux unix IPC registers controlled child and answers snapshot',
  { skip: process.platform !== 'linux', timeout: 15000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-unix-ipc-'));
    const f = fixture();
    const ipc = await startIpc({
      broker: f.broker,
      business: {
        call: async () => {
          throw new Error('unexpected business call');
        },
      },
      directory,
    });
    t.after(async () => {
      await ipc.close();
      await f.broker.close();
      rmSync(directory, { recursive: true, force: true });
    });
    const channel = new PassThrough();
    await ipc.attach({
      pid: process.pid,
      stdio: [null, null, null, channel],
    });
    const line = await new Promise((resolve, reject) => {
      let buf = '';
      const timer = setTimeout(
        () => reject(new Error('descriptor timeout')),
        3000,
      );
      channel.on('data', (chunk) => {
        buf += chunk;
        if (buf.includes('\n')) {
          clearTimeout(timer);
          resolve(buf.split('\n')[0]);
        }
      });
      channel.on('error', (error) => {
        clearTimeout(timer);
        reject(error);
      });
    });
    const descriptor = JSON.parse(line);
    assert.equal(descriptor.transport, 'unix');
    assert.equal(descriptor.protocolVersion, 1);
    assert.match(descriptor.pipe, /\.sock$/);
    const client = connectIpc(descriptor);
    t.after(() => client.close());
    const result = await client.call({
      ...command(f.broker, 'snapshot'),
      shellEpoch: '00000000-0000-0000-0000-000000000000',
      requestId: randomUUID(),
    });
    assert.equal(result.snapshot.state, 'signed_out');
  },
);
