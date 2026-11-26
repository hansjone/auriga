export const placementMessages = {
  'zh-CN': {
    signIn: '重新登录',
    states: {
      pending: '等待分配',
      ready: '已就绪',
      switching: '切换中',
      unavailable: '暂不可用',
    },
    title: 'Auriga DSH',
    market: '我的 DSH 资源',
    shared: '共享模式',
    dedicated: '独立容器',
    empty: '尚未分配。新成员首次打开时将分配共享实例。',
    open: '打开 Auriga DSH',
    refresh: '刷新状态',
    failed: '暂时无法打开，请刷新状态后重试。',
    waiting: '分配尚未就绪，请稍后刷新。',
    migration:
      '现有独享实例待迁移。请联系管理员在管理台完成维护迁移，完成后即可进入新模式。',
    fallbackAction: '继续进入现有独享实例',
    switching: '模式正在切换中，请稍候。',
    refreshLater: '稍后可刷新状态。',
    unavailable: '当前实例暂不可用，请稍后重试或联系管理员。',
    backToShared: '返回共享入口',
    resource:
      '企业 skill 仅来自平台统一资源。需要个人 Key、私有 skill、任意插件、shell 或终端时，请向管理员申请独立容器；获准后仍从此入口进入。',
    risk: '共享仅适用于互信团队。原生入口仍可能具有 owner 能力；成员可能互看文件和会话，并以其他成员身份操作内部应用，审计记在被冒用者名下。阻塞、崩溃、资源失效及取消超时可能停止全池，中断其他成员任务。',
    policy:
      '模式切换会封存本人文件30天，以空目录开始并丢弃会话；文件恢复须显式申请。',
    expired: '登录已失效，请重新登录。',
  },
  en: {
    signIn: 'Sign in again',
    states: {
      pending: 'Pending',
      ready: 'Ready',
      switching: 'Switching',
      unavailable: 'Unavailable',
    },
    title: 'Auriga DSH',
    market: 'My DSH resources',
    shared: 'Shared',
    dedicated: 'Dedicated container',
    empty:
      'No placement yet. New members receive a shared placement on first open.',
    open: 'Open Auriga DSH',
    refresh: 'Refresh status',
    failed: 'Unable to open. Refresh status and retry.',
    waiting: 'Placement is not ready. Refresh later.',
    migration:
      'Your existing dedicated instance needs migration. Ask an administrator to migrate it in the admin console before entering the new mode.',
    fallbackAction: 'Continue to existing dedicated instance',
    switching: 'Your workspace mode is switching. Please wait.',
    refreshLater: 'Refresh the status in a moment.',
    unavailable:
      'Your instance is temporarily unavailable. Retry later or contact your administrator.',
    backToShared: 'Back to shared entry',
    resource:
      'Enterprise skills come only from platform unified resources. Ask an administrator for a dedicated container for personal keys, private skills, arbitrary plugins, shell or terminal; use this same entry after approval.',
    risk: 'Shared mode is for trusted teams. Native entries may retain owner capabilities. Members may read each other’s files and sessions and operate internal applications as another member, with audit attributed to that member. Stalls, crashes, resource expiry and cancellation timeouts can stop the entire pool and interrupt other members.',
    policy:
      'Mode switches archive personal files for 30 days, start an empty workspace and discard sessions. File recovery requires an explicit request.',
    expired: 'Session expired. Sign in again.',
  },
};
