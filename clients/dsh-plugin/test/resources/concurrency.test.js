import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate } from 'node:timers/promises';
import { fixture, id } from './fixtures.js';

const input = {
  packageId: id(4),
  versionId: id(5),
  action: 'install',
  key: 'initial-install',
};

for (const mode of ['install', 'upgrade']) {
  test(
    `reconciliation waits for an in-flight ${mode} before accepting revocations and enforcing`,
    { timeout: 10000 },
    async (t) => {
      const f = await fixture(t);
      if (mode === 'upgrade') {
        f.pack.level = 'l1';
        await f.engine.apply(input);
        f.upgrade();
        f.hostCalls.length = 0;
      }
      let enterEnable, releaseEnable;
      const entered = new Promise((resolve) => {
        enterEnable = resolve;
      });
      const released = new Promise((resolve) => {
        releaseEnable = resolve;
      });
      const originalEnable = f.host.enable;
      f.host.enable = async (name, enabled) => {
        const result = await originalEnable(name, enabled);
        if (enabled) {
          enterEnable();
          await released;
        }
        return result;
      };
      const applying = f.engine.apply({
        ...input,
        versionId: f.version.id,
        key: `concurrent-${mode}`,
      });
      // Observe a possible failure immediately, while retaining the outcome below.
      void applying.catch(() => {});
      await entered;
      const expectedCalls =
        mode === 'upgrade'
          ? ['disable', 'install', 'enable']
          : ['install', 'enable'];
      f.revocations.revision = 2;
      const list = await (
        await f.fetchImpl(
          new URL(
            '/api/resource/v1/revocations',
            f.runtime.config.platformOrigin,
          ),
          { method: 'GET' },
        )
      ).json();
      let accepted = false,
        enforced = false;
      const accepting = f.engine.acceptRevocations(list).then(() => {
        accepted = true;
      });
      const enforcing = f.engine.enforce().then(() => {
        enforced = true;
      });
      let outcomes;
      try {
        // Run independent queued work without using a guessed installation delay.
        await setImmediate();
        assert.equal(accepted, false);
        assert.equal(enforced, false);
        assert.equal(f.engine.cachedRevocations().revision, 1);
        assert.deepEqual(f.hostCalls, expectedCalls);
      } finally {
        releaseEnable();
        outcomes = await Promise.allSettled([applying, accepting, enforcing]);
      }
      assert.deepEqual(
        outcomes.map((outcome) => outcome.status),
        ['fulfilled', 'fulfilled', 'fulfilled'],
      );
      assert.equal(outcomes[0].value.state, 'applied');
      assert.equal(outcomes[0].value.localVersion, f.version.version);
      assert.equal(f.engine.cachedRevocations().revision, 2);
      assert.equal((await f.engine.list()).items[0].renewalState, 'current');
      assert.equal((await f.host.observe(f.pack.name)).enabled, true);
      assert.deepEqual(f.hostCalls, expectedCalls);
    },
  );
}
