export const runtimeZh = {
  opsActualImages: '实际运行镜像',
  opsImageId: '实际镜像摘要',
  opsSetup: '如未启用，请由运维配置部署 worker。',
  opsTarget: '批准的升级目标',
  opsRegister: '登记升级',
  opsUpgradeConfirm: '确认登记升级？部署流程会重建相关服务，可能短暂中断。',
  opsUpgradeScope:
    '目标来自运维批准目录。登记只进入队列；以部署回执及实际镜像为准。下表显示最近 50 条。',
  opsReceipts: '部署回执',
  opsCreated: '登记时间',
  opsFinished: '完成时间',
  opsBackupPlan: 'PG 备份计划',
  opsPlanEnabled: '启用定时备份',
  opsPlanDisabled: '关闭定时备份',
  opsInterval: '间隔（小时）',
  opsNext: '下次计划',
  opsBytes: '字节数',
  opsBackupNow: '立即备份',
  opsPlanInvalid: '备份间隔须为 1–720 小时的整数。',
  opsPlanConfirm: '确认修改 PG 备份计划？',
  opsBackupConfirm: '确认触发 PG 备份？备份将消耗数据库和磁盘资源。',
  opsDownloadConfirm:
    '确认下载数据库归档？需近期验证，授权仅限本会话一次使用、60 秒有效。',
  opsUpgrades: '版本与升级',
  opsBackups: '备份与恢复',
  opsLifecycle: '生命周期',
  runtimeCatalog: '可用运行时',
  runtimeDefault: '默认版本',
  runtimeImages: '制品 / 镜像',
  runtimeInstances: '使用实例数',
  runtimeScope:
    '目录来自已部署的签名运行时制品。实例版本是期望规格，实际应用状态以代数和重启状态为准。',
  runtimeMissing:
    '平台服务镜像清单与升级登记尚未接入。请通过现有部署流程升级；此页不会拉取或切换镜像。',
  backupMissing:
    'PG 备份计划、执行器与结果存储尚未接入，暂不能手动触发或下载备份。',
  backupScope: '备份范围',
  backupScopeText:
    '数据库逻辑备份不包含成员卷、资源包、总部工作区和部署密钥；这些数据须按存储方案分别备份。',
  backupRestore: '恢复前检查',
  backupRestoreText:
    '由运维人员确认目标库与维护窗口，先备份当前数据。在隔离的空库中用 pg_restore 验证归档、角色和扩展，再按部署流程恢复。恢复后检查迁移版本、登录、资源与实例状态。此页不执行恢复。',
  backupDownload: '下载与保存',
  backupDownloadText:
    '从部署环境的受控备份存储获取归档和校验和，校验后离线保存。未取得备份清单与恢复验证记录前，不应视为可恢复。',
  lifecycleScope:
    '策略对云端实例生效。活动上报缺失或过期时不授权回收；常驻实例仍受账号状态和配额约束。',
  lifecycleIdle: '闲置回收（分钟）',
  lifecycleStarts: '同时启动上限',
  lifecycleInvalid: '闲置时长至少 5 分钟，启动上限至少 1；请输入 32 位正整数。',
  lifecycleConfirm:
    '确认修改云端策略？闲置分钟：{idle}；同时启动上限：{starts}。降低时长可能在下一轮判定停止空闲实例。',
  lifecycleLinks: '配额与例外',
  lifecycleLinksText:
    '默认、分组与个人配额在配额页统一配置；常驻例外在实例详情设置，并受有效配额许可约束。',
  lifecycleDrain: '排空策略',
  lifecycleDrainText:
    '在节点页发起排空：停止调度并迁移实例；请先确认其他节点有可用容量。排空会影响正在运行的实例。',
};
export const runtimeEn: Record<keyof typeof runtimeZh, string> = {
  opsActualImages: 'Running images',
  opsImageId: 'Actual image digest',
  opsSetup: 'If disabled, ask operations to configure the deployment worker.',
  opsTarget: 'Approved upgrade target',
  opsRegister: 'Register upgrade',
  opsUpgradeConfirm:
    'Register this upgrade? Deployment may recreate services and briefly interrupt availability.',
  opsUpgradeScope:
    'Targets come from the approved deployment catalog. Registration only queues work; verify the receipt and actual images. The latest 50 receipts are shown.',
  opsReceipts: 'Deployment receipts',
  opsCreated: 'Requested at',
  opsFinished: 'Completed at',
  opsBackupPlan: 'PG backup schedule',
  opsPlanEnabled: 'Enable scheduled backups',
  opsPlanDisabled: 'Disable scheduled backups',
  opsInterval: 'Interval (hours)',
  opsNext: 'Next scheduled',
  opsBytes: 'Bytes',
  opsBackupNow: 'Back up now',
  opsPlanInvalid: 'Backup interval must be an integer from 1 to 720 hours.',
  opsPlanConfirm: 'Change the PG backup schedule?',
  opsBackupConfirm:
    'Trigger a PG backup? This uses database and disk resources.',
  opsDownloadConfirm:
    'Download the database archive? Recent verification is required. The grant is single-use, bound to this session, and expires in 60 seconds.',

  opsUpgrades: 'Versions and upgrades',
  opsBackups: 'Backup and restore',
  opsLifecycle: 'Lifecycle',
  runtimeCatalog: 'Available runtimes',
  runtimeDefault: 'Default version',
  runtimeImages: 'Artifacts / images',
  runtimeInstances: 'Instances using version',
  runtimeScope:
    'The catalog contains deployed signed releases. Instance versions are desired specifications; generations and restart status show application progress.',
  runtimeMissing:
    'Platform image inventory and upgrade registration are not connected. Use the existing deployment workflow; this page does not pull or switch images.',
  backupMissing:
    'PG backup schedules, execution and result storage are not connected. Manual backup and download are unavailable.',
  backupScope: 'Backup scope',
  backupScopeText:
    'Logical database backups exclude member volumes, resource archives, headquarters workspaces and deployment keys. Back them up separately using the storage procedure.',
  backupRestore: 'Before restoring',
  backupRestoreText:
    'Operations staff must confirm the target database and maintenance window and back up current data. Validate the archive, roles and extensions with pg_restore in an isolated empty database, then follow the deployment restore procedure. Verify migrations, login, resources and instances afterward. This page does not execute restores.',
  backupDownload: 'Download and retention',
  backupDownloadText:
    'Obtain the archive and checksum from controlled deployment backup storage, verify and retain offline. A backup inventory and successful restore validation are required to establish recoverability.',
  lifecycleScope:
    'Policy applies to cloud instances. Missing or stale activity cannot authorize reclamation. Always-on instances remain subject to account status and quotas.',
  lifecycleIdle: 'Idle timeout (minutes)',
  lifecycleStarts: 'Concurrent starts',
  lifecycleInvalid:
    'Idle timeout must be at least 5 minutes and concurrent starts at least 1. Enter positive 32-bit integers.',
  lifecycleConfirm:
    'Change cloud policy? Idle minutes: {idle}; concurrent starts: {starts}. Reducing the timeout may stop idle instances on the next evaluation.',
  lifecycleLinks: 'Quotas and exceptions',
  lifecycleLinksText:
    'Configure default, group and member quotas on the quotas page. Configure always-on exceptions in instance details, subject to effective quota permission.',
  lifecycleDrain: 'Drain policy',
  lifecycleDrainText:
    'Drain from the nodes page to stop scheduling and migrate instances. Confirm spare capacity on other nodes first. Draining affects running instances.',
};
