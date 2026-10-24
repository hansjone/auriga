// 总部是平台系统 DSH；入口展示不启动实例或发送聊天。
tabs.runtime.push(['headquarters','总部 Agent']);
function headquartersEntry(){
  if(!permitted('runtime'))return '';
  return '<section class="panel hq-entry"><div class="task-symbol">'+icon('runtime')+'</div><div class="task-body"><div class="task-title">总部 Agent <span class="tag">系统 DSH</span></div><span class="sub">独立工作区 · 在 DSH 中发起聊天与任务</span></div>'+btn('运行详情','go','runtime/headquarters')+btn('进入总部 DSH ↗','hq-enter','','primary')+'</section>';
}
function headquartersPage(){
  return '<section class="panel"><div class="panel-head"><h2>总部 Agent <span class="tag">系统 DSH</span></h2>'+badge('running')+'</div><div class="model-pad">'+facts([['归属','Auriga 总部'],['实例类型','平台系统实例'],['工作区','总部独立工作区'],['访问者','当前管理员 · 林清'],['Agent 身份','总部 Agent'],['默认模型','Auto · 设计示例']])+'<div class="notice subtle">进入总部的 DSH 工作界面，主动发起聊天、安排任务并查看结果。</div><div class="model-links">'+btn('进入总部 DSH ↗','hq-enter','','primary')+'</div></div></section><section class="panel model-space"><div class="panel-head"><h2>运行与访问</h2><span>设计示例</span></div><div class="health-row"><span>访问权限</span><span>获授权的管理员</span></div><div class="health-row"><span>运行策略</span><span>按部署配置；进入前检查就绪</span></div><div class="health-row"><span>会话归属</span><span>总部工作区</span></div><div class="health-row"><span>操作追踪</span><span>记录实际进入与发起操作的管理员</span></div></section><div class="footnote">总部实例与成员个人实例分开管理；原型未连接实际系统 DSH。</div>';
}
document.addEventListener('click',e=>{
  const el=e.target.closest('[data-action="hq-enter"]');if(!el||!permitted('runtime'))return;
  openDialog('进入总部 DSH',facts([['目标','Auriga 总部系统实例'],['访问者','管理员 · 林清'],['Agent 身份','总部 Agent'],['会话与数据','总部独立工作区']])+'<div class="notice subtle">正式产品将在新标签页打开总部 DSH。当前是入口设计预览，尚未配置真实访问地址。</div><div class="change-line">进入后由管理员主动发起聊天，不自动创建或发送任务。</div><div class="change-line">更换访问管理员后，总部的身份、工作区与历史会话保持不变。</div>',btn('关闭','close'));
});
