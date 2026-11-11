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
  securityUpstream: '中转上游出口状态尚未接入',
  securityConfirm:
    '保存出网策略？此操作需要近期身份验证；不会自动改变出口运行配置。',
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
  securityUpstream: 'Relay upstream exit status is not connected',
  securityConfirm:
    'Save egress policy? Recent identity verification is required. This does not automatically change exit runtime configuration.',
  securityUnsupported:
    'This policy contains unsupported fields or categories and cannot be edited here.',
  securityReload:
    'The write outcome is uncertain. Refresh and check the stored version before continuing.',
  securityInvalid:
    'Use lowercase domains, optionally *.example.com. Addresses, URLs, ports and internal names are not supported.',
};
