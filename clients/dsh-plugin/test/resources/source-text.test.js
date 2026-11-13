import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { TextDecoder } from 'node:util';
test('P1-C source text remains valid UTF-8 with no replacement glyphs or question-mark placeholders', () => {
  const decoder = new TextDecoder('utf-8', { fatal: true });
  function check(dir) {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, entry.name);
      if (entry.isDirectory()) check(path);
      else if (/\.(?:js|ts|css)$/.test(entry.name)) {
        const text = decoder.decode(readFileSync(path));
        assert.ok(!text.includes(String.fromCharCode(0xfffd)), path);
        assert.doesNotMatch(text, /\?{3,}/, path);
        assert.doesNotMatch(text, /\u951f\u65a4\u62f7/, path);
      }
    }
  }
  for (const path of [
    '../../src/resources',
    '../../src/settings',
    '../../../../web/panel/src',
  ])
    check(fileURLToPath(new URL(path, import.meta.url)));
});
