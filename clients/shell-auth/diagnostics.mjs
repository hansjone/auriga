import { createHash, randomUUID } from 'node:crypto';
import {
  existsSync,
  readdirSync,
  lstatSync,
  readFileSync,
  appendFileSync,
  writeFileSync,
  mkdirSync,
  unlinkSync,
  rmdirSync,
  linkSync,
} from 'node:fs';
import { join, resolve } from 'node:path';
import { safePath, privateDirectory } from './vault.mjs';
import { requireValid } from './schema.mjs';

export const REDACTOR_VERSION = 'shell-auth-1';
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const allowed = {
  service: ['shell-auth', 'dsh-plugin', 'iam', 'edge'],
  level: ['INFO', 'WARN', 'ERROR', 'DEBUG'],
  phase: ['start', 'end'],
  stage: [
    'shell_identity',
    'device_authorization',
    'device_poll',
    'session_revoke',
    'session_revalidate',
    'token_verify',
    'department_lookup',
    'sso_cookie_check',
    'challenge_start',
    'scan_status',
    'token_acquire',
    'diagnostic_export',
  ],
  result: [
    'success',
    'waiting',
    'scanned',
    'failure',
    'expired',
    'cancelled',
    'retry_pending',
    'retry_exhausted',
    'requested',
    'committed',
    'propagated',
  ],
  purpose: ['login_info', 'login_sso', 'revalidate'],
  chain: ['login', 'revalidation', 'revocation'],
  state: [
    'signed_out',
    'authenticating',
    'authenticated',
    'unreachable',
    'reauth_required',
    'revoked',
  ],
  reason: [
    'expired',
    'offline',
    'source_disabled',
    'account_changed',
    'shell_restarted',
    'revalidation_rejected',
    'revalidation_unreachable',
    'user_logout',
    'admin_disabled',
    'member_disabled',
  ],
  strategy: ['token-verify', 'department'],
  cookieFamily: ['portal', 'uds', 'none'],
  udsMessage: ['success', 'waiting', 'rejected', 'unavailable'],
};
const ids = new Set([
  'correlationId',
  'parentCorrelationId',
  'eventId',
  'transactionId',
  'requestId',
  'tenantId',
  'providerId',
  'trace_id',
  'spanId',
]);
const booleans = new Set([
  'departmentPresent',
  'tokenMissing',
  'tokenReplaced',
  'cookiePresent',
  'eligible',
  'suppressed',
  'identityMatched',
]);
const numbers = new Set([
  'durationMs',
  'revision',
  'attempt',
  'maxRetries',
  'intervalSeconds',
  'dueLagMs',
  'responseBytes',
  'credentialVersion',
  'httpStatus',
]);
const dates = new Set([
  'timestamp',
  'nextRevalidateAt',
  'retryAt',
  'retryDeadline',
  'updatedAt',
]);
/** Deliberate positive projection; unknown keys AND their values are omitted. */
export function redact(input) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) return {};
  const result = {};
  for (const [key, value] of Object.entries(input)) {
    if (
      ids.has(key) &&
      typeof value === 'string' &&
      (UUID.test(value) ||
        (['trace_id', 'spanId'].includes(key) &&
          /^[a-f0-9]{16,32}$/.test(value)))
    )
      result[key] = value;
    else if (booleans.has(key) && typeof value === 'boolean')
      result[key] = value;
    else if (numbers.has(key) && Number.isSafeInteger(value) && value >= 0)
      result[key] = value;
    else if (
      dates.has(key) &&
      typeof value === 'string' &&
      /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z$/.test(value) &&
      Number.isFinite(Date.parse(value))
    )
      result[key] = value;
    else if (allowed[key]?.includes(value)) result[key] = value;
    else if (
      ['tokenHashPrefix', 'oldTokenHashPrefix', 'newTokenHashPrefix'].includes(
        key,
      ) &&
      (value === null ||
        (typeof value === 'string' && /^[a-f0-9]{12}$/.test(value)))
    )
      result[key] = value;
    else if (
      key === 'empNo' &&
      typeof value === 'string' &&
      /^[\p{L}\p{N}_.-]{1,128}$/u.test(value)
    )
      result[key] = value;
    else if (key === 'udsMessage') result[key] = '[REDACTED]';
  }
  if (Object.hasOwn(input, 'department'))
    result.departmentPresent =
      typeof input.department === 'string' &&
      input.department.trim().length > 0;
  return result;
}
export function createLog(directory, helper, identity = () => ({})) {
  privateDirectory(directory, helper);
  const file = join(directory, 'auriga-shell-auth.jsonl');
  return (event) => {
    safePath(file);
    // Rotation stays within the trusted directory, never reads arbitrary paths.
    if (existsSync(file) && lstatSync(file).size > 8388608) {
      const old = join(directory, 'auriga-shell-auth.previous.jsonl');
      safePath(old);
      writeFileSync(old, readFileSync(file), { mode: 0o600 });
      writeFileSync(file, '', { mode: 0o600 });
    }
    const safe = redact({
      ...event,
      ...identity(),
      service: 'shell-auth',
      timestamp: new Date().toISOString(),
      eventId: randomUUID(),
      level: event.result === 'failure' ? 'WARN' : 'INFO',
    });
    appendFileSync(file, JSON.stringify(safe) + '\n', { mode: 0o600 });
  };
}
function crc32(buffer) {
  let crc = 0xffffffff;
  for (const byte of buffer) {
    crc ^= byte;
    for (let i = 0; i < 8; i++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
  }
  return (crc ^ 0xffffffff) >>> 0;
}
/** Store-only ZIP: no external development tooling in the installed command. */
export function zip(files) {
  const records = [],
    directory = [];
  let offset = 0;
  for (const [name, buffer] of Object.entries(files)) {
    const filename = Buffer.from(name),
      crc = crc32(buffer);
    const header = Buffer.alloc(30);
    header.writeUInt32LE(0x04034b50);
    header.writeUInt16LE(20, 4);
    header.writeUInt32LE(crc, 14);
    header.writeUInt32LE(buffer.length, 18);
    header.writeUInt32LE(buffer.length, 22);
    header.writeUInt16LE(filename.length, 26);
    records.push(header, filename, buffer);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50);
    central.writeUInt16LE(20, 4);
    central.writeUInt16LE(20, 6);
    central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(buffer.length, 20);
    central.writeUInt32LE(buffer.length, 24);
    central.writeUInt16LE(filename.length, 28);
    central.writeUInt32LE(offset, 42);
    directory.push(central, filename);
    offset += header.length + filename.length + buffer.length;
  }
  const size = directory.reduce((n, b) => n + b.length, 0),
    end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50);
  end.writeUInt16LE(Object.keys(files).length, 8);
  end.writeUInt16LE(Object.keys(files).length, 10);
  end.writeUInt32LE(size, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...records, ...directory, end]);
}
export async function exportDiagnostics({
  root,
  output,
  helper,
  from,
  to,
  tenantId,
  providerId,
}) {
  root = safePath(resolve(root));
  output = safePath(resolve(output));
  if (!existsSync(join(root, '.auriga-desktop-install')))
    throw new Error('owned installation required');
  safePath(join(root, 'install.json'));
  const install = JSON.parse(readFileSync(join(root, 'install.json'), 'utf8'));
  const dataHome = safePath(install.dataHome);
  const end = to ? Date.parse(to) : Date.now(),
    start = from ? Date.parse(from) : end - 7200000;
  if (
    !Number.isFinite(start) ||
    !Number.isFinite(end) ||
    end <= start ||
    end - start > 86400000 ||
    end > Date.now() + 60000
  )
    throw new Error('invalid diagnostic time window');
  if (
    (tenantId && !UUID.test(tenantId)) ||
    (providerId && !UUID.test(providerId)) ||
    Boolean(tenantId) !== Boolean(providerId)
  )
    throw new Error('tenant/provider pair required');
  mkdirSync(output, { recursive: true });
  safePath(output);
  const stage = join(output, '.uac-diagnostics-' + randomUUID());
  privateDirectory(stage, helper);
  const tempZip = join(stage, 'bundle.zip'),
    auditId = randomUUID();
  const missing = ['iam', 'audit', 'ops', 'loki'];
  const logs = [];
  let bytes = 0,
    dropped = 0,
    truncated = false;
  try {
    const sources = /** @type {[string, string, RegExp][]} */ ([
      [
        'desktop',
        join(dataHome, 'auriga', 'logs'),
        /^auriga-shell-auth(?:\.previous)?\.jsonl$/,
      ],
      [
        'plugin',
        join(dataHome, 'auriga', 'logs'),
        /^auriga-dsh-[\w-]+\.jsonl$/,
      ],
    ]);
    for (const [source, directory, pattern] of sources) {
      safePath(directory);
      let found = false;
      if (existsSync(directory))
        for (const name of readdirSync(directory).filter((name) =>
          pattern.test(name),
        )) {
          const file = safePath(join(directory, name)),
            info = lstatSync(file);
          if (!info.isFile()) throw new Error('unsafe log source');
          found = true;
          if (info.size > 16777216 || bytes + info.size > 45000000) {
            truncated = true;
            continue;
          }
          bytes += info.size;
          for (const line of readFileSync(file, 'utf8').split('\n')) {
            if (!line) continue;
            if (Buffer.byteLength(line) > 65536) {
              dropped++;
              continue;
            }
            let record;
            try {
              record = JSON.parse(line);
            } catch {
              dropped++;
              continue;
            }
            // No installation-wide expansion: export only one explicitly chosen tenant/provider.
            if (
              !tenantId ||
              record.tenantId !== tenantId ||
              record.providerId !== providerId
            )
              continue;
            const timestamp = Date.parse(record.timestamp);
            if (
              !Number.isFinite(timestamp) ||
              timestamp < start ||
              timestamp > end
            )
              continue;
            logs.push(redact(record));
          }
        }
      if (!found || !tenantId) missing.push(source);
    }
    const files = {
      'uac-steps.jsonl': Buffer.from(
        logs.map((l) => JSON.stringify(l)).join('\n') +
          (logs.length ? '\n' : ''),
      ),
      'audit.jsonl': Buffer.alloc(0),
      'ops-alerts.json': Buffer.from('[]\n'),
      'sanitized-config.json': Buffer.from(
        JSON.stringify({ brokerVersion: '1', localOnly: true }) + '\n',
      ),
    };
    const manifest = {
      version: 1,
      from: new Date(start).toISOString(),
      to: new Date(end).toISOString(),
      redactorVersion: REDACTOR_VERSION,
      exportAuditId: auditId,
      partial: true,
      missingSources: missing,
      installationVersion: String(install.current).slice(0, 128),
      componentVersions: { broker: '1' },
      sources: ['iam', 'audit', 'ops', 'loki', 'desktop', 'plugin'].map(
        (source) => ({
          source,
          available: !missing.includes(source),
          reason: missing.includes(source)
            ? 'missing'
            : truncated
              ? 'truncated'
              : dropped
                ? 'invalid_log'
                : 'available',
        }),
      ),
      files: Object.entries(files).map(([name, value]) => ({
        name,
        sha256: createHash('sha256').update(value).digest('hex'),
        lines: value.toString().split('\n').filter(Boolean).length,
        bytes: value.length,
        truncated,
      })),
    };
    requireValid('UacDiagnosticManifest', manifest);
    files['manifest.json'] = Buffer.from(JSON.stringify(manifest, null, 2));
    // All bytes are projected before entering the private staging directory.
    const archive = zip(files);
    if (archive.length > 52428800) throw new Error('diagnostic size limit');
    writeFileSync(tempZip, archive, { flag: 'wx', mode: 0o600 });
    const destination = join(
      output,
      `uac-diagnostics-${new Date().toISOString().replace(/[:.]/g, '-')}-${randomUUID()}.zip`,
    );
    safePath(destination);
    linkSync(tempZip, destination); // atomic publish, never overwrite
    return {
      file: destination,
      partial: true,
      missingSources: missing,
      dropped,
      truncated,
      exportAuditId: auditId,
    };
  } finally {
    if (existsSync(tempZip)) unlinkSync(tempZip);
    rmdirSync(stage);
  }
}
