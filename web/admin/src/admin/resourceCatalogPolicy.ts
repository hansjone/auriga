import type { CatalogReview, CatalogDistribution } from './resourceCatalogApi';

const rank = { r1: 1, r2: 2, r3: 3 };
export function riskOptions(review: CatalogReview) {
  const r = review.version.risk;
  return (['r1', 'r2', 'r3'] as const).filter(
    (x) => rank[x] >= Math.max(rank[r.level], rank[r.minimumLevel]),
  );
}
export function publicationReady(review: CatalogReview, memberId: string) {
  const v = review.version,
    r = v.risk;
  if (
    review.authorId === memberId ||
    review.status !== 'approved' ||
    v.state !== 'approved'
  )
    return false;
  const current = review.decisions.filter(
    (d) =>
      d.versionRevision === v.revision &&
      d.digest === v.digest &&
      d.reviewerId !== review.authorId,
  );
  const latest = new Map(current.map((d) => [d.reviewerId, d]));
  const approvals = [...latest.values()].filter(
    (d) => d.decision === 'approve',
  );
  const high = r.level !== 'r1' || r.minimumLevel !== 'r1';
  return (
    ![...latest.values()].some((d) => d.decision === 'reject') &&
    approvals.length >= Math.max(r.requiredReviewers, high ? 2 : 1) &&
    approvals.filter((d) => d.role === 'security').length >=
      Math.max(r.requiredSecurityReviewers, high ? 1 : 0)
  );
}
export function distributionApplied(d: CatalogDistribution) {
  return (
    d.state === 'applied' &&
    d.selections.length > 0 &&
    d.selections.every((s) =>
      d.reports.some(
        (r) =>
          r.reportSource === 'node' &&
          r.state === 'applied' &&
          r.generation === d.generation &&
          r.observedGeneration === d.generation &&
          r.selectionRevision === d.selectionRevision &&
          r.identity.tenantId === s.identity.tenantId &&
          r.identity.version === s.identity.version &&
          r.identity.manifestDigest === s.identity.manifestDigest &&
          r.identity.kind === s.identity.kind &&
          r.identity.packageId === s.identity.packageId &&
          r.identity.versionId === s.identity.versionId,
      ),
    )
  );
}

export function textDiff(before: string, after: string) {
  const a = before.split('\n'),
    b = after.split('\n');
  return Array.from({ length: Math.max(a.length, b.length) }, (_, i) => ({
    line: i + 1,
    before: a[i] ?? '',
    after: b[i] ?? '',
    changed: a[i] !== b[i],
  }));
}
