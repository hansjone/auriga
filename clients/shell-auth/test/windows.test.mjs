import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { buildHelper } from '../windows/build.mjs';
import { Vault } from '../vault.mjs';
import { createInterface } from 'node:readline';
import { startIpc, connectIpc } from '../ipc.mjs';
import { fixture, command } from './fixtures.mjs';

test(
  'real Windows DPAPI roundtrip and protected current-user ACL; ciphertext contains no token',
  { skip: process.platform !== 'win32', timeout: 15000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-shell-win-'));
    t.after(() => rmSync(directory, { recursive: true, force: true }));
    const helper = buildHelper(join(directory, 'helper.exe')),
      data = join(directory, 'private');
    const vault = new Vault(data, helper),
      token = randomUUID();
    await vault.save({ token });
    assert.deepEqual(await vault.load(), { token });
    assert.ok(!readFileSync(vault.file).includes(Buffer.from(token)));
    const acl = JSON.parse(
      execFileSync(helper, ['secure', data], { encoding: 'utf8' }),
    );
    assert.equal(acl.protectedAcl, true);
    assert.match(acl.sid, /^S-1-/);
  },
);
test(
  'real named-pipe helper accepts registered child through inherited capability',
  { skip: process.platform !== 'win32', timeout: 15000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-shell-ipc-'));
    const helper = buildHelper(join(directory, 'helper.exe')),
      f = fixture();
    const ipc = await startIpc({
      helper,
      broker: f.broker,
      business: {
        call: async () => {
          throw new Error();
        },
      },
    });
    t.after(async () => {
      await ipc.close();
      await f.broker.close();
      rmSync(directory, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      });
    });
    // A real controlled child receives the descriptor only through fd3; stdout
    // below deliberately returns no descriptor or capability.
    const script = `import {createReadStream} from 'node:fs';import {createInterface} from 'node:readline';import {once} from 'node:events';import {connectIpc} from ${JSON.stringify(new URL('../ipc.mjs', import.meta.url).href)};const input=createReadStream('',{fd:3});const lines=createInterface({input});const [line]=await once(lines,'line');const d=JSON.parse(line);lines.close();input.destroy();const c=connectIpc(d);const r=await c.call(${JSON.stringify({ ...command(f.broker, 'snapshot'), shellEpoch: '00000000-0000-0000-0000-000000000000' })});console.log(JSON.stringify(r));c.close();`;
    const child = spawn(
      process.execPath,
      ['--input-type=module', '-e', script],
      { stdio: ['ignore', 'pipe', 'pipe', 'pipe'], windowsHide: true },
    );
    t.after(() => child.kill());
    let output = '';
    child.stdout.on('data', (b) => {
      output += b;
    });
    const exited = once(child, 'exit');
    await ipc.attach(child);
    const [code] = await exited;
    assert.equal(code, 0);
    assert.equal(JSON.parse(output).snapshot.state, 'signed_out');
  },
);

test(
  'real pipe rejects unregistered process and wrong capability',
  { skip: process.platform !== 'win32', timeout: 15000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-shell-deny-'));
    const helper = buildHelper(join(directory, 'helper.exe')),
      pipe = 'auriga-test-' + randomUUID(),
      capability = randomUUID().replaceAll('-', '') + '12345678901';
    const server = spawn(helper, ['server', pipe], {
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    server.stderr.resume();
    const lines = createInterface({ input: server.stdout });
    t.after(async () => {
      const ended = once(server, 'exit');
      server.kill();
      await ended;
      lines.close();
      rmSync(directory, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      });
    });
    const listening = once(lines, 'line');
    server.stdin.write(JSON.stringify({ capability }) + '\n');
    await listening;
    let client = connectIpc({ helperPath: helper, pipe, capability });
    await assert.rejects(
      client.call(command(fixture().broker, 'snapshot')),
      /disconnected/,
    );
    client.close();
    const registered = once(lines, 'line');
    server.stdin.write(JSON.stringify({ register: process.pid }) + '\n');
    await registered;
    client = connectIpc({
      helperPath: helper,
      pipe,
      capability: 'x'.repeat(43),
    });
    await assert.rejects(
      client.call(command(fixture().broker, 'snapshot')),
      /disconnected/,
    );
    client.close();
  },
);
