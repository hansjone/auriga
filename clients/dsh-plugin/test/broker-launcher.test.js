import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import {
  mkdtemp,
  mkdir,
  copyFile,
  writeFile,
  readFile,
  rm,
} from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'auriga-private-launcher-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  const local = join(root, 'src/local'),
    artifact = join(root, 'shell-auth');
  await mkdir(local, { recursive: true });
  await mkdir(join(artifact, 'windows'), { recursive: true });
  await writeFile(join(root, 'package.json'), '{"type":"module"}');
  for (const name of ['broker-launcher.js', 'broker-package.js'])
    await copyFile(
      new URL('../src/local/' + name, import.meta.url),
      join(local, name),
    );
  // Only the C public startup seam is doubled; the real Node private IPC,
  // parent PID, packaged entry, byte verification and shutdown run unchanged.
  const modules = {
    'ipc.mjs': 'export {};',
    'schema.mjs': 'export {};',
    'windows/shell-helper.exe': 'fixture, never executed',
    'desktop.mjs': `import {randomBytes} from 'node:crypto';
import {writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
export async function startDesktopAuth({root,dataHome}) {
 await writeFile(join(root,'startup.json'),JSON.stringify({dataHome}));
 return {
  async attach(child) {
   await writeFile(join(root,'parent.json'),JSON.stringify({pid:child.pid}));
   child.stdio[3].end(JSON.stringify({protocolVersion:1,pipe:'auriga-shell-'+randomBytes(16).toString('hex'),capability:randomBytes(32).toString('base64url'),helperPath:fileURLToPath(new URL('./windows/shell-helper.exe',import.meta.url))})+'\\n');
  },
  async close() {await writeFile(join(root,'closed'),'closed');}
 };
}`,
  };
  const files = [];
  for (const [path, bytes] of Object.entries(modules)) {
    await writeFile(join(artifact, path), bytes);
    files.push({
      path,
      sha256: createHash('sha256').update(bytes).digest('hex'),
    });
  }
  await writeFile(
    join(artifact, 'broker-manifest.json'),
    JSON.stringify({ version: 1, files }),
  );
  const child = spawn(
    process.execPath,
    [join(local, 'broker-launcher.js'), root, root],
    {
      windowsHide: true,
      stdio: ['ignore', 'ignore', 'ignore', 'ipc'],
    },
  );
  t.after(() => {
    if (child.exitCode === null) child.kill();
  });
  return { root, child, exited: once(child, 'exit') };
}
test(
  'independent launcher passes the actual parent PID and descriptor through private IPC only',
  { skip: process.platform !== 'win32', timeout: 15000 },
  async (t) => {
    const f = await fixture(t),
      [message] = await once(f.child, 'message');
    assert.match(message.descriptor.capability, /^[A-Za-z0-9_-]{43}$/);
    assert.equal(
      JSON.parse(await readFile(join(f.root, 'parent.json'), 'utf8')).pid,
      process.pid,
    );
    assert.equal(
      f.child.spawnargs.some((arg) =>
        arg.includes(message.descriptor.capability),
      ),
      false,
    );
    assert.equal(
      JSON.parse(await readFile(join(f.root, 'startup.json'), 'utf8')).dataHome,
      f.root,
    );
    f.child.send({ action: 'close' });
    assert.equal((await f.exited)[0], 0);
    assert.equal(await readFile(join(f.root, 'closed'), 'utf8'), 'closed');
  },
);
test(
  'loss of the controlling DSH IPC connection closes the standalone broker',
  { skip: process.platform !== 'win32', timeout: 15000 },
  async (t) => {
    const f = await fixture(t);
    await once(f.child, 'message');
    f.child.disconnect();
    assert.equal((await f.exited)[0], 0);
    assert.equal(await readFile(join(f.root, 'closed'), 'utf8'), 'closed');
  },
);
