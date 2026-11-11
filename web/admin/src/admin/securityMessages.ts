export const securityZh = {
  securityEntries: '访问入口',
  securityEgress: '出网策略',
  securityConfigured: '部署配置（只读）',
  securityEntryHint: '仅展示已校验的安装配置；证书健康与实时可达性尚未接入。',
  securityPortal: '门户',
  securityAdmin: '管理台',
  securityInstances: '实例入口',
  securityRelay: '中转站对外域名',
  securityRelayHint: '中转站公网映射尚未接入',
  securityPath: '门户同源路径 /i/，仅限互信团队',
  securitySubdomain: '每实例独立子域',
  securityTransport: '传输配置',
  securityHttp: 'HTTP（无传输加密）',
  securityHttps: 'HTTPS（证书状态未接入）',
  securityAllow: '允许域名（每行一项）',
  securityDeny: '拒绝域名（每行一项）',
  securityBoundary:
    '私网、回环、链路本地、CGNAT 与元数据地址始终拒绝；域名仍须在连接时校验解析结果。',
  securityStored: '保存版本',
  securityApplied: '生效版本',
  securityReport: '应用回报',
  securityPending:
    '尚未接入策略下发与生效回报。保存仅更新平台配置，不代表出口已应用。',
  securityUpstream: '中转后端地址',
  securityDistributedVersion: '下发版本',
  securitySaved: '已保存',
  securityDistributed: '已下发',
  securityEffective: '已生效',
  securityFailed: '失败',
  securityStale: '回报已过期',
  securityDisabled: '下发未配置',
  securityNode: '出口节点',
  securityReportedAt: '节点回报时间',
  securityEmptyAllow:
    '允许列表为空时拒绝全部实例出网；拒绝列表优先，保存后由出口自动拉取。请刷新查看回报。',
  securityExit: '中转默认上游出口',
  securityDirect: '直连（单提供商代理以中转站配置为准；不受实例出网策略管理）',
  securityConfirm:
    '保存并下发出网策略？此操作需要近期身份验证；生效时会断开已有出网连接。允许列表为空将拒绝全部实例出网。',
  securityUnsupported: '当前策略包含此编辑器不支持的字段或分类，暂不可编辑。',
  securityReload: '请求结果未确认，请刷新后核对保存版本再操作。',
  securityInvalid:
    '仅支持小写域名，可用 *.example.com；不接受地址、URL、端口或内部域名。',
};
export const securityEn: Record<keyof typeof securityZh, string> = {
  securityEntries: 'Access entries',
  securityEgress: 'Egress policy',
  securityConfigured: 'Installation configuration (read only)',
  securityEntryHint:
    'Validated installation settings only. Live certificate health and reachability are not connected.',
  securityPortal: 'Portal',
  securityAdmin: 'Administration',
  securityInstances: 'Instance entry',
  securityRelay: 'Public relay domain',
  securityRelayHint: 'Public relay mapping is not connected',
  securityPath: 'Portal-origin /i/ paths; trusted teams only',
  securitySubdomain: 'Separate subdomain per instance',
  securityTransport: 'Configured transport',
  securityHttp: 'HTTP (unencrypted)',
  securityHttps: 'HTTPS (certificate status unavailable)',
  securityAllow: 'Allowed domains (one per line)',
  securityDeny: 'Denied domains (one per line)',
  securityBoundary:
    'Private, loopback, link-local, CGNAT and metadata addresses are always denied. Resolved addresses must still be checked at connection time.',
  securityStored: 'Stored version',
  securityApplied: 'Applied version',
  securityReport: 'Application report',
  securityPending:
    'Policy distribution and application reports are not connected. Saving updates platform configuration only.',
  securityUpstream: 'Relay backend origin',
  securityDistributedVersion: 'Distributed version',
  securitySaved: 'Saved',
  securityDistributed: 'Distributed',
  securityEffective: 'Effective',
  securityFailed: 'Failed',
  securityStale: 'Report expired',
  securityDisabled: 'Distribution not configured',
  securityNode: 'Exit node',
  securityReportedAt: 'Node report time',
  securityEmptyAllow:
    'An empty allow list denies all instance egress. Deny rules take precedence. The exit pulls saved policies automatically; refresh to check its report.',
  securityExit: 'Default relay upstream exit',
  securityDirect:
    'Direct (per-provider proxies follow relay configuration; instance egress policy does not apply)',
  securityConfirm:
    'Save and distribute egress policy? Recent identity verification is required. Applying it disconnects existing tunnels. An empty allow list denies all instance egress.',
  securityUnsupported:
    'This policy contains unsupported fields or categories and cannot be edited here.',
  securityReload:
    'The write outcome is uncertain. Refresh and check the stored version before continuing.',
  securityInvalid:
    'Use lowercase domains, optionally *.example.com. Addresses, URLs, ports and internal names are not supported.',
};
