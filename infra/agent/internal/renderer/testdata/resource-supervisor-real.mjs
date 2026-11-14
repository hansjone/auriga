import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdtemp, writeFile, rename, rm } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { setTimeout as delay } from 'node:timers/promises';

const root = await mkdtemp(join(tmpdir(), 'auriga-supervisor-'));
const file = join(root, 'policy.json');
const supervisor = resolve('infra/agent/internal/renderer/resource_supervisor.mjs');
const policy = expires => JSON.stringify({schemaVersion:1,newSessionRequired:false,trustedTime:new Date().toISOString(),leaseExpiresAt:new Date(expires).toISOString()});
try {
  for (const renew of [false, true]) {
    const started = Date.now();
    await writeFile(file, policy(started + 1500));
    const child = spawn(process.execPath, [supervisor, file, process.execPath, '-e', 'setInterval(()=>{},100)'], {windowsHide:true,stdio:'pipe'});
    const exit = once(child, 'exit');
    let stderr = ''; child.stderr.on('data', data => stderr += data);
    const timeout = setTimeout(() => child.kill(), 8000);
    try {
      if (renew) { await delay(600); await writeFile(file+'.next',policy(started+3000)); await rename(file+'.next',file); }
      const [code] = await exit;
      assert.equal(code,78,stderr);
      const elapsed = Date.now()-started;
      assert(elapsed >= (renew ? 2900 : 1400) && elapsed < 7000, `lease deadline ${elapsed}`);
    } finally { clearTimeout(timeout); if (child.exitCode === null && child.signalCode === null) { child.kill(); await exit; } }
  }
  await writeFile(file,policy(Date.now()+60000));
  const rollback = 'const real=Date.now;setTimeout(()=>{Date.now=()=>real()-121000},400)';
  const child=spawn(process.execPath,['--import','data:text/javascript,'+encodeURIComponent(rollback),supervisor,file,process.execPath,'-e','setInterval(()=>{},100)'],{windowsHide:true,stdio:'pipe'});
  const exit=once(child,'exit'), timeout=setTimeout(()=>child.kill(),8000);
  try { const [code]=await exit; assert.equal(code,78,'wall clock rollback did not stop child'); } finally {clearTimeout(timeout);if(child.exitCode===null&&child.signalCode===null){child.kill();await exit;}}
  console.log('Independent supervisor stopped real child at expiry and clock rollback; atomic policy renewal extended deadline.');
} finally { await rm(root, {recursive:true,force:true}); }
