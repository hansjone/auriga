import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { pathToFileURL } from 'node:url';
import { performance } from 'node:perf_hooks';

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
const imports = `
  import fs from 'node:fs';
  import {Context} from ${JSON.stringify(modulePath ? pathToFileURL(modulePath).href : '')};
`;

// Do not assume one fixed byte count fills every OS pipe. Keep a single actual
// fs.write outstanding, advancing through partial/completed writes up to 16MiB.
// EAGAIN is explicit POSIX pressure; Windows may instead retain the write. The
// pending branch observes the same real request over two scheduled heartbeats.
const fillPipe = `
  async function fillPipe() {
    const block=Buffer.alloc(1024*1024,120);
    let bytes=0, request=0, pending=false, ended=false, sample=0;
    const state={kind:'preparing',bytes:0};
    return new Promise((resolve,reject)=>{
      function submit() {
        if(ended)return;
        if(bytes>=16*1024*1024)return fail(Error('fixture capacity exhausted without pressure'));
        pending=true;request++;
        fs.write(2,block,0,block.length,null,(error,count)=>{
          pending=false;
          if(ended)return;
          if(error){
            if(error.code==='EAGAIN'||error.code==='EWOULDBLOCK')return finish('eagain');
            return fail(Error('fixture fill failed'));
          }
          if(!count)return fail(Error('fixture made no progress'));
          bytes+=count;
          submit();
        });
      }
      function finish(kind){ended=true;clearInterval(observe);state.kind=kind;state.bytes=bytes;resolve(state);}
      function fail(error){ended=true;clearInterval(observe);reject(error);}
      const observe=setInterval(()=>{
        if(pending && sample===request)finish('pending');
        else sample=pending?request:0;
      },25);
      submit();
    });
  }
  const go=new Promise(resolve=>process.once('message',message=>{if(message==='go')resolve();}));
`;

function runFixture(code, { lifetime = false, mode = '' } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(process.execPath, ['--input-type=module', '-e', code], {
      stdio: ['ignore', 'pipe', 'pipe', 'ipc', ...(lifetime ? ['pipe'] : [])],
      windowsHide: true,
    });
    // Block explicit AND child_process's automatic post-exit stderr drain.
    const drainStderr = child.stderr.resume.bind(child.stderr);
    child.stderr.resume = () => child.stderr;
    let stdout = '',
      stderrBytes = 0,
      ready,
      result,
      scheduled,
      ownedWriter;
    let scheduledBeforeDrain = false,
      killRequested = false,
      killAcknowledged = false;
    let drained = false,
      writerStart = 0,
      witnessMs = 0,
      witnessBeforeDrain = false;
    const writes = [];
    let witness = '';
    let observation;
    const watchdog = setTimeout(
      () => fail('fixture preparation exceeded watchdog'),
      5000,
    );
    function fail(message) {
      clearTimeout(watchdog);
      clearTimeout(observation);
      child.kill();
      if (ownedWriter) {
        try {
          process.kill(ownedWriter, 'SIGKILL');
        } catch {
          /* Already retired. */
        }
      }
      drain();
      reject(new Error(message));
    }
    function drain() {
      if (drained) return;
      drained = true;
      child.stderr.on('data', (chunk) => {
        stderrBytes += chunk.length;
      });
      drainStderr();
    }
    function maybeKillParent() {
      if (
        mode === 'parent-killed' &&
        !killRequested &&
        scheduled &&
        writes.some((row) => row.event === 'write-start') &&
        child.connected
      ) {
        killRequested = true;
        child.send('kill-parent');
      }
    }
    child.stdout.on('data', (chunk) => {
      stdout += chunk;
    });
    child.on('message', (message) => {
      if (message.type === 'ready') {
        ready = message.fill;
        clearTimeout(watchdog);
        observation = setTimeout(
          () => {
            if (lifetime)
              fail('writer did not retire before output observation deadline');
            else {
              drain(); // Old synchronous sink must fail before this recovery.
              observation = setTimeout(
                () => fail('child did not exit after fixture recovery'),
                3800,
              );
            }
          },
          lifetime ? 3000 : 1200,
        );
        child.send('go');
      } else if (message.type === 'result') {
        if (!drained) result = message;
        drain();
      } else if (message.type === 'owned-writer') ownedWriter = message.pid;
      else if (message.type === 'parent-scheduled') {
        scheduled = message.fill;
        scheduledBeforeDrain = !drained;
        maybeKillParent();
        if (witnessMs) {
          clearTimeout(observation);
          drain();
        }
      } else if (message.type === 'killing-parent') {
        killAcknowledged = true;
      }
    });
    if (lifetime) {
      child.stdio[4].on('data', (chunk) => {
        witness += chunk;
        let newline;
        while ((newline = witness.indexOf('\n')) >= 0) {
          const row = JSON.parse(witness.slice(0, newline));
          witness = witness.slice(newline + 1);
          if (row.event === 'writer-ready') writerStart = performance.now();
          else writes.push(row);
        }
        maybeKillParent();
      });
      child.stdio[4].once('end', () => {
        witnessMs = performance.now() - writerStart;
        witnessBeforeDrain = !drained;
        if (scheduled) {
          clearTimeout(observation);
          drain();
        }
      });
    }
    child.once('error', (error) => fail(error.message));
    child.once('close', (exitCode, signal) => {
      clearTimeout(watchdog);
      clearTimeout(observation);
      resolve({
        exitCode,
        signal,
        stdout,
        stderrBytes,
        ready,
        result,
        scheduled,
        scheduledBeforeDrain,
        killAcknowledged,
        writes,
        witnessMs,
        witnessBeforeDrain,
        writerStart,
      });
    });
  });
}

test(
  '真实满stderr管道在准备屏障后、恢复读取前完成403拒绝/心跳/卸载',
  options,
  async () => {
    for (const withSink of [false, true]) {
      const result = await runFixture(`${imports}${fillPipe}
      import {installNetworkDiagnosticSink} from ${JSON.stringify(diagnosticUrl)};
      import {createNetworkLifecycle} from ${JSON.stringify(lifecycleUrl)};
      const ctx=new Context();if(${withSink})installNetworkDiagnosticSink(ctx);
      const fill=await fillPipe();process.send({type:'ready',fill});await go;
      let heartbeats=0,prepared=false;
      const heartbeat=setInterval(()=>heartbeats++,25);
      const lifecycle=createNetworkLifecycle({directory:'unused',ctx,getAccess:async()=>'',authorizeLocal:async()=>false,defaultProvider:{kind:'device-connection',async prepare(){prepared=true;throw {status:403};}}});
      lifecycle.login();
      setTimeout(async()=>{
        clearInterval(heartbeat);
        const state=lifecycle.status;
        await lifecycle.close();await ctx.fiber.dispose();
        process.send({type:'result',heartbeats,prepared,state:state.state,reason:state.reason,disposed:true},()=>process.disconnect());
      },50);
    `);
      assert.equal(result.exitCode, 0, result.stdout);
      assert.ok(
        ['pending', 'eagain'].includes(result.ready?.kind),
        'actual pressure precondition',
      );
      assert.ok(
        result.result,
        'business result must arrive before forced drain',
      );
      assert.ok(result.result.heartbeats > 0);
      assert.equal(result.result.prepared, true);
      assert.equal(result.result.state, 'unavailable');
      assert.equal(result.result.reason, 'forbidden');
      assert.equal(result.result.disposed, true);
      assert.ok(result.stderrBytes > 0, 'real pipe carries fixture bytes');
    }
  },
);

async function writerLifetime(mode) {
  const held = mode !== 'natural';
  // Observe the actual write result. For lease/orphan cases only, deliberately
  // retain its completion callback: POSIX can return EAGAIN instead of hanging.
  // This is explicit fault injection, not a claim of natural Linux blocked I/O.
  const prelude = `const fixtureFS=require('node:fs');const actualWrite=fixtureFS.write;const mark=row=>fixtureFS.writeSync(4,JSON.stringify(row)+'\\n');mark({event:'writer-ready'});fixtureFS.write=(fd,buffer,offset,length,position,callback)=>{mark({event:'write-start'});actualWrite(fd,buffer,offset,length,position,(error,count)=>{mark({event:'write-result',kind:error?(error.code==='EAGAIN'||error.code==='EWOULDBLOCK'?'eagain':'error'):count===length?'complete':'partial'});if(!${held})callback(error,count);});};`;
  return runFixture(
    `${imports}${fillPipe}
    import childProcess from 'node:child_process';
    import {syncBuiltinESMExports} from 'node:module';
    const originalSpawn=childProcess.spawn;
    let ended=false,scheduled=false,ctx;
    async function finish(){if(ended&&scheduled){await ctx.fiber.dispose();process.disconnect();}}
    childProcess.spawn=(executable,args,options)=>{
      const copy=[...args];copy[copy.indexOf('-e')+1]=${JSON.stringify(prelude)}+copy[copy.indexOf('-e')+1];
      const worker=originalSpawn(executable,copy,{...options,stdio:[...options.stdio,4]});
      fs.closeSync(4);
      process.send({type:'owned-writer',pid:worker.pid});
      worker.on('exit',()=>{ended=true;void finish();});
      return worker;
    };
    syncBuiltinESMExports();
    const {installNetworkDiagnosticSink,createDiagnosticLog}=await import(${JSON.stringify(diagnosticUrl)});
    ctx=new Context();installNetworkDiagnosticSink(ctx);
    const fill=await fillPipe();process.send({type:'ready',fill});await go;
    process.on('message',message=>{if(message==='kill-parent')process.send({type:'killing-parent'},()=>process.kill(process.pid,'SIGKILL'));});
    createDiagnosticLog(ctx,1)({event:'failure',phase:'ledger_restore',code:'ledger_missing',http_status:0,errno:'none',elapsed_ms:1,budget_ms:0,attempt:0,cancelled:false,created:false});
    setTimeout(()=>{scheduled=true;process.send({type:'parent-scheduled',fill});void finish();},100);
  `,
    { lifetime: true, mode },
  );
}

for (const mode of ['natural', 'lease-held-callback', 'parent-killed'])
  test('真实管道writer边界：' + mode, options, async (t) => {
    const result = await writerLifetime(mode);
    assert.ok(['pending', 'eagain'].includes(result.ready?.kind));
    assert.ok(
      result.scheduled,
      'parent event loop scheduled while reader paused',
    );
    assert.equal(result.scheduledBeforeDrain, true);
    if (mode === 'parent-killed') {
      assert.equal(result.killAcknowledged, true);
      assert.ok(result.exitCode !== 0 || result.signal === 'SIGKILL');
    }
    assert.ok(
      result.writerStart > 0 &&
        result.writes.some((row) => row.event === 'write-start'),
      'actual writer reached fs.write',
    );
    assert.equal(result.witnessBeforeDrain, true);
    assert.ok(
      result.witnessMs > 0 && result.witnessMs < 2800,
      'writer EOF before drain',
    );
    if (mode === 'lease-held-callback')
      assert.ok(
        result.witnessMs >= 1500,
        'held real write callback reached hard lease',
      );
    if (mode === 'natural') {
      const outcome = result.writes.findLast(
        (row) => row.event === 'write-result',
      );
      if (outcome)
        assert.equal(
          outcome.kind,
          'eagain',
          'natural pressure must not silently become a successful idle writer',
        );
      else
        assert.ok(
          result.witnessMs >= 1500,
          'naturally outstanding write reached hard lease',
        );
    }
    t.diagnostic(
      `${mode}: fill=${result.ready.kind}, write=${result.writes.findLast((row) => row.event === 'write-result')?.kind ?? 'outstanding'}, writer EOF=${Math.round(result.witnessMs)}ms before drain`,
    );
  });
