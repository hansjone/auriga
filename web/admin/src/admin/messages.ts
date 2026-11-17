import { securityZh, securityEn } from './securityMessages';
import { credentialsZh, credentialsEn } from './credentialsMessages';
import { devicesZh, devicesEn } from './devicesMessages';
import { runtimeZh, runtimeEn } from './runtimeMessages';
import { useI18n } from 'vue-i18n';
import { orgZh, orgEn } from './orgMessages';
import { quotaZh, quotaEn } from './quotaMessages';
import { rolesZh, rolesEn } from './rolesMessages';
import { resourceZh, resourceEn } from './resourceMessages';
import { providersZh, providersEn } from './providersMessages';
import { settingsZh, settingsEn } from './settingsMessages';

const zh = {
  desktopTitle: '客户端发布与更新',
  desktopIntro: '查看渠道当前版本，校验并发布已离线签名的安装包。',
  desktopChannels: '渠道当前生效版本',
  desktopNoActive: '暂无生效版本',
  desktopExpires: '清单有效期至',
  desktopExpired:
    '签名清单已过期，客户端将拒绝更新，请以更高 sequence 续签发布。',
  desktopWorkflow: '发布流程',
  desktopBuild: '构建',
  desktopBuildHelp:
    '通过 GitHub CI 或现场 auriga-client 套件生成 Windows exe 与构建记录。',
  desktopSign: '离线签名',
  desktopSignHelp:
    '用 review-only 审阅来源和摘要，人工确认后签名。发行私钥保存在离线签名机。',
  desktopUpload: '上传发布',
  desktopUploadHelp:
    '选择已签清单与 exe，核对版本、策略、包摘要和登记公钥，再确认发布。',
  desktopUpdate: '客户端更新',
  desktopUpdateHelp:
    '客户端按渠道与灰度检查更新，验签后下载，重启应用。安装冒烟失败时回退到原版本。',
  desktopGuide: '查看 MAINTAIN.md 客户端发布操作说明',
  desktopVerify: '验证身份',
  desktopStepUpHint:
    '发布/下架前需验证身份，读取台账无需验证。操作时平台会检查验证是否仍有效。',
  desktopVerified: '本页已完成身份验证，发布/下架时平台仍会检查有效期。',
  desktopStepUpRequired: '需要近期身份验证，请点击“验证身份”后重试。',
  desktopLoginRequired: '会话已失效，请重新登录。',
  desktopServiceError: '服务或网络不可用，请刷新重试。',
  desktopRequestError: '请求未完成，请检查服务返回的错误码。',
  desktopPolicyConflict: '发行序号或渠道策略已变化，请刷新台账并重新校验。',
  desktopSignatureInvalid:
    '清单签名或字段校验失败，请检查登记公钥及离线签名材料。',
  desktopValidate: '校验文件',
  desktopChecking: '校验中…',
  desktopHashProgress: '本地安装包 SHA256 校验进度 {percentage}%',
  desktopFilesInvalid:
    '请选择不超过 32 KiB 的 JSON 与有效 exe（不超过 4 GiB）。',
  desktopManifestInvalid:
    '清单字段无效，sequence 必须高于全平台历史序号，请检查已签原件。',
  desktopSizeMismatch: 'exe 大小与清单不一致，已阻止发布。',
  desktopHashMismatch: 'exe SHA256 与清单不一致，已阻止发布。',
  desktopKeyMissing: '平台未返回登记公钥，请检查服务配置并刷新台账。',
  desktopKeyMismatch: '签名公钥指纹与平台登记指纹不一致，已阻止发布。',
  desktopVersionSequence: '版本 / sequence',
  desktopPolicy: '签名发布策略',
  desktopPackageSize: 'exe 大小 / 清单大小',
  desktopSigningKey: '签名公钥 SPKI-SHA256',
  desktopValidationPassed:
    '本地大小、SHA256、公钥指纹一致，平台已用该登记公钥验证清单签名及发布策略。',
  desktopConfirm:
    '我已核对以上版本、渠道、最低版本、灰度和回滚来源，确认发布这些文件。',
  desktopLatestPassed: 'latest 自检通过，同渠道完整清单与本次发布一致。',
  desktopLatestFailed:
    'latest 自检未通过，发行可能已生效。请先核对台账和渠道状态，再处理。',
  desktopLedger: '发行台账',
  desktopDevices: '设备版本分布',
  desktopDeviceSource:
    '数据来自当前租户已登记且未吊销设备的 clientVersion 字段，反映最近登记信息。云端实例暂无桌面客户端版本上报。',
  desktopDevicePermission: '查看设备版本分布还需 iam.network.manage 权限。',
  desktopNoReports: '暂无上报',
  desktopUnreported: '未上报版本',
  desktopDeviceCount: '{count} 台设备',
  desktopFailureReports: '更新失败数暂无上报，客户端更新诊断目前保存在本机。',
  desktopManifest: '已签名清单',
  desktopArchive: '安装包',
  desktopPublish: '发布',
  desktopWithdraw: '下架',
  desktopWithdrawn: '已下架',
  desktopEmpty: '暂无版本',
  desktopFailed: '操作失败，请检查权限、签名或服务配置',
  desktopPublished: '发行已发布',
  desktopPublishedRefreshFailed:
    '发行已发布，台账刷新失败，请刷新后核对当前版本。',
  desktopWithdrawCompleted: '发行已下架',
  desktopWithdrawCompletedRefreshFailed:
    '发行已下架，台账刷新失败，请刷新后核对当前版本。',
  desktopPolicyInstructions:
    '发布策略包含在离线签名清单中。调整最低可用版本、渠道或灰度时，使用更高发行序号重新签名发布。服务端不持有私钥。',
  desktopMandatoryInstructions:
    '低于最低可用版本的客户端必须更新，不受灰度比例限制。显式回滚需在清单中指定 rollbackFrom 来源版本。',
  desktopStable: '正式',
  desktopBeta: '测试',
  desktopPolicySummary:
    '渠道：{channel} · 最低可用版本：{minimumVersion} · 灰度：{percentage}%',
  desktopRollbackSource: '显式回滚来源：{version}',
  ...securityZh,
  ...credentialsZh,
  ...devicesZh,
  ...runtimeZh,
  navRelayProviders: '模型与供应商',
  navRelayKeys: '虚拟 Key',
  navRelayUsage: '用量查询',
  ...providersZh,
  ...settingsZh,
  ...resourceZh,
  ...orgZh,
  ...quotaZh,
  ...rolesZh,
  environment: '运行环境',
  membership: '成员与权限',
  nodes: '运行节点',
  instances: '成员实例',
  members: '成员',
  refresh: '刷新',
  retry: '重试',
  details: '详情',
  close: '关闭详情',
  loading: '加载中…',
  empty: '暂无数据',
  readonly: '当前权限仅允许查看',
  forbidden: '没有此操作所需权限，请联系管理员。',
  confirm: '确认操作',
  cancel: '取消',
  save: '保存配置',
  submitting: '提交中…',
  saved: '已保存服务端配置。',
  accepted: '操作已接受；请刷新查看实际状态，接受请求不代表执行完成。',
  failed: '请求失败，请重试。',
  conflict: '数据已变化或当前状态不允许操作，请刷新详情后重试。',
  limited: '请求过于频繁，请稍后重试。',
  unavailable: '服务暂时不可用，请稍后重试。',
  retryAfter: '建议等待：{value}',
  name: '名称',
  status: '状态',
  id: 'ID',
  yes: '是',
  no: '否',
  unknown: '未知',
  previous: '上一页',
  next: '下一页',
  node: '节点',
  owner: '成员 ID',
  runtime: '运行方式',
  version: 'DSH 版本',
  desired: '期望状态',
  phase: '实际阶段',
  ready: '已就绪',
  generation: '期望 / 已观察代数',
  pendingRestart: '待重启',
  reasons: '待重启原因',
  alwaysOn: '常驻运行',
  resources: 'CPU / 内存 / 磁盘',
  start: '启动',
  stop: '停止',
  restart: '重启',
  instanceConfirm:
    '确认对「{name}」执行{action}？停止或重启会中断该成员正在使用的实例。',
  alwaysOnConfirm:
    '确认将「{name}」的常驻运行设为「{value}」？是否允许仍由服务端配额决定。',
  projection: '详情来自管理列表；刷新将重新读取当前列表页。',
  lastUpdated: '服务端更新时间',
  error: '失败原因',
  hostname: '主机名',
  platform: '系统 / 架构',
  agentVersion: '代理版本',
  drivers: '驱动',
  schedulable: '允许调度',
  labels: '标签',
  labelKey: '标签名',
  labelValue: '标签值',
  addLabel: '添加标签',
  remove: '移除',
  invalidLabels: '标签名不能为空或重复。',
  capacity: '容量（CPU 毫核 / 内存 MiB / 磁盘 GiB / 最大实例数）',
  usage: '运行实例 / 实例总数',
  lastSeen: '最近在线',
  drain: '排空节点',
  drainConfirm:
    '确认排空「{name}」？将禁止调度新实例，已有实例等待空闲迁移；本操作不代表迁移已完成。',
  nodeConfirm:
    '确认保存「{name}」的名称、标签和调度配置？标签或调度变化会影响后续实例分配。',
  search: '搜索姓名或邮箱',
  displayName: '显示名',
  email: '邮箱',
  type: '成员类型',
  roles: '角色',
  groups: '分组 ID',
  created: '创建时间',
  enable: '启用成员',
  disable: '停用成员',
  memberConfirm:
    '确认对「{name}」执行{action}？停用会影响登录与实例使用，不能停用自己或最后一位所有者。',
  memberSaved: '成员状态已更新。',
  states: {
    online: '在线',
    offline: '离线',
    draining: '排空中',
    revoked: '已吊销',
    running: '运行中',
    stopped: '已停止',
    scheduling: '调度中',
    absent: '未运行',
    preparing: '准备中',
    migrating: '迁移中',
    starting: '启动中',
    pending_restart: '待重启',
    stopping: '停止中',
    deleting: '删除中',
    failed: '失败',
    active: '正常',
    disabled: '已停用',
    offboarded: '已离职',
    member: '内部成员',
    external: '外部成员',
  },
};
const en = {
  desktopTitle: 'Client releases and updates',
  desktopIntro:
    'Inspect active channels, validate and publish offline-signed packages.',
  desktopChannels: 'Active channel releases',
  desktopNoActive: 'No active release',
  desktopExpires: 'Manifest expires',
  desktopExpired:
    'The signed manifest has expired. Clients will reject updates. Renew with a higher sequence.',
  desktopWorkflow: 'Release workflow',
  desktopBuild: 'Build',
  desktopBuildHelp:
    'Use GitHub CI or the on-site auriga-client kit to build the Windows exe and build record.',
  desktopSign: 'Sign offline',
  desktopSignHelp:
    'Inspect sources and digests with review-only, then confirm and sign. Keep the release private key on the offline signing machine.',
  desktopUpload: 'Upload and publish',
  desktopUploadHelp:
    'Select the signed manifest and exe. Check version, policy, package digest and registered key before confirming.',
  desktopUpdate: 'Client update',
  desktopUpdateHelp:
    'Clients check their channel and rollout, verify and download, then apply on restart. Failed installation smoke checks restore the previous version.',
  desktopGuide: 'Read the client release instructions in MAINTAIN.md',
  desktopVerify: 'Verify identity',
  desktopStepUpHint:
    'Verify identity before publishing or withdrawing. Reading the ledger requires no step-up. The platform checks verification freshness when you act.',
  desktopVerified:
    'Identity verified in this page. The platform still checks freshness when publishing or withdrawing.',
  desktopStepUpRequired:
    'Recent identity verification required. Verify your identity and retry.',
  desktopLoginRequired: 'Your session expired. Sign in again.',
  desktopServiceError:
    'The service or network is unavailable. Refresh and retry.',
  desktopRequestError:
    'The request did not complete. Check the returned error code.',
  desktopPolicyConflict:
    'Release sequence or channel policy changed. Refresh the ledger and validate again.',
  desktopSignatureInvalid:
    'Manifest signature or fields rejected. Check the registered key and offline signing materials.',
  desktopValidate: 'Validate files',
  desktopChecking: 'Validating…',
  desktopHashProgress: 'Local package SHA256 progress {percentage}%',
  desktopFilesInvalid:
    'Select a JSON manifest up to 32 KiB and a nonempty exe up to 4 GiB.',
  desktopManifestInvalid:
    'Invalid manifest fields. Sequence must exceed the global ledger watermark. Check the signed original.',
  desktopSizeMismatch:
    'The exe size differs from the manifest. Publication blocked.',
  desktopHashMismatch:
    'The exe SHA256 differs from the manifest. Publication blocked.',
  desktopKeyMissing:
    'The platform did not return its registered key. Check configuration and refresh.',
  desktopKeyMismatch:
    'The signing public key fingerprint differs from the registered fingerprint. Publication blocked.',
  desktopVersionSequence: 'Version / sequence',
  desktopPolicy: 'Signed release policy',
  desktopPackageSize: 'Exe size / manifest size',
  desktopSigningKey: 'Signing public key SPKI-SHA256',
  desktopValidationPassed:
    'Local size, SHA256 and key fingerprint match. The platform verified the manifest signature and release policy using that registered key.',
  desktopConfirm:
    'I checked the version, channel, minimum version, rollout and rollback source and confirm publication of these files.',
  desktopLatestPassed:
    'latest self-check passed. The complete channel manifest matches this release.',
  desktopLatestFailed:
    'latest self-check did not pass. The release may already be active. Inspect the ledger and channel before proceeding.',
  desktopLedger: 'Release ledger',
  desktopDevices: 'Device version distribution',
  desktopDeviceSource:
    'Source is clientVersion on registered, unrevoked devices in this tenant, reflecting the latest registration. Cloud instances have no desktop version reports.',
  desktopDevicePermission:
    'Device version distribution also requires iam.network.manage.',
  desktopNoReports: 'No reports yet',
  desktopUnreported: 'Version unreported',
  desktopDeviceCount: '{count} devices',
  desktopFailureReports:
    'Update failure counts have no reports yet. Client update diagnostics currently stay on each device.',
  desktopManifest: 'Signed manifest',
  desktopArchive: 'Package',
  desktopPublish: 'Publish',
  desktopWithdraw: 'Withdraw',
  desktopWithdrawn: 'Withdrawn',
  desktopEmpty: 'No releases',
  desktopFailed:
    'Operation failed. Check permission, signature or service configuration',
  desktopPublished: 'Release published',
  desktopPublishedRefreshFailed:
    'Release published. The ledger could not be refreshed. Refresh to check the current version.',
  desktopWithdrawCompleted: 'Release withdrawn',
  desktopWithdrawCompletedRefreshFailed:
    'Release withdrawn. The ledger could not be refreshed. Refresh to check the current version.',
  desktopPolicyInstructions:
    'Release policy is included in the offline signed manifest. To change the minimum version, channel or rollout, sign and publish a manifest with a higher sequence. The server does not hold the private key.',
  desktopMandatoryInstructions:
    'Clients below the minimum version must update regardless of rollout percentage. An explicit rollback must specify its source version in rollbackFrom.',
  desktopStable: 'Stable',
  desktopBeta: 'Beta',
  desktopPolicySummary:
    'Channel: {channel} · Minimum version: {minimumVersion} · Rollout: {percentage}%',
  desktopRollbackSource: 'Rollback source: {version}',
  ...securityEn,
  ...credentialsEn,
  ...devicesEn,
  ...runtimeEn,
  navRelayProviders: 'Models and providers',
  navRelayKeys: 'Virtual keys',
  navRelayUsage: 'Usage',
  ...providersEn,
  ...settingsEn,
  ...resourceEn,
  ...orgEn,
  ...quotaEn,
  ...rolesEn,
  environment: 'Runtime environment',
  membership: 'Members and permissions',
  nodes: 'Runtime nodes',
  instances: 'Member instances',
  members: 'Members',
  refresh: 'Refresh',
  retry: 'Retry',
  details: 'Details',
  close: 'Close details',
  loading: 'Loading…',
  empty: 'No data',
  readonly: 'Your permissions allow viewing only',
  forbidden:
    'You do not have permission for this operation. Contact an administrator.',
  confirm: 'Confirm operation',
  cancel: 'Cancel',
  save: 'Save configuration',
  submitting: 'Submitting…',
  saved: 'Server configuration saved.',
  accepted:
    'Operation accepted. Refresh to check the actual state; acceptance does not mean execution has completed.',
  failed: 'Request failed. Please retry.',
  conflict:
    'The data changed or the current state prevents this operation. Refresh details before retrying.',
  limited: 'Too many requests. Please retry later.',
  unavailable: 'Service unavailable. Please retry later.',
  retryAfter: 'Suggested wait: {value}',
  name: 'Name',
  status: 'Status',
  id: 'ID',
  yes: 'Yes',
  no: 'No',
  unknown: 'Unknown',
  previous: 'Previous',
  next: 'Next',
  node: 'Node',
  owner: 'Member ID',
  runtime: 'Runtime',
  version: 'DSH version',
  desired: 'Desired state',
  phase: 'Observed phase',
  ready: 'Ready',
  generation: 'Desired / observed generation',
  pendingRestart: 'Restart pending',
  reasons: 'Restart reasons',
  alwaysOn: 'Always on',
  resources: 'CPU / memory / disk',
  start: 'Start',
  stop: 'Stop',
  restart: 'Restart',
  instanceConfirm:
    'Confirm {action} for “{name}”? Stopping or restarting interrupts this member’s instance.',
  alwaysOnConfirm:
    'Set always-on for “{name}” to “{value}”? Server quotas still apply.',
  projection:
    'Details come from the administrative list. Refresh reloads the current list page.',
  lastUpdated: 'Server updated at',
  error: 'Failure reason',
  hostname: 'Hostname',
  platform: 'OS / architecture',
  agentVersion: 'Agent version',
  drivers: 'Drivers',
  schedulable: 'Schedulable',
  labels: 'Labels',
  labelKey: 'Label key',
  labelValue: 'Label value',
  addLabel: 'Add label',
  remove: 'Remove',
  invalidLabels: 'Label keys must be nonempty and unique.',
  capacity: 'Capacity (CPU millicores / memory MiB / disk GiB / max instances)',
  usage: 'Running / total instances',
  lastSeen: 'Last seen',
  drain: 'Drain node',
  drainConfirm:
    'Drain “{name}”? New scheduling will stop; existing instances await idle migration. This does not mean migration is complete.',
  nodeConfirm:
    'Save the name, labels and scheduling configuration for “{name}”? Label and scheduling changes affect subsequent instance placement.',
  search: 'Search name or email',
  displayName: 'Display name',
  email: 'Email',
  type: 'Member type',
  roles: 'Roles',
  groups: 'Group IDs',
  created: 'Created at',
  enable: 'Enable member',
  disable: 'Disable member',
  memberConfirm:
    'Confirm {action} for “{name}”? Disabling affects sign-in and instances. You cannot disable yourself or the last owner.',
  memberSaved: 'Member status updated.',
  states: {
    online: 'Online',
    offline: 'Offline',
    draining: 'Draining',
    revoked: 'Revoked',
    running: 'Running',
    stopped: 'Stopped',
    scheduling: 'Scheduling',
    absent: 'Absent',
    preparing: 'Preparing',
    migrating: 'Migrating',
    starting: 'Starting',
    pending_restart: 'Restart pending',
    stopping: 'Stopping',
    deleting: 'Deleting',
    failed: 'Failed',
    active: 'Active',
    disabled: 'Disabled',
    offboarded: 'Offboarded',
    member: 'Internal',
    external: 'External',
  },
};

export function useAdminI18n() {
  const i18n = useI18n({ useScope: 'local', messages: { 'zh-CN': zh, en } });
  const state = (value?: string) =>
    !value
      ? i18n.t('unknown')
      : Object.hasOwn(zh.states, value)
        ? i18n.t(`states.${value}`)
        : value;
  return { ...i18n, state };
}
