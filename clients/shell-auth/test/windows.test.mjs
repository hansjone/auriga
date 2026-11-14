import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync, readFileSync, writeFileSync } from 'node:fs';
import { randomUUID, randomBytes } from 'node:crypto';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { spawn, spawnSync, execFileSync } from 'node:child_process';
import { once } from 'node:events';
import { buildHelper } from '../windows/build.mjs';
import { Vault } from '../vault.mjs';
import { createInterface } from 'node:readline';
import { BusinessProxy } from '../business.mjs';
import { startIpc, connectIpc } from '../ipc.mjs';
import { fixture, command, login } from './fixtures.mjs';

test(
  'closing the real helper before handshake settles late calls without unhandled errors',
  { skip: process.platform !== 'win32', timeout: 15000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-shell-early-close-'));
    t.after(() =>
      rmSync(directory, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      }),
    );
    const helper = buildHelper(join(directory, 'helper.exe'));
    const client = connectIpc({
      helperPath: helper,
      pipe: 'auriga-shell-' + randomBytes(16).toString('hex'),
      capability: randomBytes(32).toString('base64url'),
    });
    client.close();
    client.close();
    await assert.rejects(
      client.call({ requestId: randomUUID() }),
      /disconnected/,
    );
    await assert.rejects(
      client.subscribe({ requestId: randomUUID() }, () =>
        assert.fail('closed subscriber'),
      ),
      /disconnected/,
    );
  },
);

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
    const duplicate = spawnSync(helper, ['server', pipe], {
      input: JSON.stringify({ capability }) + '\n',
      encoding: 'utf8',
      windowsHide: true,
      timeout: 5000,
    });
    assert.equal(
      duplicate.status,
      1,
      'first pipe instance must reject a second server',
    );
    assert.equal(duplicate.stderr.trim(), 'shell helper unavailable');
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

test(
  'pipe client uses Identification and rejects an untrusted same-user server before sending its capability',
  { skip: process.platform !== 'win32', timeout: 15000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-pipe-server-auth-'));
    let fake, client;
    t.after(async () => {
      for (const child of [client, fake])
        if (child?.exitCode === null) {
          const ended = once(child, 'exit');
          child.kill();
          await ended;
        }
      rmSync(directory, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      });
    });
    const helper = buildHelper(join(directory, 'helper.exe')),
      fakePath = join(directory, 'fake.exe'),
      source = join(directory, 'fake.cs');
    // Same real SID, but a different executable. A squatter must not receive the
    // capability. Querying the real pipe token also proves Identification QoS.
    writeFileSync(
      source,
      `using System;using System.IO;using System.IO.Pipes;using System.Security.Principal;class Fake{static void Main(string[] args){using(var pipe=new NamedPipeServerStream(args[0],PipeDirection.InOut,1,PipeTransmissionMode.Byte,PipeOptions.None)){Console.WriteLine("listening");pipe.WaitForConnection();string level="none";pipe.RunAsClient(()=>{level=WindowsIdentity.GetCurrent().ImpersonationLevel.ToString();});var line=new StreamReader(pipe).ReadLine();Console.WriteLine(level);Console.WriteLine(line==null?"empty":"received");}}}`,
    );
    execFileSync(
      join(
        process.env.SystemRoot ?? 'C:\\Windows',
        'Microsoft.NET/Framework64/v4.0.30319/csc.exe',
      ),
      ['/nologo', '/target:exe', '/out:' + fakePath, source],
      { windowsHide: true, stdio: 'pipe' },
    );
    const pipe = 'auriga-fake-' + randomUUID();
    fake = spawn(fakePath, [pipe], {
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    const lines = createInterface({ input: fake.stdout });
    t.after(() => lines.close());
    const [ready] = await once(lines, 'line');
    assert.equal(ready, 'listening');
    const output = [];
    lines.on('line', (line) => output.push(line));
    const fakeExited = once(fake, 'exit');
    client = spawn(helper, ['client', pipe], {
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'pipe'],
    });
    const clientExited = once(client, 'exit');
    client.stdout.resume();
    client.stderr.resume();
    client.stdin.end(
      JSON.stringify({ capability: randomBytes(32).toString('base64url') }) +
        '\n',
    );
    assert.equal((await clientExited)[0], 1);
    assert.equal((await fakeExited)[0], 0);
    assert.deepEqual(output, ['Identification', 'empty']);
  },
);

test(
  'real Windows K2 host context and current enterprise credential exchange',
  { skip: process.platform !== 'win32', timeout: 20000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-k2-ipc-')),
      helper = buildHelper(join(directory, 'helper.exe')),
      f = fixture();
    await login(f);
    const business = new BusinessProxy(f.broker);
    const ipc = await startIpc({ helper, broker: f.broker, business });
    t.after(async () => {
      await ipc.close();
      business.close();
      await f.broker.close();
      rmSync(directory, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      });
    });
    const script = `import{createReadStream}from'node:fs';import{createInterface}from'node:readline';import{once}from'node:events';import{randomUUID}from'node:crypto';import{connectIpc}from ${JSON.stringify(new URL('../ipc.mjs', import.meta.url).href)};const input=createReadStream('',{fd:3}),lines=createInterface({input});const[line]=await once(lines,'line');lines.close();input.destroy();const client=connectIpc(JSON.parse(line));const state=(await client.call({protocolVersion:1,requestId:randomUUID(),shellEpoch:'00000000-0000-0000-0000-000000000000',expectedRevision:0,action:'snapshot'})).snapshot;const envelope=()=>({protocolVersion:1,requestId:randomUUID(),shellEpoch:state.shellEpoch,expectedRevision:state.revision,expiresAt:new Date(Date.now()+25000).toISOString()});const result=await client.hostContext({...envelope(),action:'register',context:{kind:'session',hostSessionId:'native-host-session',expiresAt:new Date(Date.now()+600000).toISOString()}});if(!result.context)throw new Error('host register failed');const credentials=await client.enterprise({...envelope(),executionContextId:result.context.executionContextId,purpose:'raw'});if(!credentials.credentials)throw new Error('credential read failed');await client.hostContext({...envelope(),action:'revoke',executionContextId:result.context.executionContextId});const rejected=await client.enterprise({...envelope(),executionContextId:result.context.executionContextId,purpose:'raw'});console.log(JSON.stringify({version:credentials.credentials.credentialVersion,revoked:rejected.error?.reason==='context_revoked'}));client.close();`;
    const child = spawn(
      process.execPath,
      ['--input-type=module', '-e', script],
      { stdio: ['ignore', 'pipe', 'pipe', 'pipe'], windowsHide: true },
    );
    t.after(() => child.kill());
    let stdout = '',
      stderr = '';
    child.stdout.on('data', (b) => {
      stdout += b;
    });
    child.stderr.on('data', (b) => {
      stderr += b;
    });
    const exited = once(child, 'exit');
    await ipc.attach(child);
    const [code] = await exited;
    assert.equal(code, 0, stderr);
    assert.deepEqual(JSON.parse(stdout), { version: 1, revoked: true });
  },
);

test(
  'eight simultaneous authenticated pipe connections keep the listener and existing peers alive',
  { skip: process.platform !== 'win32', timeout: 25000 },
  async (t) => {
    const directory = mkdtempSync(join(tmpdir(), 'auriga-pipe-capacity-'));
    const helper = buildHelper(join(directory, 'helper.exe')),
      f = fixture();
    const business = new BusinessProxy(f.broker),
      ipc = await startIpc({ helper, broker: f.broker, business });
    let child;
    t.after(async () => {
      child?.kill();
      await ipc.close();
      business.close();
      await f.broker.close();
      rmSync(directory, {
        recursive: true,
        force: true,
        maxRetries: 5,
        retryDelay: 100,
      });
    });
    const script = `import{createReadStream}from'node:fs';import{createInterface}from'node:readline';import{once}from'node:events';import{randomUUID}from'node:crypto';import{connectIpc}from ${JSON.stringify(new URL('../ipc.mjs', import.meta.url).href)};const input=createReadStream('',{fd:3}),lines=createInterface({input});const[line]=await once(lines,'line');lines.close();input.destroy();const descriptor=JSON.parse(line),clients=[];const query=()=>({protocolVersion:1,requestId:randomUUID(),shellEpoch:'00000000-0000-0000-0000-000000000000',expectedRevision:0,action:'snapshot'});try{for(let i=0;i<8;i++){const client=connectIpc(descriptor);clients.push(client);if((await client.call(query())).snapshot.state!=='signed_out')throw Error('snapshot failed');}const excess=connectIpc(descriptor);let denied=false;try{await excess.call(query());}catch{denied=true;}finally{excess.close();}for(const client of clients)if((await client.call(query())).snapshot.state!=='signed_out')throw Error('peer lost');console.log(JSON.stringify({connected:clients.length,excessRejected:denied,live:true}));}finally{for(const client of clients)client.close();}`;
    child = spawn(process.execPath, ['--input-type=module', '-e', script], {
      windowsHide: true,
      stdio: ['ignore', 'pipe', 'pipe', 'pipe'],
    });
    let output = '',
      errors = '';
    child.stdout.on('data', (b) => {
      output += b;
    });
    child.stderr.on('data', (b) => {
      errors += b;
    });
    const exited = once(child, 'exit');
    await ipc.attach(child);
    assert.equal((await exited)[0], 0, errors);
    assert.deepEqual(JSON.parse(output), {
      connected: 8,
      excessRejected: true,
      live: true,
    });
  },
);
