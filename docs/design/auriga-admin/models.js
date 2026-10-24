// 模型管理交互原型：全部数据与测试结果仅在浏览器内模拟。
tabs.models=[['relay','中转站'],['providers','提供商与模型'],['auto','Auto 选模'],['usage','用量'],['keys','API Key']];
const modelMeta={
  'model-1':{publicId:'general',upstream:'general-v2',context:128000,output:8192,image:true},
  'model-2':{publicId:'reasoning',upstream:'reasoner-v1',context:64000,output:16384,image:false},
  'model-3':{publicId:'backup',upstream:'general-backup',context:32000,output:8192,image:true}
};
state.modelDesign??={mode:'manual',pool:['model-1','model-2','model-3'],limit:200000};
let autoDraft,autoCursor=0;
const poolModels=()=>state.modelDesign.pool.map(id=>providers.find(m=>m.id===id)).filter(Boolean);
const modelTitle=id=>providers.find(m=>m.id===id)?.name||id;
const modelStatus=m=>m.status==='error'?'<span class="status bad">熔断中</span>':badge('ok');
const modelProposal='<span class="tag">能力规划</span>';
function modelWorkspace(){
  if(tab==='relay')return relayView();
  if(tab==='providers')return providerView();
  if(tab==='auto')return autoView();
  if(tab==='usage')return modelUsageView();
  return modelKeysView();
}
function relayView(){
  const healthy=providers.filter(m=>m.status==='ok').length;
  return '<div class="stats">'+[
    ['今日请求','1,723','示例统计 · 今日'],['成功率','98.6%','含失败请求'],['可用模型',healthy+' / '+providers.length,'1 个模型熔断中'],['今日 Token','2.37M','输入 1.68M · 输出 0.69M']
  ].map(([k,v,s])=>'<div class="stat"><div class="stat-label">'+k+'</div><div class="stat-number">'+v+'</div><div class="stat-foot">'+s+'</div></div>').join('')+'</div>'+
  '<div class="model-grid"><section class="panel"><div class="panel-head"><h2>模型调用入口</h2><span>OpenAI 兼容</span></div><div class="model-pad"><div class="endpoint"><span class="mono">https://ai.auriga.example/ai/v1</span>'+btn('接入说明','llm-access','','small')+'</div><div class="relay-flow"><div><b>调用方</b><span>云端 DSH · 现场 DSH · API 客户端</span></div><span>→</span><div><b>Auriga 网关</b><span>认证 · 配额 · 计量</span></div><span>→</span><div><b>上游模型</b><span>统一管理提供商</span></div></div><div class="model-links">'+btn('查看可用模型','go','models/providers')+btn('管理 API Key','go','models/keys')+'</div></div></section>'+
  '<section class="panel"><div class="panel-head"><h2>Auto 模型池 '+modelProposal+'</h2>'+btn('配置','go','models/auto','small')+'</div><div class="health-row"><span>选模方式</span><b>'+autoModeLabel(state.modelDesign.mode)+'</b></div><div class="health-row"><span>首选可用模型</span><b>'+esc(poolModels().find(m=>m.status==='ok')?.name||'暂无可用模型')+'</b></div><div class="health-row"><span>模型池</span><span>'+poolModels().filter(m=>m.status==='ok').length+' 可用 / '+poolModels().length+' 已配置</span></div><div class="health-row"><span>故障处理</span><span>输出开始前按策略切换</span></div></section></div>'+
  '<section class="panel model-space"><div class="panel-head"><h2>近期调用</h2><span>示例 · 不保存对话正文</span></div>'+table(['时间','调用方','请求模型','实际模型','结果','Token'],[
    ['10:42:18','平台内置功能','<span class="mono">auto</span>','通用模型',badge('ok'),'1,240'],
    ['10:41:53','研发测试 Key','<span class="mono">reasoning</span>','推理模型',badge('ok'),'3,182'],
    ['10:41:20','平台内置功能','<span class="mono">auto</span>','通用模型','<span class="status warn">切换后成功</span>','2,460'],
    ['10:40:07','研发测试 Key','<span class="mono">backup</span>','备用模型',badge('error'),'—']
  ])+'</section>';
}
const autoModeLabel=mode=>mode==='automatic'?'自动轮询':'手动优先';
function providerView(){
  return '<div class="toolbar"><span class="sub">3 个提供商 · 3 个模型</span><span class="sub">上游凭据仅由服务端保管</span></div><div class="stack">'+providers.map((m,i)=>'<section class="panel"><div class="panel-head"><div><h2>'+m.provider+'</h2><span class="mono">https://provider-'+String.fromCharCode(97+i)+'.example/v1</span></div><div class="head-actions">'+btn('获取模型','llm-fetch',m.id,'small')+btn('提供商设置','llm-provider',m.id,'small')+'</div></div>'+table(['模型 / 对外 ID','上游模型','上下文 / 最大输出','输入','健康',''],[[
    '<button class="name-button" data-action="model" data-id="'+m.id+'">'+m.name+'</button><span class="sub mono">'+modelMeta[m.id].publicId+'</span>',
    '<span class="mono">'+modelMeta[m.id].upstream+'</span>',modelMeta[m.id].context/1000+'K / '+modelMeta[m.id].output/1024+'K',modelMeta[m.id].image?'文本、图片':'文本',modelStatus(m),btn('测试','llm-test',m.id,'small')
  ]])+'</section>').join('')+'<div class="footnote">图片能力用于后续选模设计；Auriga G1 当前仅支持文本与工具消息。</div>';
}
function autoView(){
  autoDraft=structuredClone(state.modelDesign);
  return '<div class="toolbar"><span class="sub">内置模型别名 <span class="mono">auto</span> '+modelProposal+'</span>'+btn('测试 Auto','llm-test','auto')+'</div><section class="panel"><div class="panel-head"><h2>选模策略</h2><span id="auto-dirty">已保存 · 示例</span></div><div class="model-pad auto-compact"><div class="auto-modes">'+[
    ['manual','手动优先','按模型池顺序，优先使用第一个可用模型。'],['automatic','自动轮询','在符合条件的可用模型之间轮流分配。']
  ].map(([v,n,d])=>'<label class="auto-mode"><input type="radio" name="auto-mode" value="'+v+'" '+(autoDraft.mode===v?'checked':'')+'><span><b>'+n+'</b><span class="sub">'+d+'</span></span></label>').join('')+'</div><div class="auto-save-row"><label class="form-field">每人每日 Token 上限<input id="auto-limit" class="input" type="number" min="0" step="1000" value="'+autoDraft.limit+'"></label><div class="model-links">'+btn('保存示例配置','llm-save-auto','','primary')+btn('重置修改','llm-reset-auto')+'</div></div><div id="auto-error" class="error" role="alert"></div></div></section>'+
  '<section class="panel model-space"><div class="panel-head"><h2>模型池</h2><span>顺序影响优先选择与故障切换</span></div><div id="auto-pool">'+autoPoolRows()+'</div><div class="model-pad pool-add"><select id="auto-add-choice" aria-label="选择加入模型池的模型">'+autoPoolOptions()+'</select>'+btn('加入模型池','llm-pool-add')+'</div></section><details class="panel model-space model-rules"><summary>故障切换与能力规则</summary><div class="model-pad"><div class="rule-item"><b>先匹配能力，再选择模型</b><span>筛选图片、上下文、健康与配额；按优先级或轮询，最多尝试 3 个模型。</span></div><div class="rule-item"><b>输出开始后不切换</b><span>流中断直接报错；熔断后的试探仅由请求或管理员测试触发。</span></div><div class="rule-item"><b>限额迁移</b><span>旧 Hub 的 0 为不限；Auriga 的 0 配额为禁止，正式接入需显式映射。</span></div></div></details><div class="footnote">Auto 与故障切换为待迁移能力；当前原型不会调用上游。测试使用已保存的示例配置。</div>';
}
function autoPoolRows(){return autoDraft.pool.length?table(['顺序','模型','能力','健康',''],autoDraft.pool.map((id,i)=>{const m=providers.find(x=>x.id===id),meta=modelMeta[id];return [String(i+1),m.name+'<span class="sub mono">'+meta.publicId+'</span>',meta.context/1000+'K · '+(meta.image?'文本、图片':'文本'),modelStatus(m),'<div class="head-actions"><button class="btn small" data-action="llm-pool-up" data-id="'+id+'" '+(i===0?'disabled':'')+'>上移</button><button class="btn small" data-action="llm-pool-down" data-id="'+id+'" '+(i===autoDraft.pool.length-1?'disabled':'')+'>下移</button>'+btn('移除','llm-pool-remove',id,'small danger')+'</div>']})):'<div class="empty">模型池为空，Auto 将不可用。</div>'}
function autoPoolOptions(){const rest=providers.filter(m=>!autoDraft.pool.includes(m.id));return rest.length?rest.map(m=>'<option value="'+m.id+'">'+m.name+'</option>').join(''):'<option value="">所有模型已加入</option>'}
function dirtyAuto(){if($('auto-dirty'))$('auto-dirty').textContent='有未保存修改'}
function updateAutoPool(){ $('auto-pool').innerHTML=autoPoolRows();$('auto-add-choice').innerHTML=autoPoolOptions();dirtyAuto() }
function modelUsageView(){
  return '<div class="toolbar"><span class="sub">今日 · 2026-10-23</span><select id="usage-dimension" aria-label="用量统计维度"><option value="member">按成员</option><option value="key">按 API Key</option><option value="provider">按提供商</option><option value="model">按实际模型</option></select></div><section class="panel" id="model-usage-table">'+usageRows('member')+'</section><div class="footnote">仅统计平台网关调用；Auto 用量归入实际服务模型，并单独记录 Auto 来源。</div>';
}
function usageRows(dimension){
  const rows={member:[['陈远','623','420','620K / 210K'],['许知','318','218','310K / 130K'],['苏禾','502','360','480K / 230K'],['周宁','280','182','270K / 120K']],key:[['研发测试 Key','818','400','900K / 340K'],['自动化服务 Key','905','780','780K / 350K']],provider:[['供应商 A','1,284','1,000','1.20M / 450K'],['供应商 B','416','180','460K / 230K'],['供应商 C','23','0','20K / 10K']],model:[['通用模型 · general','1,284','1,000','1.20M / 450K'],['推理模型 · reasoning','416','180','460K / 230K'],['备用模型 · backup','23','0','20K / 10K']]};
  return table([{member:'成员',key:'API Key',provider:'提供商',model:'实际模型'}[dimension],'总请求','经 Auto 请求','输入 / 输出 Token'],rows[dimension]);
}
function modelKeysView(){
  return '<div class="toolbar"><span class="sub">外部客户端接入 '+modelProposal+'</span>'+btn('签发流程','llm-key-flow')+'</div>'+listing(['名称 / 归属','Key 前缀','今日 Token / 上限','到期时间','最近调用','状态'],[
    {status:'active',cells:['研发测试 Key<span class="sub">陈远</span>','<span class="mono">sk-demo-a…</span>','120K / 500K','2026-11-23','刚刚',badge('active')]},
    {status:'active',cells:['自动化服务 Key<span class="sub">许知</span>','<span class="mono">sk-demo-b…</span>','86K / 300K','2026-12-31','8 分钟前',badge('active')]},
    {status:'disabled',cells:['旧测试 Key<span class="sub">周宁</span>','<span class="mono">sk-demo-c…</span>','0 / 100K','2026-10-20','3 天前','<span class="status">已吊销</span>']}
  ])+'<div class="footnote">与提供商的上游 API Key 分开管理。签发后只展示一次完整值；列表仅显示前缀。</div>';
}
function modelTest(id){
  const target=id==='auto'?'Auto':modelTitle(id);
  openDialog('测试 '+target,'<div class="notice subtle">模拟选模，不发送真实请求，也不生成模型回复。</div><label class="form-field">请求场景<select id="llm-scenario"><option value="text">普通文本 · 4K 上下文</option><option value="image">含图片 · 4K 上下文</option><option value="long">长上下文 · 96K</option><option value="failure">首选模型在输出前失败</option></select></label><div class="model-links">'+btn('运行模拟','llm-run-test',id,'primary')+'</div><div id="llm-test-result" class="model-space" role="status"></div>',btn('关闭','close'));
}
function runModelTest(id){
  const scenario=$('llm-scenario').value,trace=[];
  let eligible=(id==='auto'?poolModels():providers.filter(m=>m.id===id)).filter(m=>{
    const meta=modelMeta[m.id],reason=id==='auto'&&m.status==='error'?'熔断中':scenario==='image'&&!meta.image?'不支持图片':scenario==='long'&&meta.context<96000?'上下文不足':'';
    if(reason)trace.push(m.name+'：跳过，'+reason);return !reason;
  });
  if(id==='auto'&&state.modelDesign.mode==='automatic'&&eligible.length){const start=autoCursor++%eligible.length;eligible=eligible.slice(start).concat(eligible.slice(0,start))}
  let chosen=null,attempts=0;
  for(const m of eligible){if(attempts>=3)break;attempts++;if((scenario==='failure'&&attempts===1)||m.status==='error'){trace.push(m.name+'：输出前失败');if(id!=='auto')break;continue}chosen=m;trace.push(m.name+'：模拟命中');break}
  $('llm-test-result').innerHTML=facts([['请求模型',id==='auto'?'auto':modelMeta[id].publicId],['实际模型',chosen?chosen.name:'无可用模型'],['策略',id==='auto'?autoModeLabel(state.modelDesign.mode):'指定模型，不切换'],['尝试次数',String(attempts)]])+trace.map(s=>'<div class="change-line">'+esc(s)+'</div>').join('')+'<div class="notice subtle">'+(chosen?'选模模拟成功；未请求上游，耗时与 Token 不作伪造统计。':'模拟不可用。检查模型池、能力要求与健康状态。')+'</div>';
}
document.addEventListener('click',e=>{
  const el=e.target.closest('[data-action^="llm-"]');if(!el||el.disabled||role!=='super')return;
  const a=el.dataset.action,id=el.dataset.id;
  if(a==='llm-test')return modelTest(id);
  if(a==='llm-run-test')return runModelTest(id);
  if(a==='llm-reset-auto')return render();
  if(a.startsWith('llm-pool-')){
    const i=autoDraft.pool.indexOf(id);
    if(a==='llm-pool-add'){const v=$('auto-add-choice').value;if(v&&!autoDraft.pool.includes(v))autoDraft.pool.push(v)}
    else if(a==='llm-pool-remove'&&i>=0)autoDraft.pool.splice(i,1);
    else{const j=i+(a==='llm-pool-up'?-1:1);if(i>=0&&j>=0&&j<autoDraft.pool.length)[autoDraft.pool[i],autoDraft.pool[j]]=[autoDraft.pool[j],autoDraft.pool[i]]}
    return updateAutoPool();
  }
  if(a==='llm-save-auto'){
    const field=$('auto-limit'),limit=Number(field.value);if(!field.value.trim()||!Number.isSafeInteger(limit)||limit<0){$('auto-error').textContent='请输入大于或等于 0 的整数。';return}
    autoDraft.mode=document.querySelector('[name="auto-mode"]:checked').value;autoDraft.limit=limit;state.modelDesign=structuredClone(autoDraft);autoCursor=0;
    record('保存 Auto 示例配置',autoModeLabel(autoDraft.mode)+' · '+autoDraft.pool.length+' 个模型','模型');render();return toast('示例配置已保存，仅对本地原型生效。');
  }
  if(a==='llm-access')return openDialog('中转站接入',facts([['示例 Base URL','https://ai.auriga.example/ai/v1'],['协议','OpenAI 兼容'],['接口','GET /models · POST /chat/completions'],['外部客户端','API Key 接入 · 待迁移']])+'<h3 class="detail-h">按调用方区分认证</h3><div class="change-line">云端 DSH：受信实例通道</div><div class="change-line">现场 DSH：设备身份与模型授权</div><div class="change-line">外部客户端：专用模型 API Key，不可访问管理接口</div><div class="notice subtle">地址为示例。Auriga G1 暂不接受外部 API Key，也不包含 Auto 故障切换；需补充契约后实现。</div>');
  if(a==='llm-provider'){const m=providers.find(x=>x.id===id);return openDialog(m.provider+' · 设置',facts([['提供商',m.provider],['协议','OpenAI 兼容'],['上游 Key','已配置 · 只写不读'],['模型数','1']])+'<h3 class="detail-h">配置行为</h3><div class="change-line">地址与 Key 只配一次，由该提供商下的模型共用。</div><div class="change-line">更新 Key 后，关联模型使用新凭据。</div><div class="change-line">删除前展示关联模型与 Auto 模型池的影响。</div><div class="notice subtle">此处为配置设计说明，不写入真实上游凭据。</div>')}
  if(a==='llm-fetch'){const m=providers.find(x=>x.id===id);return openDialog('获取模型 · '+m.provider,'<div class="notice subtle">上游发现列表示例；正式产品勾选后批量导入。当前未连接上游。</div>'+table(['上游模型','对外 ID','状态'],[[modelMeta[id].upstream,modelMeta[id].publicId,'已添加'],['example-new-model','example-new-model','待导入示例']])+'<div class="change-line">重名需要修改对外 ID；auto 为保留 ID。</div>')}
  if(a==='llm-key-flow')return openDialog('签发模型 API Key','<div class="notice subtle">待迁移能力。本原型不签发真实密钥。</div><div class="change-line">1. 填写名称、绑定成员、每日 Token 上限与到期时间。</div><div class="change-line">2. 重新验证管理员身份，确认授权范围。</div><div class="change-line">3. 创建后仅显示一次完整 Key。</div><div class="change-line">4. 列表保留前缀与用量，支持吊销；过期或成员停用时失效。</div><div class="change-line">模型 Key 只用于模型接口，不能用于登录或后台管理。</div>');
});
document.addEventListener('change',e=>{if(e.target.name==='auto-mode')dirtyAuto();if(e.target.id==='usage-dimension')$('model-usage-table').innerHTML=usageRows(e.target.value)});
document.addEventListener('input',e=>{if(e.target.id==='auto-limit')dirtyAuto()});
