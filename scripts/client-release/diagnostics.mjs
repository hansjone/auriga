import { dirname, isAbsolute, relative, resolve, sep } from 'node:path';

// Sanitize before truncation: a truncated PEM block or credential must never
// become visible merely because its closing delimiter fell outside the limit.
export function safeDiagnostic(
  value,
  {
    privatePaths = [],
    env = process.env,
    limit = 1800,
    visiblePaths = [],
    hidePaths = true,
  } = {},
) {
  let text = String(value ?? '');
  text = text.replace(
    /-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?(?:-----END [^-]*PRIVATE KEY-----|$)/g,
    '[私钥已隐藏]',
  );
  const secrets = [];
  for (const [name, secret] of Object.entries(env)) {
    if (
      secret &&
      /(?:auth|cookie|csrf|token|password|secret|private.*key)/i.test(name)
    )
      secrets.push(secret);
  }
  try {
    const auth = JSON.parse(env.AURIGA_CLIENT_ADMIN_AUTH ?? 'null');
    for (const value of [auth?.cookie, auth?.csrf])
      if (typeof value === 'string' && value) secrets.push(value);
  } catch {
    /* Invalid auth is diagnosed separately without logging its input. */
  }
  for (const path of privatePaths.filter(Boolean)) {
    for (const value of [path, resolve(path), dirname(resolve(path))]) {
      secrets.push(
        value,
        value.replaceAll('\\', '/'),
        value.replaceAll('\\', '\\\\'),
      );
    }
  }
  for (const secret of secrets.sort((a, b) => b.length - a.length))
    text = text.split(secret).join('[已隐藏]');
  text = text.replace(/\b(?:https?|file):\/\/[^\s<>"']+/gi, '[地址已隐藏]');
  if (hidePaths)
    text = text.replace(
      /(['"])((?:[A-Za-z]:[\\/]|\\\\|\/)[^\r\n]*?)\1|(?:[A-Za-z]:[\\/]|\\\\)[^\r\n<>"'|;]*|(?<![\w:/])\/(?:[^\s/]+\/)*[^\s,;:)]+/g,
      (match, quote, path) =>
        visiblePaths.includes(path ?? match)
          ? match
          : `${quote ?? ''}[路径已隐藏]${quote ?? ''}`,
    );
  text = text
    .replace(
      /\b(?:cookie|authorization|csrf|token|password|secret|key)\s*[:=]\s*[^\r\n;,]+/gi,
      '[凭据已隐藏]',
    )
    .replace(
      /\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g,
      '[令牌已隐藏]',
    )
    .replace(/\p{Cc}/gu, ' ')
    .trim();
  return text.length > limit ? text.slice(0, limit) + '…[已截断]' : text;
}

function within(path, root) {
  const rel = relative(root, path);
  return rel !== '..' && !rel.startsWith(`..${sep}`) && !isAbsolute(rel);
}

// Only structured filesystem paths get an exception. Error text can include
// subprocess output, command arguments or remote responses.
export function buildErrorPaths(error, roots, privateDir, options) {
  return [...new Set([error.path, error.dest])].filter((path) => {
    if (typeof path !== 'string' || !isAbsolute(path)) return false;
    if (privateDir && within(path, privateDir)) return false;
    if (
      /[\\/](?:\.env(?:\.[^\\/]+)?|\.npmrc|\.ssh|\.aws|credentials?|secrets?|private|id_(?:rsa|dsa|ecdsa|ed25519)(?:\.pub)?)(?:[\\/]|$)/i.test(
        path,
      ) ||
      /\.(?:pem|key|pfx|p12)$/i.test(path)
    )
      return false;
    return (
      roots.some((root) => within(path, root)) &&
      safeDiagnostic(path, { ...options, hidePaths: false }) === path
    );
  });
}

export function privateKeyFailure(result, path, env = process.env) {
  const options = { privatePaths: [path], env };
  const kind = result.error
    ? `spawn=${safeDiagnostic(result.error.code ?? result.error.name, options)}`
    : result.signal
      ? `signal=${safeDiagnostic(result.signal, options)}`
      : `exit=${result.status}`;
  const streams = [
    ['error', result.error?.message],
    ['stdout', result.stdout],
    ['stderr', result.stderr],
  ]
    .filter(([, value]) => value)
    .map(([name, value]) => `${name}=${safeDiagnostic(value, options)}`)
    .join('；');
  const policy =
    /AuthorizationManager|UnauthorizedAccess|PSSecurityException|execution polic|running scripts is disabled|执行策略|禁止运行脚本/i.test(
      [result.error?.message, result.stdout, result.stderr].join('\n'),
    );
  const next = policy
    ? '检测到 PowerShell 授权或执行策略拒绝；请管理员核对 Get-ExecutionPolicy -List 与脚本信任，按组织策略处理后重试；工具不会绕过策略'
    : 'ACL 应满足 Owner=当前用户、关闭继承、仅一条 Allow FullControl；确认后可用 scripts/resource-keys/protect-private-path.ps1 保护空目录或空文件，再重试';
  return new Error(
    `桌面私钥权限校验或保护失败（${kind}）${streams ? '；' + streams : ''}。下一步：${next}`,
  );
}
