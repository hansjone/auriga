// 结构门禁，不执行插件代码；局限和保留通道规则见 shell-auth-migration.md。
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
const base = fileURLToPath(
  new URL('../../clients/dsh-plugin/src/', import.meta.url),
);

function withoutComments(source) {
  return source.replace(
    /(['"`])(?:\\[\s\S]|(?!\1)[^\\])*?\1|\/\*[\s\S]*?\*\/|(?<!\\)\/\/[^\r\n]*/g,
    (value) => (value.startsWith('//') || value.startsWith('/*') ? ' ' : value),
  );
}
function routeFeatures(source) {
  return [
    ...new Set(
      [
        ...withoutComments(source).matchAll(
          /\/(?:api\/[A-Za-z][A-Za-z0-9_-]*\/v[0-9]+|ai\/v1|ai\/mcp|ai\/team\/node|oauth2\/)(?:[A-Za-z0-9_./?=&:%-]|\$\{[^}]+\})*/g,
        ),
      ].map((m) =>
        m[0].replace(
          /\$\{\s*([^}]+?)\s*\}/g,
          (_, expression) => '${' + expression.replace(/\s+/g, '') + '}',
        ),
      ),
    ),
  ];
}
function transportFeatures(source) {
  const code = withoutComments(source),
    features = new Set();
  for (const match of code.matchAll(
    /\b(fetch|controlledFetch|tracedFetch|traceFetch|fetchImpl|fetcher|undiciFetch|customFetch)\s*\(/g,
  ))
    features.add('call:' + match[1]);
  for (const match of code.matchAll(
    /\bnew\s+(WebSocket|XMLHttpRequest|EventSource)\s*\(/g,
  ))
    features.add('new:' + match[1]);
  for (const match of code.matchAll(
    /(?:\bfrom\s*|\brequire\s*\(\s*|\bimport\s*\(\s*|\bimport\s*)(['"])((?:node:)?(?:https?|net|tls|http2)|undici|axios|ws)\1/g,
  ))
    features.add('import:' + match[2]);
  return [...features];
}
function checkBrokerCalls(source, operations, file) {
  const code = withoutComments(source);
  if (
    /\b(?:const|let|var)\s+(?!broker\b)[A-Za-z_$][\w$]*\s*=\s*broker\b(?!\s*[.(?])|\bimport\s*\{[^}]*\bbroker\s+as\s+|\b(?:const|let|var)\s*\{[^}]*\bcall\b[^}]*\}\s*=\s*broker\b/.test(
      code,
    )
  )
    throw new Error('broker alias forbidden: ' + file);
  for (const match of code.matchAll(
    /\bbroker\s*\.\s*call\s*\(\s*([\s\S]*?)(?:,|\))/g,
  )) {
    const literal = /^(['"])([^'"]+)\1$/.exec(match[1].trim());
    if (!literal) throw new Error('dynamic broker operation: ' + file);
    if (!operations.some((o) => o.operation === literal[2]))
      throw new Error('unknown broker operation: ' + literal[2]);
  }
}

// A finite local node relay is not a platform HTTP client. Validate its actual request shape
// and trusted Linux provider together; a retained transport entry alone is insufficient.
function checkSharedNodeRelay(files, dedicatedBrowser = false) {
  const file = 'shared/node-transport.js',
    provider = 'shared/ports-plugin.js';
  const compact = (text) =>
    withoutComments(text ?? '')
      .replace(/\s+/g, '')
      .replaceAll('"', "'");
  const code = compact(files[file]),
    source = compact(files[provider]);
  const allowed = [
    'health',
    'resolve',
    'register',
    'lookup',
    'resourceSnapshot',
    'beginTask',
    'issueCapability',
    'finishTask',
    'authorizeCapability',
    'readCredentials',
    'enterpriseOutbound',
    'messageContext',
    'model',
    'send',
    'receive',
  ];
  const operations = /constoperations=newSet\(\[([^\]]*)\]\);/.exec(code);
  if (
    !operations ||
    operations[1] !== allowed.map((name) => "'" + name + "',").join('') ||
    !code.includes(
      "if(!operations.has(operation))fail('common.validation_failed');",
    ) ||
    (code.match(/\brequest\b/g) ?? []).length !== 2 ||
    (code.match(/'node:http'/g) ?? []).length !== 1 ||
    (code.match(/\bsocketPath\b/g) ?? []).length !== 2 ||
    !code.includes("import{request}from'node:http';") ||
    !code.includes('exportfunctioncreateSharedNodeTransport(socketPath){') ||
    !code.includes(
      "constoutgoing=request({socketPath,path:'/_auriga/shared-rpc/'+operation,method:'POST',headers:{'content-type':'application/json','content-length':String(bytes.length),},signal,});",
    )
  )
    throw new Error(
      'shared node relay must remain a finite socket-only POST transport',
    );
  for (const guard of [
    "process.platform!=='linux'",
    'constinitial=read();',
    'constroot=`/var/lib/auriga/pools/${value.physicalInstanceId}`;',
    "config.configFile!==root+'/control/shared-host.json'",
    "value.nodeSocket!==root+'/control/shared-node.sock'",
    '!stat.isFile()||(stat.mode&0o022)!==0||stat.size>262144',
    '!uuid.test(value.physicalInstanceId)',
    'constuuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;',
    "if(!lstatSync(initial.nodeSocket).isSocket())fail('common.unavailable');",
    'constbridge=createSharedNodeTransport(initial.nodeSocket);',
  ])
    if (!source.includes(guard))
      throw new Error(
        'shared node relay lost trusted provider guard: ' + guard,
      );
  if ((source.match(/\bcreateSharedNodeTransport\b/g) ?? []).length !== 2)
    throw new Error('shared node relay provider alias forbidden');
  if (
    /\b(?:initial|value)\.(?:nodeSocket|physicalInstanceId|tenantId)(?:\+=|\|\|=|\?\?=|=(?!=))/.test(
      source,
    )
  )
    throw new Error(
      'shared node relay trusted identity/socket override forbidden',
    );
  for (const [name, text] of Object.entries(files))
    if (
      name !== file &&
      name !== provider &&
      !(dedicatedBrowser && name === 'dedicated/plugin.js') &&
      /\bcreateSharedNodeTransport\b/.test(withoutComments(text))
    )
      throw new Error(
        'shared node relay is callable only by its trusted provider: ' + name,
      );
}

// This is a native browser carrier, not another platform credential client.
// Permit one exact same-origin POST call only when its two callers and the
// actual finite, signed-member server composition are present together.
function checkSharedBrowserCarrier(files) {
  const file = 'shared-browser/bootstrap.js';
  if (!Object.hasOwn(files, file)) return false;
  const compact = (source) =>
    withoutComments(source ?? '')
      .replace(/\s+/g, '')
      .replaceAll('"', "'");
  const visible = withoutComments(files[file]);
  const code = compact(files[file]);
  const native = compact(files['shared-browser/native.js']);
  const handler = compact(files['shared-browser/plugin.js']);
  const reject = () => {
    throw new Error(
      'shared browser must remain a finite same-origin signed-member carrier',
    );
  };
  const count = (source, pattern) => [...source.matchAll(pattern)].length;
  const request =
    "constresponse=awaitfetcher(newURL(`auriga/shared/browser/${kind}`,base),{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'content-type':'application/json'},body:JSON.stringify({channel,endpoint,payload}),signal,},);";
  if (
    !code.includes("constbase=newURL('./',document.baseURI);") ||
    !code.includes('constfetcher=globalThis.fetch.bind(globalThis);') ||
    !code.includes(
      'asyncfunctionsend(kind,channel,endpoint,payload,signal){',
    ) ||
    !/if\(channel!=='\/api'\)thrownewError\('[^']*'\);/.test(code) ||
    !code.includes(request) ||
    !code.includes(
      "constresponse=awaitsend('rpc',channel,endpoint,payload,signal);",
    ) ||
    !code.includes(
      "constresponse=awaitsend('stream',channel,endpoint,payload,signal);",
    ) ||
    !code.includes(
      "constresult=awaitrpc.call('/api',`aurigaShared/${endpoint}`,{args:request===undefined?{}:{request}},signal,);",
    ) ||
    !code.includes('globalThis.__DSH_TRANSPORT__={rpc,ownsHost:false};') ||
    count(visible, /\bfetcher\b/g) !== 2 ||
    count(visible, /\bfetch\b/g) !== 1 ||
    count(visible, /\bbase\b/g) !== 2 ||
    count(visible, /\bkind\b/g) !== 3 ||
    count(visible, /\bsend\b/g) !== 3 ||
    count(code, /rpc\.call\(/g) !== 1 ||
    count(visible, /\bread\s*\(/g) !== 4 ||
    [...visible.matchAll(/\bread\s*\(\s*(['"])([^'"]+)\1/g)]
      .map((match) => match[2])
      .join(',') !== 'messagingSessions,messagingSettings,messagingSettings' ||
    count(code, /\bnewURL\(/g) !== 2 ||
    transportFeatures(files[file]).join(',') !== 'call:fetcher' ||
    /\b(?:token|identity|bearer|authorization|cookie|access_token|refresh_token|serviceJwt)\b|\b(?:import|require)\b/i.test(
      visible,
    )
  )
    reject();

  // The endpoint is wire data for the original DSH client. It is NOT a URL
  // or a member selector. Keep the actual server dispatch table finite, and
  // keep each call bound to the Peer admitted by the Host's signed identity.
  for (const shape of [
    "constunary=newMap([['session/page','session/page'],['session/prompt','session/prompt'],['session/cancel','session/cancel'],['session/projections','session/projections'],]);",
    "conststreams=newMap([['session/follow','session/follow']]);",
    "constglobal=stream?['$events','remoteAccess/follow','session/control']:['session/list','session/modelCatalog','session/canOpenWorkspacePath',];",
    "exact(envelope,['channel','endpoint','payload']);",
    "if(endpoint==='$events/result')fail('shared.revoked');",
    'constselected=awaitprepare(peer,envelope,false);',
    'constselected=awaitprepare(peer,envelope,true);',
    'constbinding=awaithost.create(peer,{});',
  ])
    if (!native.includes(shape)) reject();
  for (const shape of [
    "constprefix='/auriga/shared/browser';",
    "[prefix+'/rpc',prefix+'/stream'].includes(path)",
    "consttoken=req.headers['auriga-identity'];",
    'peer=awaithost.admitBrowser(token,{method:req.method,canonicalPath:path,});',
    "if(peer.principal.kind==='owner')fail('shared.revoked');",
    'constenvelope=awaitjsonBody(req);',
    'native.call(peer,envelope,signal)',
    'native.open(peer,envelope,signal)',
  ])
    if (!handler.includes(shape)) reject();
  if (
    count(handler, /host\.admitBrowser\(/g) !== 1 ||
    count(handler, /native\.call\(/g) !== 1 ||
    count(handler, /native\.open\(/g) !== 1 ||
    /\b(?:authorizeIndex|authenticatedUrl|admitConnection|cookie|authorization|bearer)\b/i.test(
      handler,
    )
  )
    reject();
  // Supplement the semantic checks with the reviewed c498b824 dispatcher
  // shape. Adding a new branch requires an explicit guard review; changing
  // this fingerprint alone never permits a different URL/header/transport.
  // 2026-11-21 review (SHARED-UI-NOWORKSPACE): keeps fixed member Workspace;
  // session/create may omit workspaceId (still binds fixed only) and accept
  // agentPreset string; adds agentPresets/list + agentPresets/select (select
  // authorizes agentId as own session). Empty create still uses host.create
  // (peer,{}). No URL/header/transport/identity source change.
  if (
    createHash('sha256').update(native).digest('hex') !==
    '92527f0d407170165242820f19552bf989887aad8d180585c7b8cc39921fbe1d'
  )
    reject();
  return true;
}

// A dedicated native owner is derived only inside the admitted, unique E/W
// binding. This exception does not grant a browser any owner cookie, identity
// material, URL selector, or a second way to call the local Node transport.
function checkDedicatedBrowserCarrier(files) {
  const file = 'dedicated/bootstrap.js';
  if (!Object.hasOwn(files, file)) return false;
  const compact = (source) =>
    withoutComments(source ?? '')
      .replace(/\s+/g, '')
      .replaceAll('"', "'");
  const visible = withoutComments(files[file]);
  const code = compact(files[file]);
  const browser = compact(files['dedicated/browser.js']);
  const host = compact(files['dedicated/host.js']);
  const provider = compact(files['dedicated/plugin.js']);
  const binding = compact(files['dedicated/intent.js']);
  const bridge = compact(files['dedicated/node-bridge.js']);
  const profile = compact(files['dedicated/profile.js']);
  const count = (source, pattern) => [...source.matchAll(pattern)].length;
  const reject = () => {
    throw new Error(
      'dedicated browser must remain a same-origin unique-binding native carrier',
    );
  };
  const request =
    "constresponse=awaitfetcher(newURL(`auriga/dedicated/browser/${kind}`,base),{method:'POST',credentials:'same-origin',cache:'no-store',headers:{'content-type':'application/json'},body:JSON.stringify({channel,endpoint,payload}),signal,},);";
  if (
    !code.includes("constbase=newURL('./',document.baseURI);") ||
    !code.includes('constfetcher=globalThis.fetch.bind(globalThis);') ||
    !code.includes(
      'asyncfunctionsend(kind,channel,endpoint,payload,signal){',
    ) ||
    !/if\(channel!=='\/api'\)thrownewError\('[^']*'\);/.test(code) ||
    !code.includes(request) ||
    !code.includes(
      "constresult=await(awaitsend('rpc',channel,endpoint,payload,signal)).json();",
    ) ||
    !code.includes(
      "constresponse=awaitsend('stream',channel,endpoint,payload,signal,);",
    ) ||
    !code.includes('globalThis.__DSH_TRANSPORT__={ownsHost:true,rpc:{') ||
    count(visible, /\bfetcher\b/g) !== 2 ||
    count(visible, /\bfetch\b/g) !== 1 ||
    count(visible, /\bbase\b/g) !== 2 ||
    count(visible, /\bkind\b/g) !== 3 ||
    count(visible, /\bsend\b/g) !== 3 ||
    count(visible, /\brpc\b/g) !== 2 ||
    count(code, /\bnewURL\(/g) !== 2 ||
    transportFeatures(files[file]).join(',') !== 'call:fetcher' ||
    /\b(?:token|identity|bearer|authorization|cookie|access_token|refresh_token|serviceJwt|import|require)\b/i.test(
      visible,
    )
  )
    reject();
  for (const shape of [
    "['/auriga/dedicated/browser/rpc','/auriga/dedicated/browser/stream',].includes(path)",
    "typeofreq.headers['auriga-identity']!=='string'",
    "constrow=awaithost.admit(req.headers['auriga-identity'],{method:req.method,canonicalPath:path,});",
    'constsignal=globalThis.AbortSignal.any([abort.signal,row.abort.signal,row.native.signal,]);',
    'awaithost.check(row);signal.throwIfAborted();if(index)',
    'constresult=awaithost.call(row,input,signal);',
    'constsource=awaithost.open(row,input,signal);',
    'awaitdelay(1000,undefined,{signal});awaithost.check(row);',
    "forawait(constvalueofsource){awaithost.check(row);awaitwrite(res,{type:'item',value},signal);}",
  ])
    if (!browser.includes(shape)) reject();
  for (const shape of [
    "constforbidden=newSet(['tenantId','memberId','placementId','physicalInstanceId','platformSessionId','authGeneration','credentialVersion',]);",
    "asyncfunctioncheck(member){if(closed||member.signal?.aborted)fail('shared.revoked');sameBinding(placementBinding(member.principal),binding);awaitruntime.refresh(member);awaitresources.verify();awaitruntime.refresh(member);}",
    "constmember=awaitruntime.admit(token,request,(principal)=>ports.createPeer({principal,interactions:'none'}),);",
    'constkey=canonical(member.principal);letrow=records.get(key);',
    "native:ports.createPeer({principal:Object.freeze({kind:'owner'}),interactions:'answer',}),",
    "awaitruntime.authorize(member,'session/page',id);",
    "Object.keys(input).sort().join(',')!=='channel,endpoint,payload'",
    "input.channel!=='/api'",
    "if(mutations.has(endpoint))fail('common.forbidden');",
    '!ctx.typertGateway.claimsEndpoint(endpoint)',
    'awaitcheckArguments(row.member,input.payload.args);',
    'awaitprepare(row,input,false);signal.throwIfAborted();',
    'awaitprepare(row,input,true);signal.throwIfAborted();',
    'constcreated=awaitruntime.create(row.member,ports.sessions,{});',
    'forawait(letframeofsource){awaitcheck(row.member);signal.throwIfAborted();',
    'awaitcheck(row.member);signal.throwIfAborted();yieldframe;',
  ])
    if (!host.includes(shape)) reject();
  // The installation provider must pin the entire dedicated binding and its
  // Unix socket before it can use the retained local relay factory.
  for (const shape of [
    "process.platform!=='linux'",
    'readOwnedFile(config.configFile,262144)',
    'value.binding=normalizeBinding(value.binding);',
    "config.configFile!==root+'/control/dedicated-host.json'",
    "value.nodeSocket!==root+'/control/shared-node.sock'",
    'constinitial=read(),binding=initial.binding;',
    'sameBinding(value.binding,binding);',
    "if(!lstatSync(initial.nodeSocket).isSocket())fail('common.unavailable');",
    'consttransport=createSharedNodeTransport(initial.nodeSocket);',
    'constbridge=createDedicatedNodeBridge({transport,settings,binding});',
    'profileValidator:(context,expected)=>assertDedicatedProfile(context,expected,resources),',
    'sameBinding(placementBinding(result),binding);',
    'sameBinding(current,binding);',
    'sameBinding(result.binding,binding);',
    "runtime=createSharedRuntime({...options,ports,expectedMode:'dedicated',});",
    'resources=createDedicatedResources(ctx,{binding,',
    'host=createDedicatedHost(ctx,{runtime,ports,binding,resources});',
    "ctx.on('connection/request',(_req,res)=>{res.writeHead(403,{'cache-control':'no-store'});res.end();})",
  ])
    if (!provider.includes(shape)) reject();
  for (const shape of [
    "exact(binding,['tenantId','memberId','placementId','physicalInstanceId','mode','epoch','revision',]);",
    "if(binding.mode!=='dedicated')fail('shared.binding_mismatch');",
    "validate('Binding',left);validate('Binding',right);",
    "left.mode!=='dedicated'||right.mode!=='dedicated'||canonical(left)!==canonical(right)",
  ])
    if (!binding.includes(shape)) reject();
  for (const shape of [
    'if(body?.binding)sameBinding(placementBinding(body.binding),binding);',
    "if(operation!=='finishTask')settings();",
    'constresult=awaittransport.call(operation,body,signal);',
    'if(result?.binding)sameBinding(placementBinding(result.binding),binding);',
  ])
    if (!bridge.includes(shape)) reject();
  for (const shape of [
    "exportconstDEDICATED_PROFILE_DIR='/home/dsh/.host/.dsh/profiles/auriga-dedicated';",
    'canonical(expected)!==canonical(DEDICATED_PROFILE)||!resources?.isCurrent()',
    "profile?.name!=='auriga-dedicated'||profile.dir!==DEDICATED_PROFILE_DIR",
    "['auriga-dsh','auriga-dsh-shared','auriga-dsh-shared-ports','auriga-dsh-shared-browser',].includes(name)",
  ])
    if (!profile.includes(shape)) reject();
  if (
    count(provider, /\bcreateSharedNodeTransport\b/g) !== 2 ||
    count(host, /ports\.createPeer\(/g) !== 2 ||
    count(browser, /host\.admit\(/g) !== 1 ||
    count(bridge, /if\(operation!=='finishTask'\)settings\(\);/g) !== 2 ||
    /\b(?:authorizeIndex|authenticatedUrl|admitConnection|browserPeer|cookie|authorization|bearer)\b/i.test(
      browser + host + provider,
    )
  )
    reject();
  // Supplement, never replace, the request/binding/current-fence semantics
  // above with the two reviewed f90a4ca9 dispatch implementations.
  if (
    createHash('sha256').update(browser).digest('hex') !==
      '87a5622f605746335a8f6ddcfb183cfd1334ecd30efb7fe61b881d9ad1558ddf' ||
    createHash('sha256').update(host).digest('hex') !==
      '81fc4149551219b5dc4534ba2859410b679c960e4121d50711e25dc4ce8aac9d'
  )
    reject();
  return true;
}

export function checkPlatformMigration(inventory, operations, sources) {
  if (!['legacy', 'broker'].includes(inventory.migrationState))
    throw new Error('invalid migrationState');
  const files = sources ?? readPluginSources();
  if (inventory.migrationState === 'legacy') {
    const allowed = new Set(
      inventory.calls.flatMap((c) =>
        routeFeatures(c.evidence).map((route) => c.file + '\n' + route),
      ),
    );
    for (const [file, source] of Object.entries(files))
      for (const route of routeFeatures(source)) {
        if (!allowed.has(file + '\n' + route))
          throw new Error('new direct platform route: ' + file + ':' + route);
      }
    return;
  }
  if (inventory.calls.length)
    throw new Error('broker migration retains legacy inventory');
  const sharedBrowser = checkSharedBrowserCarrier(files);
  const dedicatedBrowser = checkDedicatedBrowserCarrier(files);
  const channels = new Map();
  for (const channel of inventory.retainedChannels ?? []) {
    if (
      !channel.file ||
      channel.file.includes('..') ||
      channels.has(channel.file) ||
      !channel.channel ||
      !channel.reason ||
      !channel.design ||
      !Array.isArray(channel.transports) ||
      !Array.isArray(channel.routePatterns)
    )
      throw new Error('invalid retained channel');
    for (const pattern of channel.routePatterns) {
      if (!pattern.startsWith('^') || !pattern.endsWith('$'))
        throw new Error('unbounded retained route');
      new RegExp(pattern);
    }
    channels.set(channel.file, channel);
  }
  for (const [file, source] of Object.entries(files)) {
    const channel = channels.get(file);
    for (const route of routeFeatures(source)) {
      if (
        !channel?.routePatterns.some((pattern) =>
          new RegExp(pattern).test(route),
        )
      )
        throw new Error('direct platform route: ' + file + ':' + route);
    }
    for (const transport of transportFeatures(source)) {
      if (
        !channel?.transports.includes(transport) &&
        !(
          sharedBrowser &&
          file === 'shared-browser/bootstrap.js' &&
          transport === 'call:fetcher'
        ) &&
        !(
          dedicatedBrowser &&
          file === 'dedicated/bootstrap.js' &&
          transport === 'call:fetcher'
        )
      )
        throw new Error('direct transport: ' + file + ':' + transport);
    }
    checkBrokerCalls(source, operations, file);
  }
  if (files['shared/node-transport.js']) {
    if (
      channels.get('shared/node-transport.js')?.channel !==
        'shared-unix-node-relay' ||
      channels.get('shared/ports-plugin.js')?.channel !==
        'shared-unix-node-provider'
    )
      throw new Error(
        'shared node relay requires its exact documented channel pair',
      );
    checkSharedNodeRelay(files, dedicatedBrowser);
  }
}

export function readPluginSources() {
  const out = {};
  function walk(dir) {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.name === 'generated') continue;
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (/\.(?:[cm]?js|tsx?)$/.test(e.name))
        out[path.relative(base, p).replaceAll('\\', '/')] = fs.readFileSync(
          p,
          'utf8',
        );
    }
  }
  walk(base);
  return out;
}
