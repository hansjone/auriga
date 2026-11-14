export const placementMessages = {
  'zh-CN': {
    title: 'Auriga DSH · 成员模式',
    shared: '共享模式',
    dedicated: '独立容器',
    risks:
      '共享池仅限互信团队。成员可能互相看到文件，并可能以 owner 能力操作。原生 Cookie/browserPeer 将不同成员映射到同一 owner，/connect 也可能构造 owner；签名成员头不自动替换原生 index/WS 身份，禁 shell 等限制可能被旁路。workspaceFiles/read、readBytes、stat 可跨成员路径，绝对路径、父目录、junction、硬链接与元数据均有风险，固定目录不构成文件隔离。成员之间会话内容可见，且可能以其他成员的身份操作内部应用、审计记在被冒用者名下。A 可打开并驱动 B 的会话、读取正文与历史；企业 skill 按会话绑定 B 凭据时，会以 B 执行业务，能力校验通过也不消除冒用。内存耗尽、事件循环阻塞、崩溃影响全池。',
    impact:
      '进程内无法选择性硬杀本人执行，仅支持合作取消。等待30秒超时后，由外部 supervisor 保守停止整个池及执行树，会中断其他成员任务；30秒不保证完成停池，未确认停止保持 blocked。任一统一资源到期或撤销也会停全池。需要保密或独立故障域请使用独立容器。',
    policy:
      '本人文件封存30天，新模式使用空目录，会话始终丢弃。旧任务不恢复；文件取回须本人显式申请并经管理员审计，不恢复执行配置。',
    budget:
      '初始预算（非实测上限）：每池20登记 / 4活跃，每人1活跃、5GiB / 10万inode；内存达80%停止准入，满载扩池，不静默搬迁在途会话。',
    resources:
      '企业 skill 来源：平台统一资源。共享模式不开放任意插件、个人 Key、私有 skill、自行共享 skill/MCP、shell 或终端；需要这些功能请向管理员申请独立容器。',
    missing:
      '权威接线尚未就绪：无法读取成员分配、修订标签与独立授权，切换已禁用。',
    refresh: '刷新状态',
    reason: '切换原因',
    confirm: '我已确认文件、会话处置及全池影响与共享风险',
    switch: '确认切换',
    denied: '无分配管理权限。',
    failed: '请求失败，请刷新权威状态后重试。',
    accepted: '操作已登记，尚不表示新模式可用。',
    operation: '操作 ID',
    lookup: '读取进度 / 重试查询',
    stepup:
      '切换要求管理会话、有效权限及600秒内身份续验；UAC与邀请成员规则一致。服务端要求时显示续验面板。',
    authorization: '独立容器授权',
    granted: '已授权',
    notGranted: '未授权',
    unknown: '尚未取得权威数据',
    preparingList: '维护窗口批量重建',
    listHelp:
      '先通知维护及导出窗口，再登记“继续独立”名单。当前契约尚无名单保存、批量执行及逐成员进度接口，批量操作尚不可用；首次登录不会由此页面静默重建。',
    states: {
      accepted: '已登记',
      fencing: '撤销旧能力',
      draining: '等待旧执行停止',
      preparing: '封存文件并准备空目录',
      committing: '提交分配',
      succeeded: '切换成功，请成员从门户进入新会话',
      failed: '切换失败',
      blocked: '已阻塞，等待确认及恢复',
    },
    failures: {
      capacity: '容量不足',
      'stop-unconfirmed': '无法确认旧执行已停止',
      'prepare-failed': '准备失败',
      revoked: '授权已撤销',
      'recovery-required': '需要恢复处理',
    },
  },
  en: {
    title: 'Auriga DSH · Member mode',
    shared: 'Shared',
    dedicated: 'Dedicated container',
    risks:
      'Shared pools are for mutually trusted teams only. Native cookies/browserPeer may map different members to the same owner; /connect may also create an owner. Signed member headers do not replace native index/WS identity, so restrictions including shell bans may be bypassed. Members may read each other’s files via workspaceFiles/read, readBytes and stat, including absolute paths, parent paths, junctions, hard links and metadata. Fixed working directories do not provide file isolation. Session content is visible between members. A may open and drive B’s session and read its body and history. Enterprise skills bound to B’s credentials may then operate internal applications as B, with audit attributed to B; successful capability checks do not remove impersonation. Memory exhaustion, event-loop stalls and crashes affect the entire pool.',
    impact:
      'In-process execution cannot be selectively hard-killed per member. Cooperative cancellation waits 30 seconds before the external supervisor stops the whole pool and execution tree, interrupting other members. This is not a 30-second stop guarantee; unconfirmed stops remain blocked. Expiry or revocation of any unified resource also stops the pool. Use a dedicated container for confidentiality or an independent failure domain.',
    policy:
      'Archive personal files for 30 days, start an empty workspace and discard all sessions. Old tasks are not restored. File recovery requires an explicit personal request and administrator audit; execution configuration is not restored.',
    budget:
      'Initial budgets, not measured limits: 20 registered / 4 active per pool; 1 active, 5 GiB / 100,000 inodes per member. Stop admission at 80% memory; add pools at capacity without silently moving active sessions.',
    resources:
      'Enterprise skill source: platform unified resources. Shared mode does not offer arbitrary plugins, personal keys, private skills, member-shared skills/MCP, shell or terminal. Ask an administrator for a dedicated container when needed.',
    missing:
      'Authority integration is pending: member placement, ETag and dedicated entitlement cannot be read. Switching is disabled.',
    refresh: 'Refresh status',
    reason: 'Reason',
    confirm:
      'I confirm file/session disposal, pool impact and shared-mode risks',
    switch: 'Confirm switch',
    denied: 'Placement management permission required.',
    failed: 'Request failed. Refresh authoritative state before retrying.',
    accepted: 'Operation registered; the new mode is not yet usable.',
    operation: 'Operation ID',
    lookup: 'Read progress / retry query',
    stepup:
      'Switches require an admin session, permission and verification within 600 seconds, equally for UAC and invited members. A verification dialog opens when required by the server.',
    authorization: 'Dedicated entitlement',
    granted: 'Granted',
    notGranted: 'Not granted',
    unknown: 'Authoritative data unavailable',
    preparingList: 'Maintenance-window batch rebuild',
    listHelp:
      'Notify members of maintenance and export windows, then register the “remain dedicated” list. The contract has no list persistence, batch execution or per-member batch progress API. Batch operations are unavailable; this page does not silently rebuild on first login.',
    states: {
      accepted: 'Registered',
      fencing: 'Revoking old capabilities',
      draining: 'Waiting for old execution to stop',
      preparing: 'Archiving files and preparing empty workspace',
      committing: 'Committing placement',
      succeeded: 'Switched; enter a new session from the portal',
      failed: 'Failed',
      blocked: 'Blocked pending confirmation and recovery',
    },
    failures: {
      capacity: 'Capacity exhausted',
      'stop-unconfirmed': 'Old execution stop unconfirmed',
      'prepare-failed': 'Preparation failed',
      revoked: 'Authorization revoked',
      'recovery-required': 'Recovery required',
    },
  },
};
