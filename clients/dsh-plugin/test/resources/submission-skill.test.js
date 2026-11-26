import test from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { installSubmissionSkill } from '../../src/resources/submission-skill.js';

const name = 'auriga-resource-submit';
const assertSkill = (skill) => {
  assert.equal(skill.name, name);
  assert.match(skill.description, /发布到 Auriga/);
  assert.deepEqual(skill.invocation, {
    modelInvocable: true,
    userInvocable: true,
  });
  for (const item of [
    'auriga_resource_prepare',
    'auriga_resource_submit',
    'skills/example/SKILL.md',
    'plugins/example/package.json',
    'pending_review',
    'common.not_found',
  ])
    assert.ok(skill.content.includes(item), item);
};

test('desktop submission skill loads the actual packaged instructions through the injected registry', () => {
  const ctx = {
    inject(services, callback) {
      assert.deepEqual(services, ['skills']);
      callback({ skills: { register: assertSkill } });
    },
  };
  installSubmissionSkill(ctx);
});

const root = process.env.AURIGA_DSH_PUBLIC_PACKAGE_ROOT;
if (process.env.AURIGA_REQUIRE_PUBLIC_HOST === '1' && !root)
  throw new Error('Pinned public-host root is required');

test(
  'pinned DSH skill registry discovers, loads and removes the desktop instructions',
  {
    skip: !root && 'Plugin workflow supplies the public rc.3 SDK',
  },
  async (t) => {
    const resolve = createRequire(join(root, 'package.json'));
    const load = async (name) =>
      import(pathToFileURL(resolve.resolve('@deepseek-ai/' + name)).href);
    const [{ Context }, { SkillRegistry }] = await Promise.all([
      load('cordis'),
      load('dsh-skill'),
    ]);
    const ctx = new Context();
    t.after(() => ctx.fiber.dispose());
    await ctx.plugin(SkillRegistry, {});
    const fiber = await ctx.plugin({
      name: 'auriga-resource-submission',
      apply: installSubmissionSkill,
    });
    assert.ok(
      (await ctx.skills.list()).some(
        (skill) => skill.name === name && skill.invocation.modelInvocable,
      ),
    );
    assertSkill(await ctx.skills.get(name));
    await fiber.dispose();
    assert.ok(!(await ctx.skills.list()).some((skill) => skill.name === name));
  },
);
