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
  securityUnconfigured: '未配置，沿用默认出网',
  securityMode: '出网模式',
  securityOpen: '开放（沿用环境默认）',
  securityAllowlist: '仅允许指定域名',
  securityRestore: '恢复默认出网',
  securityRestoreConfirm:
    '恢复默认出网？将清除平台允许与拒绝规则，保留部署拒绝列表和私网边界。',
  securityDenyAllConfirm:
    '允许列表为空。保存后将拒绝全部实例出网，并断开受影响的连接。确认保存？',
  securityLegacy: '旧策略已兼容：不支持项已忽略，保存可确认当前规则。',
  securityReportInvalid:
    '节点回报不一致或策略版本落后，下发记录已保留；请核对出口状态。',
  securityExitUnobserved: '由中转站与容器运行配置决定，尚未接入出口观测',
  securityNode: '出口节点',
  securityReportedAt: '节点回报时间',
  securityEmptyAllow:
    '开放模式按拒绝项收紧；白名单模式只允许列表内目标，空列表将拒绝全部。仅受影响的连接会断开。',
  securityExit: '中转默认上游出口',
  securityDirect: '直连（单提供商代理以中转站配置为准；不受实例出网策略管理）',
  securityConfirm:
    '保存并下发出网策略？此操作需要近期身份验证；仅新策略拒绝的现有连接会断开。',
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
  securityUnconfigured: 'Not configured; using default egress',
  securityMode: 'Egress mode',
  securityOpen: 'Open (deployment default)',
  securityAllowlist: 'Allow specified domains only',
  securityRestore: 'Restore default egress',
  securityRestoreConfirm:
    'Restore default egress? Platform allow and deny rules will be cleared. Deployment protections and private-address restrictions remain.',
  securityDenyAllConfirm:
    'The allow list is empty. Saving will deny all instance egress and close affected tunnels. Save?',
  securityLegacy:
    'Legacy policy adjusted: unsupported items were omitted. Save to confirm the current rules.',
  securityReportInvalid:
    'The node report is inconsistent or the policy version is behind. The delivery record was retained; check the exit status.',
  securityExitUnobserved:
    'Determined by relay and container configuration; upstream exit observation is unavailable',
  securityNode: 'Exit node',
  securityReportedAt: 'Node report time',
  securityEmptyAllow:
    'Open mode applies deny rules. Allowlist mode permits only listed domains; an empty list denies all. Only affected tunnels are closed.',
  securityExit: 'Default relay upstream exit',
  securityDirect:
    'Direct (per-provider proxies follow relay configuration; instance egress policy does not apply)',
  securityConfirm:
    'Save and distribute egress policy? Recent identity verification is required. Only existing tunnels denied by the new policy will close.',
  securityUnsupported:
    'This policy contains unsupported fields or categories and cannot be edited here.',
  securityReload:
    'The write outcome is uncertain. Refresh and check the stored version before continuing.',
  securityInvalid:
    'Use lowercase domains, optionally *.example.com. Addresses, URLs, ports and internal names are not supported.',
};
