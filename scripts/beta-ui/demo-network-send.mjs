import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { Buffer } from 'node:buffer';
import { fileURLToPath } from 'node:url';
import { assertStatePath, main as launcher } from './plugin-dsh.mjs';
import { regularFile, readConnect } from './demo-network-send-config.mjs';
import { canonicalize } from '../../clients/dsh-plugin/src/network/trust/jcs.js';
export { validateConnect, readConnect } from './demo-network-send-config.mjs';

const script = 'scripts/beta-ui/demo-network-send.mjs';
const inside = (a, b) => {
  const relative = path.relative(a, b);
  return (
    relative === '' ||
    (!relative.startsWith('..' + path.sep) &&
      relative !== '..' &&
      !path.isAbsolute(relative))
  );
};
const digest = (bytes) =>
  'sha256:' + createHash('sha256').update(bytes).digest('hex');

export function planPair({ connectFile, connect, a, b }) {
  a = assertStatePath(a);
  b = assertStatePath(b);
  if (inside(a, b) || inside(b, a))
    throw new Error('A/B StateDir 必须互不重叠');
  for (const state of [a, b]) {
    if (fs.existsSync(state))
      throw new Error('StateDir 已存在；拒绝覆盖 profile 或账本');
    if (!fs.existsSync(path.dirname(state)))
      throw new Error('请先创建 StateDir 父目录');
  }
  return {
    status: 'prepared-plan',
    readiness: 'unverified',
    platformOrigin: connect.platformOrigin,
    realm: connect.network.realm,
    commands: [a, b].map((state) => [
      process.execPath,
      script,
      'launch',
      '--state',
      state,
      '--connect-config',
      connectFile,
    ]),
    isolation: [a, b].map((state) => ({
      state,
      profile: path.join(state, 'dsh/profiles/web'),
      home: path.join(state, 'home'),
      cwd: path.join(state, 'workspace'),
    })),
    next: '由主控串行启动 A、B，分别正常登录；部署、READY 和收发仍待真实验收',
  };
}

// This is a local disk observation, never a validation of server signatures or
// a count of native prompts. Keep raw credentials, bodies and keysets private.
export function summarizeLedger(value) {
  if (
    !value ||
    ![2, 3].includes(value.version) ||
    !Number.isSafeInteger(value.revision)
  )
    throw new Error('不是可识别的宿主账本快照');
  const d = value.durable;
  return {
    status: d ? 'observed' : 'messaging-unconfigured',
    revision: value.revision,
    nativeInputCount: null,
    nativeInputCountReason:
      '需人工核原生会话历史；账本状态不能证明 prompt 次数',
    resumeCursor: d?.resumeCursor ?? null,
    submissions: Object.values(d?.submissions ?? {}).map((saved) => {
      const result = saved.result === null ? null : JSON.parse(saved.result);
      return {
        key: saved.request.idempotency?.key ?? null,
        producerEpoch: saved.request.idempotency?.producer_epoch ?? null,
        requestDigest: digest(canonicalize(saved.request)),
        resultDigest: saved.result === null ? null : digest(saved.result),
        status: result?.status ?? 'outcome_unknown',
        messageId: result?.message_id ?? null,
      };
    }),
    inbound: Object.values(d?.inbound ?? {}).map((r) => ({
      recipientId: r.recipientId,
      messageId: r.delivery?.message?.message_id ?? null,
      sessionId: r.hostSessionId,
      stage: r.stage,
      hostRequestId:
        r.receipt?.results?.find((v) => v.recipient_id === r.recipientId)
          ?.host_request_id ?? null,
      receiptDigest: r.receipt ? digest(canonicalize(r.receipt)) : null,
    })),
  };
}

function argumentsFor(args) {
  const [action, ...rest] = args;
  const allowed = {
    preflight: ['--connect-config'],
    plan: ['--connect-config', '--state-a', '--state-b'],
    launch: ['--connect-config', '--state'],
    status: ['--state'],
    stop: ['--state'],
    evidence: ['--state'],
  }[action];
  if (!allowed || rest.length % 2)
    throw new Error(
      '用法：demo-network-send.mjs preflight|plan|launch|status|stop|evidence（见 beta-network-send.md）',
    );
  const options = {};
  for (let i = 0; i < rest.length; i += 2) {
    if (
      !allowed.includes(rest[i]) ||
      Object.hasOwn(options, rest[i]) ||
      !rest[i + 1] ||
      rest[i + 1].startsWith('--')
    )
      throw new Error('参数未知、重复或缺少值');
    options[rest[i]] = rest[i + 1];
  }
  for (const key of allowed.filter((k) => k !== '--connect-config'))
    if (!options[key]) throw new Error('缺少必要 StateDir 参数');
  return { action, options };
}

export async function main(args) {
  const { action, options: o } = argumentsFor(args);
  const out = (value) => console.log(JSON.stringify(value, null, 2));
  if (['preflight', 'plan', 'launch'].includes(action)) {
    if (!o['--connect-config']) {
      out({
        status: 'unconfigured',
        readiness: 'blocked',
        reason:
          '未供应所选部署的受信公开 connect 配置；默认 Beta 不具完整 N1/根签/可信 HTTPS',
      });
      return 2;
    }
    const connect = readConnect(o['--connect-config']);
    if (action === 'preflight') {
      out({
        status: 'configuration-valid',
        readiness: 'unverified',
        platformOrigin: connect.platformOrigin,
        realm: connect.network.realm,
        wssUrl: connect.network.wssUrl,
        roots: connect.network.roots.map((r) => ({
          kid: r.kid,
          spkiSha256: digest(Buffer.from(r.public_key_spki, 'base64url')),
        })),
        checks:
          '仅本地格式与静态 pin；未联网、未登录、未核部署/根签公告/READY/收发',
      });
      return 0;
    }
    if (action === 'plan') {
      out(
        planPair({
          connectFile: o['--connect-config'],
          connect,
          a: o['--state-a'],
          b: o['--state-b'],
        }),
      );
      return 0;
    }
    await launcher([
      'launch',
      '--state',
      o['--state'],
      '--connect-config',
      o['--connect-config'],
    ]);
    return 0;
  }
  const state = assertStatePath(o['--state']);
  if (action === 'evidence') {
    const manifest = JSON.parse(
      regularFile(path.join(state, 'plugin-dsh.json'), 65536),
    );
    if (
      manifest.kind !== 'auriga-beta-plugin-dsh' ||
      manifest.schema !== 1 ||
      manifest.state !== state
    )
      throw new Error('不是 launcher 创建的 StateDir');
    const file = path.join(
      state,
      'dsh/profiles/web/auriga-connect/network/host/ledger.json',
    );
    const bytes = regularFile(file, 16 * 1024 * 1024);
    out({
      ...summarizeLedger(JSON.parse(bytes)),
      ledgerDigest: digest(bytes),
      observation:
        '仅原子快照只读观察；运行中可变化，不等于独立验签或原生输入证明',
    });
    return 0;
  }
  await launcher([action, '--state', state]);
  return 0;
}

if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  main(process.argv.slice(2))
    .then((code) => {
      process.exitCode = code;
    })
    .catch(() => {
      console.error(
        'blocked：操作失败；核公开配置、隔离路径及私有日志，勿删除账本或跳过 TLS',
      );
      process.exitCode = 1;
    });
}
