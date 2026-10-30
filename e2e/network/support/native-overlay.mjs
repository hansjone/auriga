import assert from 'node:assert/strict';

export const staleObjectMarker =
  'stale native object must not resolve after real agent/session replacement';

// One production overlay: weaken object identity to id equality in the native
// resolver. The stale REAL agent keeps its old session after a genuine resume.
export function nativeIdentityBypass(source) {
  for (const [before, after] of [
    [
      'ctx.agents.get(id) === agent &&',
      'ctx.agents.get(id)?.id === agent.id &&',
    ],
    [
      'ctx.sessions.get(id) === agent.session &&',
      'ctx.sessions.get(id)?.id === agent.session.id &&',
    ],
    ['native.agent !== agent', 'native.agent?.id !== agent.id'],
  ]) {
    assert.equal(
      source.split(before).length,
      2,
      'overlay anchor must match exactly once',
    );
    source = source.replace(before, after);
  }
  return source;
}

export function assertNativeVerdict({ code, stdout, stderr }, mode) {
  assert.equal(
    stderr.trim(),
    '',
    'native checks must not emit cleanup/unhandled errors',
  );
  if (mode === 'Control') {
    assert.equal(code, 0);
    assert.match(stdout, /# pass 2\b/);
    assert.match(stdout, /# fail 0\b/);
  } else {
    assert.equal(mode, 'NativeIdentityBypass');
    assert.equal(code, 1);
    assert.match(stdout, /# pass 1\b/);
    assert.match(stdout, /# fail 1\b/);
    assert.ok(stdout.includes(staleObjectMarker));
    assert.ok(stdout.includes("code: 'ERR_ASSERTION'"));
    assert.ok(!stdout.includes('native teardown failed'));
  }
  assert.match(stdout, /# cancelled 0\b/);
  assert.match(stdout, /# skipped 0\b/);
}
