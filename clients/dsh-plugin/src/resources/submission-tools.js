import { randomUUID } from 'node:crypto';
import { isAbsolute } from 'node:path';
import { registrationSession } from '../network/host/native/host-port.js';
import { catalogIdentity } from './catalog-api.js';
import { diagnoseResource } from './diagnostics.js';
import { exact, refuse, uuid } from './trust.js';
import documents from './schema-data.js';
import { schemaValid } from './schema.js';

const maxArchiveBytes = 64 * 1024 * 1024;
const handleTtl = 15 * 60 * 1000;
const maxHandles = 8;
const maxReviewReason = 1024;
const maxChangeNotes = 4096;
const str = { type: 'string', minLength: 1 };
const tags = documents.package.$defs.Plugin.properties.tags;
const permissionsSchema = documents.package.$defs.Permissions;
const metadataFields = {
  name: {
    ...str,
    maxLength: 64,
    description:
      '可选。目录资源名，仅允许小写字母、数字和短横线（^[a-z0-9]+(-[a-z0-9]+)*$），不要带 npm @scope；留空将自动从 package.json 或 SKILL.md 提取。/ Optional catalog slug (lowercase kebab-case without @scope). Omit to auto-derive.',
  },
  version: {
    ...str,
    maxLength: 128,
    description:
      '严格 SemVer 版本号，如 1.0.0。技能必填；插件默认读取 package.json 的 version，升级时须递增。/ Strict SemVer (e.g. 1.0.0). Required for skills; plugins use package.json version.',
  },
  description: {
    ...str,
    maxLength: 1024,
    description:
      '资源简介（1-1024 字符）。插件默认读取 package.json 的 description，技能须与 SKILL.md 一致。请写明用途与适用场景。/ Clear resource summary (1-1024 chars).',
  },
  license: {
    ...str,
    maxLength: 128,
    description:
      '许可证（如 MIT、Apache-2.0、Proprietary）。技能必填；插件留空时自动读取 package.json 的 license。/ License identifier; plugins fall back to package.json license.',
  },
  dshVersionRange: {
    ...str,
    maxLength: 128,
    description:
      '可选。兼容的 DSH 版本范围（如 >=0.2.1-rc.3）。插件若声明了 engines.dsh 须与其一致。/ Optional compatible DSH SemVer range.',
  },
  changeNotes: {
    ...str,
    maxLength: maxChangeNotes,
    description:
      '可选（升级版本强烈建议填写）。本次发布或升级的变更说明，审核员将在审核页直接看到。/ Optional release/upgrade change notes displayed to reviewers.',
  },
};
const validationFailed = () => refuse('common.validation_failed');
const unauthorized = () => refuse('common.unauthenticated');

/** @param {unknown} value @param {number} [max] */
function text(value, max = 4096) {
  return (
    typeof value === 'string' &&
    !!value.trim() &&
    value.length <= max &&
    !value.includes('\0')
  );
}

const hints = {
  'common.validation_failed': '检查目录、参数和清单后重新预检。',
  'common.unauthenticated':
    '平台登录已失效，请在桌面 Auriga 重新扫码登录，再预检。',
  'common.forbidden':
    '请检查租户成员身份及目标资源归属，并在桌面主会话中操作。',
  'common.not_found': '检查目标资源 ID，或在当前会话重新预检取得有效句柄。',
  'common.conflict': '检查目标资源和版本。若更改提交参数，重新预检。',
  'common.precondition_failed':
    '目标资源有并发更新，确认最新状态后用原参数重试。',
  'common.rate_limited': '等待当前工作结束或旧句柄到期后重试。',
  'common.unavailable':
    '平台连接暂时不可达（系统正在自动重连，无需重新扫码），待连接恢复后用原句柄和参数重试。',
  'resource.digest_mismatch': '检查源文件并重新预检，随后提交新的句柄。',
  'resource.storage_unavailable': '等待当前账号资源状态恢复，随后重试。',
};
const uploadRules = {
  bundled_dependency_required: '将声明的运行时依赖纳入归档，再重新预检。',
  lifecycle_scripts_disallowed: '移除归档中的包生命周期脚本，再重新预检。',
  bundle_patch_missing: '补入 package.json 声明的 DSH patch 文件，再重新预检。',
  native_build_disallowed: '移除包的原生构建文件和声明，再重新预检。',
};

/** @param {unknown} value @param {number} size */
function diagnosticText(value, size) {
  return typeof value === 'string'
    ? value
        .replace(/[\p{C}\r\n]/gu, ' ')
        .trim()
        .slice(0, size)
    : '';
}

/** The SDK displays Error.message. Use local preflight context and fixed platform replies.
 * @param {any} error @param {'prepare'|'submit'|'list'} action */
function toolFailure(error, action) {
  const { code } = diagnoseResource(error, 'catalog').body;
  const stage =
    action === 'submit' &&
    ['package', 'upload', 'submit'].includes(error?.stage)
      ? error.stage
      : action;
  let detail = '';
  if (action === 'prepare' && code === 'common.validation_failed') {
    const path = diagnosticText(error.path, 240);
    const reason = diagnosticText(error.reason, 240);
    if (path && !/^[/\\]|^[a-z]:/i.test(path) && reason)
      detail = path + ': ' + reason + ' ';
  }
  if (stage === 'upload' && Object.hasOwn(uploadRules, error?.rule))
    detail =
      error.rule +
      ' ' +
      uploadRules[/** @type {keyof typeof uploadRules} */ (error.rule)];
  const hint =
    detail && stage === 'upload'
      ? ''
      : (hints[/** @type {keyof typeof hints} */ (code)] ??
        hints['common.unavailable']);
  return Object.assign(new Error(`${code} [${stage}] ${detail}${hint}`), {
    code,
  });
}

/** @param {any} args */
function checkPrepare(args) {
  if (
    !exact(args, ['kind', 'directory', 'metadata']) ||
    !['plugin', 'skill'].includes(args.kind) ||
    !text(args.directory) ||
    !args.metadata ||
    typeof args.metadata !== 'object' ||
    Array.isArray(args.metadata)
  )
    validationFailed();
  const { permissions, tags: pluginTags, ...fields } = args.metadata;
  if (
    Object.entries(fields).some(
      ([key, value]) =>
        !Object.hasOwn(metadataFields, key) ||
        !text(value, key === 'changeNotes' ? maxChangeNotes : 1024),
    ) ||
    (args.kind === 'skill' &&
      (!text(fields.version) || !text(fields.license))) ||
    (pluginTags !== undefined &&
      (args.kind !== 'plugin' ||
        !Array.isArray(pluginTags) ||
        pluginTags.length > 5 ||
        new Set(pluginTags).size !== pluginTags.length ||
        pluginTags.some(
          (tag) =>
            typeof tag !== 'string' ||
            tag.length > 32 ||
            !/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(tag),
        ))) ||
    !schemaValid('package', 'Permissions', permissions)
  )
    validationFailed();
}

/** @param {'plugin'|'skill'} kind @param {any} permissions @param {boolean} [hasScripts] */
function estimateRisk(kind, permissions, hasScripts = false) {
  if (kind === 'plugin' || hasScripts) return 'r2';
  const hasPermissions =
    (permissions?.tools?.length ?? 0) > 0 ||
    (permissions?.outboundHosts?.length ?? 0) > 0 ||
    (permissions?.dataAccess?.length ?? 0) > 0;
  return hasPermissions ? 'r2' : 'r1';
}

/** @typedef {{manifest:any,archive:Buffer,files?:string[],hasScripts?:boolean,changeNotes?:string}} Prepared */
/** @typedef {{cwd:string,directory:string,kind:'plugin'|'skill',metadata:any}} PrepareInput */
/** @typedef {{packageId:string,versionId:string,state:'pending_review',digest:string}} Receipt */
/** @typedef {{reason:string,packageId?:string,changeNotes?:string,key:string}} Operation */
/** @typedef {{prepared:Prepared, session:any, cwd:string, expires:number, existingPackageId?:string, operation?:Operation, pending?:Promise<Receipt>, receipt?:Receipt}} Snapshot */

/** Register with the desktop host. Scope changes retire every handle created under the previous login.
 * @param {any} ctx
 * @param {{current:()=>any,service:(runtime:any)=>any,prepare:(input:PrepareInput)=>Promise<Prepared>,now?:()=>number}} options */
export function installSubmissionTools(
  ctx,
  { current, service, prepare, now = Date.now },
) {
  ctx.inject(
    [
      'tools',
      'sessions',
      'sessionController',
      'agents',
      'sessionPersistence',
      'workspaceRegistry',
    ],
    (/** @type {any} */ host) => {
      /** @type {Map<string, Snapshot>} */
      const handles = new Map();
      /** @type {{runtime:any, stamp:string}|undefined} */
      let scope;
      let closed = false;
      let preparing = false;
      function active() {
        if (closed) return unauthorized();
        try {
          const runtime = current();
          const generation = runtime?.catalogGeneration?.();
          if (!generation || runtime.resourceIdentity) return unauthorized();
          const { tenantId, ownerId, hostId } = catalogIdentity(runtime);
          const stamp = JSON.stringify([generation, tenantId, ownerId, hostId]);
          if (scope?.runtime !== runtime || scope?.stamp !== stamp) {
            handles.clear();
            scope = { runtime, stamp };
          }
          return scope;
        } catch {
          scope = undefined;
          handles.clear();
          return unauthorized();
        }
      }

      /** @param {any} execution */
      async function admitted(execution) {
        const source = active();
        /** @type {ReturnType<typeof registrationSession>} */
        let native;
        try {
          native = registrationSession(host, execution);
        } catch {
          return refuse('common.forbidden');
        }
        const check = () => {
          if (active() !== source) return unauthorized();
          if (source.runtime.vault?.snapshot?.().resourceIsolationPending)
            return refuse('resource.storage_unavailable');
          try {
            native.check();
            if (execution.signal?.aborted) return refuse('common.forbidden');
          } catch {
            return refuse('common.forbidden');
          }
        };
        async function verify() {
          check();
          try {
            await native.verify();
          } catch {
            check();
            return refuse('common.forbidden');
          }
          check();
        }
        await verify();
        for (const [handle, row] of handles)
          if (row.expires <= now()) handles.delete(handle);
        return {
          native,
          source,
          check,
          verify,
          session: host.sessions.get(native.id),
        };
      }

      /** @param {any} args @param {any} execution */
      async function listTool(args, execution) {
        if (
          args !== undefined &&
          (typeof args !== 'object' ||
            args === null ||
            Array.isArray(args) ||
            Object.keys(args).some((k) => k !== 'kind') ||
            (args.kind !== undefined &&
              !['plugin', 'skill'].includes(args.kind)))
        )
          return validationFailed();
        const { source, check, verify } = await admitted(execution);
        check();
        const svc = service(source.runtime);
        if (typeof svc?.ownedPackages !== 'function')
          return refuse('common.unavailable');
        const result = await svc.ownedPackages(args?.kind, verify);
        check();
        return Array.isArray(result)
          ? { items: result, complete: true }
          : { items: result.items, complete: !!result.complete };
      }

      /** @param {any} args @param {any} execution */
      async function prepareTool(args, execution) {
        checkPrepare(args);
        const { native, session, source, check, verify } =
          await admitted(execution);
        check();
        if (!isAbsolute(native.cwd)) return refuse('common.forbidden');
        if (preparing || handles.size >= maxHandles)
          return refuse('common.rate_limited');
        preparing = true;
        try {
          const { directory, kind, metadata } = args;
          /** @type {Prepared} */
          const { manifest, archive, files, hasScripts, changeNotes } =
            await prepare({
              cwd: native.cwd,
              directory,
              kind,
              metadata,
            });
          check();
          if (
            !Buffer.isBuffer(archive) ||
            !archive.length ||
            archive.length > maxArchiveBytes ||
            manifest?.kind !== kind ||
            !text(manifest.name) ||
            !text(manifest.version)
          )
            return validationFailed();
          const retained = [...handles.values()].reduce(
            (bytes, row) => bytes + row.prepared.archive.length,
            archive.length,
          );
          if (retained > maxArchiveBytes) return refuse('common.rate_limited');

          // Best-effort lookup to detect upgrade mode against an existing owned package.
          /** @type {{packageId:string,latestVersion:string|null,latestState:string|null,publishedVersion:string|null,versionsComplete:boolean}|undefined} */
          let existingPackage;
          /** @type {'create'|'upgrade'|'unknown'} */
          let mode = 'unknown';
          const warnings = [];
          try {
            const svc = service(source.runtime);
            if (typeof svc?.ownedPackages === 'function') {
              const raw = await svc.ownedPackages(kind, verify);
              const owned = Array.isArray(raw) ? raw : (raw?.items ?? []);
              const complete = Array.isArray(raw) ? true : !!raw?.complete;
              const match = owned.find(
                (/** @type {any} */ item) =>
                  item.kind === kind && item.name === manifest.name,
              );
              if (match) {
                mode = 'upgrade';
                existingPackage = {
                  packageId: match.packageId,
                  latestVersion: match.latestVersion ?? null,
                  latestState: match.latestState ?? null,
                  publishedVersion: match.publishedVersion ?? null,
                  versionsComplete: match.versionsComplete !== false,
                };
                if (
                  match.latestVersion === manifest.version ||
                  match.publishedVersion === manifest.version
                ) {
                  warnings.push(
                    `版本 ${manifest.version} 已存在，升级请先递增版本号后重新预检。`,
                  );
                } else if (match.versionsComplete === false) {
                  warnings.push(
                    '目标资源历史版本超过分页上限，未能查全最新版本，请确认新版本号未与已有版本冲突。',
                  );
                }
              } else if (complete) {
                mode = 'create';
              } else {
                mode = 'unknown';
                warnings.push(
                  '目录分页超过上限，未能完整核对是否已有同名资源；如为升级请先确认并显式传入 packageId。',
                );
              }
            }
          } catch {
            mode = 'unknown';
            warnings.push(
              '未能查询当前目录已有资源，无法自动判断首次发布或升级；如为升级请显式传入 packageId。',
            );
          }
          check();

          const normalizedChangeNotes =
            typeof changeNotes === 'string' && changeNotes.trim()
              ? changeNotes.trim()
              : typeof metadata.changeNotes === 'string' &&
                  metadata.changeNotes.trim()
                ? metadata.changeNotes.trim()
                : undefined;

          const handle = randomUUID();
          handles.set(handle, {
            prepared: {
              manifest: globalThis.structuredClone(manifest),
              archive: Buffer.from(archive),
              ...(Array.isArray(files) ? { files: [...files] } : {}),
              ...(normalizedChangeNotes
                ? { changeNotes: normalizedChangeNotes }
                : {}),
            },
            session,
            cwd: native.cwd,
            expires: now() + handleTtl,
            ...(existingPackage
              ? { existingPackageId: existingPackage.packageId }
              : {}),
          });

          const fileList = Array.isArray(files) ? files : [];
          const archiveDigest =
            typeof manifest.artifacts?.[0]?.digest === 'string'
              ? manifest.artifacts[0].digest
              : '';
          const dshVersionRange =
            kind === 'plugin'
              ? manifest.plugin?.dshVersionRange
              : manifest.skill?.dshVersionRange;
          return {
            handle,
            mode,
            kind,
            name: manifest.name,
            version: manifest.version,
            description: manifest.description ?? '',
            license: manifest.license ?? '',
            ...(dshVersionRange ? { dshVersionRange } : {}),
            ...(kind === 'plugin' && Array.isArray(manifest.plugin?.tags)
              ? { tags: [...manifest.plugin.tags] }
              : {}),
            permissions: manifest.permissions
              ? globalThis.structuredClone(manifest.permissions)
              : { tools: [], outboundHosts: [], dataAccess: [] },
            ...(normalizedChangeNotes
              ? { changeNotes: normalizedChangeNotes }
              : {}),
            digest: archiveDigest,
            archiveBytes: archive.length,
            fileCount: fileList.length,
            estimatedRisk: estimateRisk(
              kind,
              manifest.permissions,
              Boolean(hasScripts),
            ),
            ...(existingPackage ? { existingPackage } : {}),
            ...(warnings.length ? { warnings } : {}),
          };
        } catch (error) {
          check();
          throw error;
        } finally {
          preparing = false;
        }
      }

      /** @param {any} args @param {any} execution */
      async function submitTool(args, execution) {
        const keys = args && typeof args === 'object' ? Object.keys(args) : [];
        const allowedKeys = new Set([
          'handle',
          'reason',
          'packageId',
          'changeNotes',
        ]);
        if (
          !args ||
          typeof args !== 'object' ||
          Array.isArray(args) ||
          !Object.hasOwn(args, 'handle') ||
          !Object.hasOwn(args, 'reason') ||
          keys.some((k) => !allowedKeys.has(k)) ||
          !uuid(args.handle) ||
          !text(args.reason, maxReviewReason) ||
          (Object.hasOwn(args, 'packageId') && !uuid(args.packageId)) ||
          (Object.hasOwn(args, 'changeNotes') &&
            !text(args.changeNotes, maxChangeNotes))
        )
          return validationFailed();
        const { handle, reason, packageId } = args;
        const { native, session, source, check, verify } =
          await admitted(execution);
        check();
        const row = handles.get(handle);
        if (
          !row ||
          row.session !== session ||
          row.cwd !== native.cwd ||
          row.expires <= now()
        )
          return refuse('common.not_found');
        const resolvedPackageId = packageId ?? row.existingPackageId;
        const resolvedChangeNotes =
          (Object.hasOwn(args, 'changeNotes')
            ? args.changeNotes.trim()
            : row.prepared.changeNotes) ?? undefined;
        const operation = (row.operation ??= {
          reason,
          key: randomUUID(),
          ...(resolvedPackageId ? { packageId: resolvedPackageId } : {}),
          ...(resolvedChangeNotes ? { changeNotes: resolvedChangeNotes } : {}),
        });
        if (
          operation.reason !== reason ||
          operation.packageId !== resolvedPackageId ||
          operation.changeNotes !== resolvedChangeNotes
        )
          return refuse('common.conflict');
        if (row.receipt) return row.receipt;
        const expectedDigest =
          typeof row.prepared.manifest?.artifacts?.[0]?.digest === 'string'
            ? row.prepared.manifest.artifacts[0].digest
            : '';
        const pending = (row.pending ??= (async () => {
          const { packageId, versionId, state, digest } = await service(
            source.runtime,
          ).submitPrepared(
            {
              manifest: globalThis.structuredClone(row.prepared.manifest),
              archive: Buffer.from(row.prepared.archive),
              ...operation,
            },
            verify,
          );
          if (
            !uuid(packageId) ||
            !uuid(versionId) ||
            state !== 'pending_review' ||
            typeof digest !== 'string' ||
            !digest ||
            (expectedDigest && digest !== expectedDigest)
          )
            return refuse('common.unavailable');
          return Object.freeze({
            packageId,
            versionId,
            state,
            digest,
          });
        })());
        try {
          const receipt = await pending;
          await verify();
          row.receipt = receipt;
          return receipt;
        } catch (error) {
          check();
          throw error;
        } finally {
          if (row.pending === pending) row.pending = undefined;
        }
      }

      /** @param {any} tool @param {'prepare'|'submit'|'list'} action */
      function register(tool, action) {
        return host.tools.register({
          ...tool,
          output: {
            schema: { type: 'object', additionalProperties: true },
            render: (/** @type {any} */ _args, /** @type {any} */ value) => [
              { type: 'text', text: JSON.stringify(value) },
            ],
          },
          async execute(/** @type {any} */ args, /** @type {any} */ execution) {
            if (closed) return unauthorized();
            try {
              return await tool.execute(args, execution);
            } catch (error) {
              throw toolFailure(error, action);
            }
          },
        });
      }
      host.effect(() => {
        /** @type {(()=>void)[]} */
        const disposers = [];
        const dispose = () => {
          closed = true;
          scope = undefined;
          handles.clear();
          for (const unregister of disposers.reverse()) unregister();
        };
        try {
          disposers.push(
            register(
              {
                name: 'auriga_resource_list',
                description:
                  '查询当前登录用户在 Auriga 目录中已发布的或待审核的插件/技能，获取 packageId、当前最新版本和状态，用于升级已有资源或追踪审核状态。/ List catalog plugins and skills owned by the authenticated user for upgrades and status checks.',
                parameters: {
                  type: 'object',
                  properties: {
                    kind: {
                      type: 'string',
                      enum: ['plugin', 'skill'],
                      description:
                        '可选，按资源类型过滤（plugin 或 skill）。/ Optional kind filter.',
                    },
                  },
                  additionalProperties: false,
                },
                execute: listTool,
              },
              'list',
            ),
          );
          disposers.push(
            register(
              {
                name: 'auriga_resource_prepare',
                description:
                  '预检当前会话工作目录内的 DSH 插件或技能，返回短期送审句柄及完整摘要（含是否命中已有资源升级）。提交人身份由平台根据登录认证自动记录，无需也不应填写作者字段。/ Preflight a plugin or skill directory relative to this session and return a short-lived handle and summary. Author identity is resolved automatically by the platform.',
                parameters: {
                  type: 'object',
                  properties: {
                    kind: {
                      type: 'string',
                      enum: ['plugin', 'skill'],
                      description: '资源类型：plugin 或 skill。',
                    },
                    directory: {
                      ...str,
                      description:
                        '相对会话工作目录，例如 skills/example 或 plugins/my-plugin / Relative directory, e.g. skills/example',
                    },
                    metadata: {
                      type: 'object',
                      description:
                        '资源元数据。作者身份由平台认证信息自动填充，请勿填写 author。',
                      properties: {
                        ...metadataFields,
                        tags,
                        permissions: permissionsSchema,
                      },
                      required: ['permissions'],
                      additionalProperties: false,
                    },
                  },
                  required: ['kind', 'directory', 'metadata'],
                  additionalProperties: false,
                },
                execute: prepareTool,
              },
              'prepare',
            ),
          );
          disposers.push(
            register(
              {
                name: 'auriga_resource_submit',
                description:
                  '使用同一会话的预检句柄提交成员资源审核（支持首次发布与版本升级）。升级已有资源时自动关联预检匹配的 packageId 或显式传入 packageId，并建议提供变更说明 changeNotes。/ Submit the prepared resource for review (create or upgrade).',
                parameters: {
                  type: 'object',
                  properties: {
                    handle: {
                      ...str,
                      description: 'auriga_resource_prepare 返回的预检句柄。',
                    },
                    reason: {
                      ...str,
                      maxLength: maxReviewReason,
                      description:
                        '送审理由（1-1024 字符），将展示给审核员，请简明说明本次提交/升级目的。',
                    },
                    packageId: {
                      ...str,
                      description:
                        '可选。向已有资源升级追加新版本时指定目标资源 packageId（预检命中同名已有资源时会自动关联）。',
                    },
                    changeNotes: {
                      ...str,
                      maxLength: maxChangeNotes,
                      description:
                        '可选。本次发布或升级的详细变更说明（若预检时已传则可省略）。',
                    },
                  },
                  required: ['handle', 'reason'],
                  additionalProperties: false,
                },
                execute: submitTool,
              },
              'submit',
            ),
          );
        } catch (error) {
          dispose();
          throw error;
        }
        return dispose;
      }, 'auriga-dsh: desktop resource submission tools');
    },
  );
}
