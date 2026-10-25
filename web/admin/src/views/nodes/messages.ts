import { useI18n } from 'vue-i18n';
import { isApiError } from '@auriga/shared';

const zh = {
  materials: '加入材料',
  create: '生成加入材料',
  name: '节点备注（可选）',
  ttl: '有效期（分钟，5–1440）',
  once: '加入材料仅显示这一次。请妥善保存，关闭后无法再次查看。',
  token: '加入令牌',
  command: '加入命令',
  ca: 'CA 指纹',
  copy: '复制命令',
  copied: '命令已复制。',
  copyFailed: '复制失败，请手动选择复制。',
  clear: '关闭并清除',
  unused: '未用且未过期的令牌',
  refresh: '刷新加入材料',
  revoke: '撤销',
  confirmRevoke: '确认撤销此加入令牌？撤销后不能再用于节点接入。',
  waiting: '等待接入',
  online: '在线',
  offline: '离线',
  draining: '排空中',
  revoked: '已退役',
  expires: '到期时间',
  empty: '没有未用且未过期的令牌。',
  progress:
    '未用令牌表示等待接入；令牌消失也可能是过期或撤销。请在节点列表核对实际接入结果。',
  retire: '退役节点',
  retireConfirm: '确认退役节点“{name}”？将吊销节点证书；恢复使用需要重新加入。',
  drainFirst: '请先排空节点并停止调度。',
  emptyUnknown: '实例总数未知，无法确认已排空。',
  notEmpty: '节点仍有实例，请等待迁移完成。',
  retired: '节点已经退役。',
  stale: '以下为最后上报值，离线或退役节点的指标不代表实时容量。',
  cpu: 'CPU（毫核，已用 / 容量）',
  memory: '内存（MB，已用 / 容量）',
  disk: '磁盘（GB，已用 / 容量）',
  slots: '实例（总数 / 上限）',
  full: '已上报用量达到容量上限。',
  unknown: '未知',
  version: '代理版本',
  compatibility: '版本兼容性：未知，当前接口未提供兼容结论。',
  heartbeat: '节点离线，心跳连接不可用；请检查代理进程与网络。',
  lastSeen: '最近心跳',
  certificate: '证书到期时间',
  clock: '节点时钟偏差超过 5 秒，请检查时间同步。',
  failed:
    '操作失败，请检查连接并刷新。写操作不会自动重试；签发结果不确定时，请先刷新令牌列表核对。',
  forbidden: '没有节点管理权限，请联系管理员。',
  conflict: '节点状态已变化或仍有实例，请刷新后重试。',
  limited: '请求过于频繁，请稍后手动重试。',
  pollFailed: '自动刷新已暂停，请点击刷新重试。',
  polling: '每 10 秒刷新接入状态；编辑中的配置不会自动覆盖。',
};
const en: Record<keyof typeof zh, string> = {
  materials: 'Join material',
  create: 'Generate join material',
  name: 'Node note (optional)',
  ttl: 'Lifetime (minutes, 5–1440)',
  once: 'Shown once only. Store this material securely; closing permanently clears it from this page.',
  token: 'Join token',
  command: 'Join command',
  ca: 'CA fingerprint',
  copy: 'Copy command',
  copied: 'Command copied.',
  copyFailed: 'Copy failed. Select and copy manually.',
  clear: 'Close and clear',
  unused: 'Unused, unexpired tokens',
  refresh: 'Refresh join material',
  revoke: 'Revoke',
  confirmRevoke: 'Revoke this join token? It will no longer allow enrollment.',
  waiting: 'Waiting for enrollment',
  online: 'Online',
  offline: 'Offline',
  draining: 'Draining',
  revoked: 'Retired',
  expires: 'Expires at',
  empty: 'No unused, unexpired tokens.',
  progress:
    'Unused tokens indicate pending enrollment. A missing token may also have expired or been revoked. Check the node list for actual enrollment.',
  retire: 'Retire node',
  retireConfirm:
    'Retire node “{name}”? Its certificate will be revoked; it must enroll again to return.',
  drainFirst: 'Drain the node and stop scheduling first.',
  emptyUnknown: 'Instance total is unknown; empty state cannot be verified.',
  notEmpty: 'Instances remain. Wait for migration to complete.',
  retired: 'This node is already retired.',
  stale:
    'Last reported values. Offline or retired node metrics are not live capacity.',
  cpu: 'CPU (millicores, used / capacity)',
  memory: 'Memory (MB, used / capacity)',
  disk: 'Disk (GB, used / capacity)',
  slots: 'Instances (total / limit)',
  full: 'Reported usage has reached capacity.',
  unknown: 'Unknown',
  version: 'Agent version',
  compatibility:
    'Version compatibility: unknown; not supplied by the current API.',
  heartbeat:
    'Node offline; heartbeat connection unavailable. Check the agent process and network.',
  lastSeen: 'Last heartbeat',
  certificate: 'Certificate expires at',
  clock: 'Clock skew exceeds 5 seconds. Check time synchronization.',
  failed:
    'Operation failed. Check connectivity and refresh. Writes are not automatically retried; if issuance is uncertain, refresh the token list first.',
  forbidden:
    'Node administration permission required. Contact an administrator.',
  conflict: 'Node state changed or instances remain. Refresh before retrying.',
  limited: 'Too many requests. Retry manually later.',
  pollFailed: 'Automatic refresh paused. Click Refresh to retry.',
  polling:
    'Enrollment state refreshes every 10 seconds. Configuration edits are preserved.',
};
export function useNodesI18n() {
  const i18n = useI18n({
    useScope: 'global',
    messages: { 'zh-CN': { nodeLifecycle: zh }, en: { nodeLifecycle: en } },
  });
  const t = (key: string, params: Record<string, string> = {}) =>
    i18n.t(`nodeLifecycle.${key}`, params);
  const failure = (error: unknown) => {
    const key = isApiError(error)
      ? error.status === 403
        ? 'forbidden'
        : error.status === 409
          ? 'conflict'
          : error.status === 429
            ? 'limited'
            : 'failed'
      : 'failed';
    return `${t(key)}${isApiError(error) ? ` (${error.problem.code})` : ''}`;
  };
  return { ...i18n, t, failure };
}
