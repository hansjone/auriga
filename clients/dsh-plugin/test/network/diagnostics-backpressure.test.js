import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { pathToFileURL } from 'node:url';

const modulePath = process.env.AURIGA_CORDIS_MODULE;
const diagnosticUrl = new URL(
  '../../src/network/diagnostics.js',
  import.meta.url,
).href;
const lifecycleUrl = new URL('../../src/network/lifecycle.js', import.meta.url)
  .href;
const options = {
  skip: modulePath
    ? false
    : 'set AURIGA_CORDIS_MODULE to public Cordis 4.0.5 lib/index.js',
  timeout: 10000,
};

async function blockedPipe(withSink) {
  const code = `
    import {Context} from ${JSON.stringify(modulePath ? pathToFileURL(modulePath).href : '')};
    import {write} from 'node:fs';
    import {setImmediate} from 'node:timers/promises';
    import {installNetworkDiagnosticSink} from ${JSON.stringify(diagnosticUrl)};
    import {createNetworkLifecycle} from ${JSON.stringify(lifecycleUrl)};
    const ctx=new Context();
    if (${withSink}) installNetworkDiagnosticSink(ctx);
    let fillCompleted=false;
    write(2,Buffer.from('safe-fixture-filler\\n'.repeat(8192)),()=>{fillCompleted=true;});
    await setImmediate();
    const heartbeat=setInterval(()=>process.stdout.write('heartbeat\\n'),25);
    const lifecycle=createNetworkLifecycle({directory:'unused',ctx,getAccess:async()=>'',authorizeLocal:async()=>false,defaultProvider:{kind:'device-connection',async prepare(){process.stdout.write('prepare-denied\\n');throw {status:403};}}});
    lifecycle.login();
    setTimeout(async()=>{
      process.stdout.write('status='+lifecycle.status.state+'/'+lifecycle.status.reason+';fillCompleted='+fillCompleted+'\\n');
      clearInterval(heartbeat);
      await lifecycle.close();
      await ctx.fiber.dispose();
      process.stdout.write('disposed\\n');
    },50);
  `;
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, ['--input-type=module', '-e', code], {
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    });
    let stdout = '',
      beforeDrain = '',
      stderr = '';
    child.stdout.on('data', (chunk) => {
      stdout += chunk;
    });
    const release = setTimeout(() => {
      beforeDrain = stdout;
      child.stderr.on('data', (chunk) => {
        stderr += chunk;
      });
    }, 1200);
    const watchdog = setTimeout(() => {
      child.kill();
      reject(new Error('isolated pipe child exceeded watchdog'));
    }, 5000);
    child.once('error', reject);
    child.once('close', (code, signal) => {
      clearTimeout(release);
      clearTimeout(watchdog);
      resolve({ code, signal, beforeDrain, stdout, stderr });
    });
  });
}

test(
  '真实满stderr管道在读端恢复前仍完成403拒绝、心跳与卸载',
  options,
  async () => {
    for (const withSink of [false, true]) {
      const result = await blockedPipe(withSink);
      assert.equal(result.code, 0, result.stderr);
      assert.equal(result.signal, null);
      assert.match(result.beforeDrain, /heartbeat/);
      assert.match(
        result.beforeDrain,
        /status=unavailable\/forbidden;fillCompleted=false/,
      );
      assert.match(result.beforeDrain, /disposed/);
      assert.ok(
        result.stderr.length >= 163840,
        'actual pipe was filled by the async fixture',
      );
    }
  },
);

async function writerLifetime(mode) {
  const code = `
    import childProcess from 'node:child_process';
    import {syncBuiltinESMExports} from 'node:module';
    import {write,closeSync} from 'node:fs';
    import {setImmediate} from 'node:timers/promises';
    import {Context} from ${JSON.stringify(modulePath ? pathToFileURL(modulePath).href : '')};
    const originalSpawn=childProcess.spawn;
    childProcess.spawn=(executable,args,options)=>{
      // Extra test-only EOF witness. No production inspection API or PID log.
      const worker=originalSpawn(executable,args,{...options,stdio:[...options.stdio,4]});
      closeSync(4);
      process.stdout.write('owned-writer='+worker.pid+'\\n');
      worker.on('exit',()=>{
        process.stdout.write('writer-ended\\n',()=>{if(${JSON.stringify(mode)}==='lease')process.exit(0);});
      });
      return worker;
    };
    syncBuiltinESMExports();
    const {installNetworkDiagnosticSink,createDiagnosticLog}=await import(${JSON.stringify(diagnosticUrl)});
    const ctx=new Context();
    installNetworkDiagnosticSink(ctx);
    let completed=false;
    write(2,Buffer.from('safe-fixture-filler\\n'.repeat(8192)),()=>{completed=true;});
    await setImmediate();
    createDiagnosticLog(ctx,1)({event:'failure',phase:'ledger_restore',code:'ledger_missing',http_status:0,errno:'none',elapsed_ms:1,budget_ms:0,attempt:0,cancelled:false,created:false});
    setTimeout(()=>{
      process.stdout.write('parent-scheduled;fillCompleted='+completed+'\\n');
      if(${JSON.stringify(mode)}==='parent-killed')process.kill(process.pid,'SIGKILL');
    },100);
  `;
  return new Promise((resolve, reject) => {
    const start = Date.now();
    const child = spawn(process.execPath, ['--input-type=module', '-e', code], {
      stdio: ['ignore', 'pipe', 'pipe', 'ignore', 'pipe'],
      windowsHide: true,
    });
    // child_process normally drains unread stdio after its direct child exits.
    // Suppress that automatic drain too: the orphan check must keep the shared
    // stderr pipe blocked after the parent is forcibly terminated.
    const drainStderr = child.stderr.resume.bind(child.stderr);
    child.stderr.resume = () => child.stderr;
    let stdout = '',
      witnessMs = 0,
      beforeDrain = '',
      ownedWriter;
    child.stdout.on('data', (chunk) => {
      stdout += chunk;
      const match = /owned-writer=(\d+)/.exec(stdout);
      if (match) ownedWriter = Number(match[1]);
    });
    child.stdio[4].resume();
    child.stdio[4].once('end', () => {
      witnessMs = Date.now() - start;
    });
    const drain = setTimeout(() => {
      beforeDrain = stdout;
      drainStderr();
    }, 3000);
    const watchdog = setTimeout(() => {
      child.kill();
      if (ownedWriter) {
        try {
          process.kill(ownedWriter, 'SIGKILL');
        } catch {
          /* This owned test writer may already have exited. */
        }
      }
      reject(new Error('owned writer did not exit while pipe stayed full'));
    }, 5000);
    child.once('error', reject);
    child.once('close', () => {
      clearTimeout(drain);
      clearTimeout(watchdog);
      resolve({ stdout, beforeDrain, witnessMs });
    });
  });
}

test(
  '真实writer满管道硬租期与父强制退出后均自行退役，不依赖读端恢复',
  options,
  async (t) => {
    for (const mode of ['lease', 'parent-killed']) {
      const result = await writerLifetime(mode);
      assert.match(result.beforeDrain, /parent-scheduled;fillCompleted=false/);
      assert.ok(
        result.witnessMs > 0 && result.witnessMs < 2800,
        'writer EOF before stderr drain: ' + JSON.stringify(result),
      );
      t.diagnostic(
        `${mode}: writer EOF at ${result.witnessMs}ms; explicit stderr drain at 3000ms`,
      );
      if (mode === 'lease') {
        assert.match(result.beforeDrain, /writer-ended/);
        assert.ok(
          result.witnessMs >= 1500,
          'full pipe reached the hard writer lease',
        );
      }
    }
  },
);
