export const placementMessages = {
  'zh-CN': {
    title: 'Auriga DSH',
    shared: '共享模式',
    dedicated: '独立容器',
    empty: '尚未分配，首次打开将创建共享分配。',
    open: '打开 Auriga DSH',
    refresh: '刷新状态',
    failed: '暂时无法打开，请刷新状态后重试。',
    waiting: '分配尚未就绪，请稍后刷新。',
    resource:
      '企业 skill 仅来自平台统一资源。需要个人 Key、私有 skill、任意插件、shell 或终端时，请向管理员申请独立容器；获准后仍从此入口进入。',
    risk: '共享仅适用于互信团队。原生入口仍可能具有 owner 能力；成员可能互看文件和会话，并以其他成员身份操作内部应用，审计记在被冒用者名下。阻塞、崩溃、资源失效及取消超时可能停止全池，中断其他成员任务。',
    policy:
      '模式切换会封存本人文件30天，以空目录开始并丢弃会话；文件恢复须显式申请。',
    expired: '登录已失效，请重新登录。',
  },
  en: {
    title: 'Auriga DSH',
    shared: 'Shared',
    dedicated: 'Dedicated container',
    empty: 'No placement yet. First open creates a shared placement.',
    open: 'Open Auriga DSH',
    refresh: 'Refresh status',
    failed: 'Unable to open. Refresh status and retry.',
    waiting: 'Placement is not ready. Refresh later.',
    resource:
      'Enterprise skills come only from platform unified resources. Ask an administrator for a dedicated container for personal keys, private skills, arbitrary plugins, shell or terminal; use this same entry after approval.',
    risk: 'Shared mode is for trusted teams. Native entries may retain owner capabilities. Members may read each other’s files and sessions and operate internal applications as another member, with audit attributed to that member. Stalls, crashes, resource expiry and cancellation timeouts can stop the entire pool and interrupt other members.',
    policy:
      'Mode switches archive personal files for 30 days, start an empty workspace and discard sessions. File recovery requires an explicit request.',
    expired: 'Session expired. Sign in again.',
  },
};
