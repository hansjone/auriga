import { readFileSync } from 'node:fs';
import { parse } from 'yaml';

const name = 'auriga-resource-submit';
const markdown = new URL(
  '../../skills/auriga-resource-submit/SKILL.md',
  import.meta.url,
);

/** Register the packaged instructions in the DSH skill registry. @param {any} ctx */
export function installSubmissionSkill(ctx) {
  ctx.inject(['skills'], (/** @type {any} */ host) => {
    const raw = readFileSync(markdown, 'utf8');
    const frontmatter = /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/u.exec(raw);
    const metadata = frontmatter && parse(frontmatter[1]);
    const content = frontmatter && raw.slice(frontmatter[0].length).trim();
    if (
      metadata?.name !== name ||
      typeof metadata.description !== 'string' ||
      !metadata.description.trim() ||
      !content
    )
      throw new Error('auriga-dsh: invalid packaged submission skill');
    host.skills.register({
      name,
      description: metadata.description,
      provider: 'auriga-dsh',
      source: 'bundled',
      invocation: { modelInvocable: true, userInvocable: true },
      content,
    });
  });
}
