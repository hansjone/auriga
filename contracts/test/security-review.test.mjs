import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const read = (file) =>
  JSON.parse(readFileSync(new URL('../' + file, import.meta.url), 'utf8'));
const policy = read(
  'resource/unified-authorization.json',
).securityReviewQualification;
const fixture = read('resource/security-review-fixtures.json');
const permission = policy.permission;

test('security qualification uses trusted current permission and owner-only defaults', () => {
  assert.equal(permission, 'resource.versions.security_review');
  assert.equal(policy.source, 'ResourceAccessInputs.permissions');
  assert.deepEqual(policy.checkAt, ['review-decision', 'publication']);
  assert.equal(policy.requireSameTenant, true);
  assert.equal(policy.clientRoleIsDeclarationOnly, true);
  assert.equal(policy.revocable, true);
  assert.deepEqual(policy.revocationScope, {
    builtinOwner: 'remove-owner-role',
    customRole: 'revoke-permission-or-role',
  });
  assert.equal(policy.independentlyGrantable, true);
  assert.equal(policy.authorMayReview, false);
  assert.equal(policy.revokedSecurityReviewCounts, false);
  assert.equal(policy.countDistinctBy, 'tenantId+reviewerId');
  assert.deepEqual(
    read('catalog/m3.json').permissions[permission].builtinRoles,
    ['owner'],
  );
});

// Contract oracle only: principals are trusted server snapshots, never request data.
const valid = (input, review, principal) =>
  principal?.tenantId === input.tenantId &&
  principal.reviewerId === review.reviewerId &&
  review.tenantId === input.tenantId &&
  review.reviewerId !== input.authorId &&
  review.versionRevision === input.versionRevision &&
  review.digest === input.digest &&
  ['general', 'security'].includes(review.role) &&
  principal.permissions.includes('resource.versions.review') &&
  (review.role !== 'security' || principal.permissions.includes(permission));

for (const vector of fixture.cases) {
  test('security qualification fixture ' + vector.id, () => {
    const input = vector.input;
    if (vector.operation === 'review-decision') {
      const allowed = valid(input, input.review, input.principal);
      assert.deepEqual(
        {
          allowed,
          errorCode: allowed ? null : policy.missingQualificationError,
        },
        vector.expect,
      );
      return;
    }
    assert.equal(vector.operation, 'publication');
    const reviewers = new Set(),
      security = new Set();
    for (const review of input.reviews) {
      const principal = input.currentPrincipals.find(
        (p) =>
          p.reviewerId === review.reviewerId && p.tenantId === input.tenantId,
      );
      if (review.decision !== 'approve' || !valid(input, review, principal))
        continue;
      reviewers.add(review.reviewerId);
      if (review.role === 'security') security.add(review.reviewerId);
    }
    assert(['r1', 'r2', 'r3'].includes(input.riskLevel));
    const required = input.riskLevel === 'r1' ? 1 : 2;
    const allowed =
      reviewers.size >= required && (required === 1 || security.size >= 1);
    assert.deepEqual(
      {
        allowed,
        errorCode: allowed ? null : policy.insufficientPublicationQuorumError,
        distinctReviewers: reviewers.size,
        securityReviewers: security.size,
      },
      vector.expect,
    );
  });
}
