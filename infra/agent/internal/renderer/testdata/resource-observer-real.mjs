import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { mkdir, mkdtemp, writeFile, readFile, rename } from 'node:fs/promises';
import { join, resolve, dirname } from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { setTimeout as delay } from 'node:timers/promises';

const [runtime, evidence] = process.argv.slice(2).map(value=>resolve(value));
await mkdir(evidence, {recursive:true});
const root=await mkdtemp(join(evidence,'observer-'));
const home=join(root,'home'),workspace=join(home,'workspace');
await mkdir(workspace,{recursive:true});
for(const [directory,name] of [['skills/private-probe','private-probe'],['auriga/skills-active/current/shared-probe','shared-probe']]){
 await mkdir(join(home,directory),{recursive:true});await writeFile(join(home,directory,'SKILL.md'),`---\nname: ${name}\ndescription: Public isolated node fixture.\n---\n\n${name} body.\n`);
}
const cli=join(runtime,'node_modules/@deepseek-ai/dsh/lib/bin.js');
const require=createRequire(cli);const bundled=join(dirname(require.resolve('@deepseek-ai/dsh-agent-preset/package.json')),'skills');
const observer=resolve('infra/agent/internal/renderer/resource_observer.mjs');
const token=join(root,'instance.token'),policy=join(root,'policy.json'),probe=join(root,'probe.mjs'),result=join(root,'result.json');
await writeFile(token,'public-isolated-node-test');
await writeFile(probe,`import {writeFile,readFile} from 'node:fs/promises';export const name='node-resource-test-probe';export const inject=['webServer','sessionController'];export function apply(ctx){setImmediate(()=>void(async()=>{const old=process.env.OLD_TEST_SESSION;if(old){let denied=false;try{await ctx.sessionController.prompt({sessionId:old,prompt:'must not execute'})}catch(e){denied=String(e).includes('Private-only new session required')}if(!denied)throw new Error('old context was not refused')}const session=await ctx.sessionController.create({cwd:process.cwd(),agentPreset:'cordis'});await writeFile(process.env.NODE_TEST_RESULT,JSON.stringify({port:ctx.webServer.port,sessionId:session.sessionId,oldDenied:!!old}));})().catch(async e=>{await writeFile(process.env.NODE_TEST_RESULT,JSON.stringify({error:String(e),stack:e.stack}))}));}`);
let old;
for(const privateOnly of [false,true]){
 if(privateOnly)await rename(join(home,'auriga/skills-active/current'),join(home,'quarantine'));
 await mkdir(join(home,'auriga/skills-active/current'),{recursive:true});
 await writeFile(policy,JSON.stringify({schemaVersion:1,newSessionRequired:privateOnly}));
 await writeFile(result,'');
 const patch=join(root,privateOnly?'private.patch.yml':'active.patch.yml');
 await writeFile(patch,`- insert:\n    - id: auriga-shared-skills\n      name: '@deepseek-ai/dsh-skill-filesystem'\n      config:\n        providerName: auriga-shared\n        includeDefaultRoots: false\n        customSkillDirs: [${JSON.stringify(join(home,'auriga/skills-active/current'))}]\n        watch: true\n    - id: node-resource-observer\n      name: ${JSON.stringify(pathToFileURL(observer).href)}\n      config: ${JSON.stringify({tokenFile:token,policyFile:policy})}\n    - id: node-resource-test-probe\n      name: ${JSON.stringify(pathToFileURL(probe).href)}\n`);
 const env=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('AURIGA_')&&!key.startsWith('DSH_')));
 let log='';const child=spawn(process.execPath,[cli,'web','--patch',patch,'--no-open','--host','127.0.0.1','--port','0'],{cwd:workspace,env:{...env,DSH_HOME:home,DSH_AGENTS_HOME:join(root,'agents'),DSH_BUNDLED_SKILL_DIR:bundled,NODE_TEST_RESULT:result,...old?{OLD_TEST_SESSION:old}:{}},windowsHide:true,stdio:['ignore','pipe','pipe']});
 child.stdout.on('data',b=>log+=b);child.stderr.on('data',b=>log+=b);let exited=false;child.on('exit',()=>exited=true);
 try{
  let ready;const end=Date.now()+180000;while(Date.now()<end&&!exited){try{ready=JSON.parse(await readFile(result,'utf8'));break}catch{}await delay(100)}
  assert(ready&&!ready.error,JSON.stringify(ready)+'\n'+log);assert(ready.port>0);old=ready.sessionId;
  const response=await fetch(`http://127.0.0.1:${ready.port}/auriga-node-resource-catalog`,{headers:{Authorization:'Bearer public-isolated-node-test'}});assert.equal(response.status,200,log);const catalog=await response.json();assert.equal(catalog.privateOnly,privateOnly);
  for(const preset of ['cordis','standard','ptc']){const rows=catalog.skills.filter(s=>s.preset===preset);assert(rows.some(s=>s.name==='private-probe'),'private skill missing '+preset);assert(rows.some(s=>s.name==='editing-cordis-compositions'),'upstream skill missing '+preset);const shared=rows.find(s=>s.name==='shared-probe');if(privateOnly){assert.equal(shared,undefined);assert(ready.oldDenied)}else{assert.equal(shared.provider,'auriga-shared');assert.equal(shared.content,'shared-probe body.')}}
  const denied=await fetch(`http://127.0.0.1:${ready.port}/auriga-node-resource-catalog`);assert.equal(denied.status,403);
  await writeFile(join(root,privateOnly?'private.json':'active.json'),JSON.stringify({ready,catalog},null,2));
 }finally{if(!exited){const stopped=once(child,'exit');child.kill();await stopped}await writeFile(join(root,privateOnly?'private.log':'active.log'),log.replace(/token=[^&\s]+/g,'token=REDACTED'))}
}
console.log('Real DSH: all presets preserve private/upstream skills; shared sources observed; old private-only context refused; all children stopped. '+root);
