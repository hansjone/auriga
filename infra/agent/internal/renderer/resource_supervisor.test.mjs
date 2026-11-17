import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm } from 'node:fs/promises';
import { spawn } from 'node:child_process';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';

test('supervisor cleans command-line personal dependencies even without managed references before DSH starts', async () => {
  const root = await mkdtemp(join(tmpdir(), 'auriga-plugin-supervisor-'));
  const profile = join(root, 'profiles/web/package.json');
  const marker = join(root, 'started');
  const cli = join(root, 'cli.mjs');
  const policy = join(root, 'policy.json');
  await mkdir(dirname(profile), { recursive: true });
  await writeFile(
    profile,
    JSON.stringify({
      dependencies: {
        'personal-cli': '1.0.0',
        'auriga-dsh': 'link:/opt/auriga-dsh',
        '@deepseek-ai/dsh-base': '1.0.0',
      },
    }),
  );
  await writeFile(
    policy,
    JSON.stringify({
      schemaVersion: 1,
      newSessionRequired: false,
      allowPersonalPlugins: false,
      managedPluginNames: [],
      plugins: [],
    }),
  );
  await writeFile(
    cli,
    `import fs from 'node:fs';import assert from 'node:assert/strict';const args=process.argv.slice(2);assert.deepEqual(args,['plugin','--profile','web','remove','personal-cli','--config.ignore-scripts=true']);const file=${JSON.stringify(profile)};const p=JSON.parse(fs.readFileSync(file));delete p.dependencies['personal-cli'];fs.writeFileSync(file,JSON.stringify(p));`,
  );
  const preload = `import fs from 'node:fs';import cp from 'node:child_process';import{syncBuiltinESMExports}from'node:module';const read=fs.readFileSync;fs.readFileSync=function(p,...args){if(p==='/opt/dsh/node_modules/@deepseek-ai/dsh/package.json')return JSON.stringify({bin:{dsh:'bin/dsh.mjs'}});return read.call(this,p,...args)};const spawn=cp.spawn;cp.spawn=function(c,args,options){if(args[0].split(String.fromCharCode(92)).join('/').endsWith('/opt/dsh/node_modules/@deepseek-ai/dsh/bin/dsh.mjs'))args=[${JSON.stringify(cli)},...args.slice(1)];return spawn(c,args,options)};syncBuiltinESMExports();`;
  const supervisor = fileURLToPath(
    new URL('./resource_supervisor.mjs', import.meta.url),
  );
  let child;
  try {
    child = spawn(
      process.execPath,
      [
        '--import',
        'data:text/javascript,' + encodeURIComponent(preload),
        supervisor,
        policy,
        process.execPath,
        '-e',
        `require('node:fs').writeFileSync(${JSON.stringify(marker)},'started')`,
      ],
      {
        env: { ...process.env, DSH_HOME: root },
        windowsHide: true,
        stdio: 'pipe',
      },
    );
    let stderr = '';
    child.stderr.on('data', (data) => (stderr += data));
    const timeout = setTimeout(() => child.kill(), 20000);
    try {
      const code = await new Promise((accept, reject) => {
        child.once('error', reject);
        child.once('exit', accept);
      });
      assert.equal(code, 0, stderr);
    } finally {
      clearTimeout(timeout);
    }
    assert.equal(await readFile(marker, 'utf8'), 'started');
    const saved = JSON.parse(await readFile(profile, 'utf8'));
    assert.deepEqual(Object.keys(saved.dependencies).sort(), [
      '@deepseek-ai/dsh-base',
      'auriga-dsh',
    ]);
  } finally {
    if (child?.exitCode === null && child?.signalCode === null) child.kill();
    assert.equal(dirname(resolve(root)), resolve(tmpdir()));
    await rm(root, { recursive: true, force: true });
  }
});
