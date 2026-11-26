const hints: Record<string, [string, string]> = {
  guardian_missing: [
    '当前宿主尚无可信 guardian，共享安装暂不可用。请通过 Auriga 桌面启动器打开 DSH 后刷新。',
    'Shared installation is unavailable until a trusted guardian is present. Open DSH through the Auriga desktop launcher, then refresh.',
  ],
  trust_roots_missing: [
    '签名信任根缺失或不可读。请更新 Auriga 客户端，或请管理员检查资源签名配置后刷新。',
    'Signature trust roots are missing or unreadable. Update Auriga, or ask an administrator to check resource signing configuration.',
  ],
  trust_roots_invalid: [
    '签名信任根配置无效。请联系管理员检查文件格式、kid 是否重复及公钥内容，修复后刷新。',
    'Signature trust root configuration is invalid. Ask an administrator to check the file format, duplicate key IDs and public keys, then refresh after correcting it.',
  ],
  signature_invalid: [
    '资源签名、摘要、撤销或有效期校验失败。请检查本机时间，并联系管理员重新发布或续签此版本。',
    'Signature, digest, revocation or expiry checks failed. Check your clock and ask an administrator to publish or renew this version.',
  ],
  platform_unreachable: [
    '平台不可达。请检查平台连接后刷新；安装结果以刷新后的本机状态为准。',
    'Cannot reach the platform. Check the connection, then refresh to confirm the local installation state.',
  ],
  storage_unavailable: [
    '本机资源目录不可用。请确认目录可写且使用本机真实路径，然后刷新。',
    'Local resource storage is unavailable. Check write access and use a real local filesystem path, then refresh.',
  ],
  host_busy: [
    '客户端正在同步共享资源，请稍后刷新重试。',
    'The client is synchronizing shared resources. Wait a moment, then refresh and retry.',
  ],
  state_conflict: [
    '资源版本或安装状态已变化。请刷新并重新选择要操作的精确版本。',
    'The resource version or installation state changed. Refresh and select the exact version again.',
  ],
  runtime_unavailable: [
    '平台连接尚未就绪。请前往概览完成登录或重新连接后刷新。',
    'The platform connection is not ready. Sign in or reconnect in Overview, then refresh.',
  ],
  installer_unavailable: [
    '本机安装组件尚未就绪。请更新客户端并重新打开 Auriga 后刷新。',
    'The local installer is not ready. Update the client and reopen Auriga, then refresh.',
  ],
  unauthenticated: [
    '登录已失效，请重新登录后刷新。',
    'Your sign-in expired. Sign in again, then refresh.',
  ],
  forbidden: [
    '当前账号或设备没有资源访问权限。请刷新登录凭据后重试，仍失败请联系管理员检查权限和设备状态。',
    'Your account or device is not authorized to access resources. Refresh your sign-in credentials and retry. If access is still denied, contact an administrator to check permissions and device status.',
  ],
  validation_failed: [
    '资源数据或请求格式无效。请刷新，仍失败请联系管理员检查客户端与平台版本。',
    'The resource data or request is invalid. Refresh; if this persists, ask an administrator to check client and platform versions.',
  ],
  not_found: [
    '此资源或版本已不可用，请刷新资源市场。',
    'This resource or version is no longer available. Refresh the market.',
  ],
  rate_limited: [
    '请求过于频繁，请稍后手动刷新。',
    'Too many requests. Wait, then refresh manually.',
  ],
  apply_failed: [
    '本机应用资源失败。请刷新确认安装状态，并检查客户端诊断日志。',
    'The resource could not be applied locally. Refresh its state and check client diagnostic logs.',
  ],
  internal_error: [
    '操作未完成。请刷新确认状态；若仍失败，请将关联编号提供给管理员查询客户端日志。',
    'The operation did not complete. Refresh its state; if this persists, give the trace ID to an administrator to check client logs.',
  ],
};
export class ResourceDiagnosticError extends Error {
  readonly reason: string;
  readonly trace?: string;
  constructor(reason: string, trace?: string) {
    super(reason);
    this.reason = Object.hasOwn(hints, reason) ? reason : 'internal_error';
    this.trace =
      typeof trace === 'string' && /^[a-f0-9]{32}$/.test(trace)
        ? trace
        : undefined;
  }
}
export async function resourceResponseError(response: Response) {
  const body = await response.json().catch(() => ({}));
  const byStatus: Record<number, string> = {
    400: 'validation_failed',
    401: 'unauthenticated',
    403: 'forbidden',
    404: 'not_found',
    409: 'state_conflict',
    412: 'state_conflict',
    422: 'signature_invalid',
    429: 'rate_limited',
    503: 'platform_unreachable',
  };
  const byCode: Record<string, string> = {
    'resource.storage_unavailable': 'storage_unavailable',
    'resource.signature_invalid': 'signature_invalid',
    'resource.digest_mismatch': 'signature_invalid',
    'resource.apply_failed': 'apply_failed',
  };
  return new ResourceDiagnosticError(
    Object.hasOwn(hints, body?.reason)
      ? body.reason
      : ((Object.hasOwn(byCode, body?.code) ? byCode[body.code] : undefined) ??
          byStatus[response.status] ??
          'internal_error'),
    /^[a-f0-9]{32}$/.test(body?.trace_id) ? body.trace_id : undefined,
  );
}
export function resourceErrorMessage(error: unknown, english: boolean) {
  const reason =
    error instanceof ResourceDiagnosticError
      ? error.reason
      : 'platform_unreachable';
  const hint = (hints[reason] ?? hints.internal_error)[english ? 1 : 0];
  return (
    hint +
    (error instanceof ResourceDiagnosticError && error.trace
      ? (english ? ' Trace ID: ' : ' 关联编号：') + error.trace
      : '')
  );
}
