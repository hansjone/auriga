import type { CatalogReview, CatalogDistribution } from './resourceCatalogApi';

const rank = { r1: 1, r2: 2, r3: 3 };
export function riskOptions(review: CatalogReview) {
  const r = review.version.risk;
  return (['r1', 'r2', 'r3'] as const).filter(
    (x) =>
      rank[x] >=
      Math.max(
        rank[r.level],
        rank[r.minimumLevel],
        review.version.kind === 'plugin' ? 2 : 1,
      ),
  );
}
export function publicationReady(review: CatalogReview, memberId: string) {
  return publicationBlockers(review, memberId).length === 0;
}
export function publicationBlockers(review: CatalogReview, memberId: string) {
  const v = review.version,
    r = v.risk;
  const issues: { code: string; actual?: number; required?: number }[] = [];
  if (!memberId || review.authorId === memberId)
    issues.push({ code: 'author' });
  if (review.status !== 'approved' || v.state !== 'approved')
    issues.push({ code: 'awaitingApproval' });
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
  const high =
    v.kind === 'plugin' || r.level !== 'r1' || r.minimumLevel !== 'r1';
  const required = Math.max(r.requiredReviewers, high ? 2 : 1),
    security = Math.max(r.requiredSecurityReviewers, high ? 1 : 0);
  if ([...latest.values()].some((d) => d.decision === 'reject'))
    issues.push({ code: 'hasRejection' });
  if (approvals.length < required)
    issues.push({
      code: 'missingReviewers',
      actual: approvals.length,
      required,
    });
  const actual = approvals.filter((d) => d.role === 'security').length;
  if (actual < security)
    issues.push({ code: 'missingSecurity', actual, required: security });
  return issues;
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
