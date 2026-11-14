// 本地投影契约参考判定，仅供共享向量测试。
import { createHash } from 'node:crypto';
import { digest, jcs } from './msgnet-v2-oracle.mjs';
export function evaluateLocalProjection(v) {
  const i = v.input,
    s = i.request.selector,
    m = i.manifest,
    old = i.previous;
  if (
    old &&
    (BigInt(s.generation) < BigInt(old.generation) ||
      (s.generation === old.generation &&
        BigInt(s.keyset_version) < BigInt(old.keyset_version)) ||
      BigInt(s.bundle_version) < BigInt(old.bundle_version) ||
      BigInt(s.material_revision) < BigInt(old.material_revision))
  )
    return 'version_rollback';
  if (
    old &&
    s.material_revision === old.material_revision &&
    jcs(s) !== jcs(old)
  )
    return 'same_version_conflict';
  if (digest(m) !== s.manifest_digest) return 'invalid_chain';
  const binding = { ...s };
  delete binding.manifest_digest;
  const content = { ...m };
  delete content.files;
  if (jcs(binding) !== jcs(content)) return 'invalid_chain';
  const names = m.files.map((x) => x.name);
  if (
    jcs(names) !== jcs([...new Set(names)].sort()) ||
    !names.includes('publication.json') ||
    !names.includes('bundle.json') ||
    jcs(Object.keys(i.files).sort()) !== jcs(names)
  )
    return 'invalid_chain';
  for (const file of m.files) {
    const bytes = Buffer.from(i.files[file.name], 'utf8');
    if (
      bytes.length !== file.size ||
      'sha256:' + createHash('sha256').update(bytes).digest('hex') !==
        file.sha256
    )
      return 'invalid_chain';
  }
  if (jcs(s) !== jcs(i.published)) return 'invalid_chain';
  if (
    !i.persisted ||
    (i.request.operation === 'confirm' && !i.loaded) ||
    i.failed
  )
    return 'projection_pending';
  return 'accept';
}
