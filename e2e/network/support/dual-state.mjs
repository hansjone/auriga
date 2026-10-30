import assert from 'node:assert/strict';
import { resolve, dirname, join } from 'node:path';

export function dualHostState(nodeOwner, javaDirectory, name) {
  const owner = resolve(nodeOwner),
    java = resolve(javaDirectory);
  assert.equal(
    dirname(owner),
    dirname(java),
    'Node and Java private roots must be siblings',
  );
  assert.notEqual(owner, java, 'Node must own a separate private root');
  assert.ok(
    ['A', 'B'].includes(name),
    'only the two owned DSH hosts are allowed',
  );
  const state = join(owner, 'dsh-' + name);
  assert.notEqual(
    state,
    java,
    'DSH profile must never enter the Java cleanup tree',
  );
  return state;
}
