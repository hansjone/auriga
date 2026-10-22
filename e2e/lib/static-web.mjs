import assert from 'node:assert/strict';

function contentType(response) {
  return String(response.res.headers.get('content-type') || '')
    .split(';')[0]
    .trim()
    .toLowerCase();
}

/** Parse only the local Vite entry assets. Reject before making any request. */
export function entryAssets(response, audience) {
  assert.equal(response.status, 200, `${audience} entry status`);
  assert.equal(contentType(response), 'text/html', `${audience} entry MIME`);
  const html = response.text;
  assert.match(html, /<!doctype html>/i, 'entry must be an HTML document');
  assert.match(html, new RegExp(`<title>Auriga ${audience}</title>`, 'i'));
  assert.match(html, /<div\s+id=["']app["']/i, 'entry app mount missing');
  const assets = [];
  for (const tag of html.match(/<(?:script|link)\b[^>]*>/gi) || []) {
    const attrs = Object.fromEntries(
      [...tag.matchAll(/([\w-]+)\s*=\s*(["'])(.*?)\2/g)].map((m) => [
        m[1].toLowerCase(),
        m[3],
      ]),
    );
    const kind = /^<script/i.test(tag)
      ? attrs.type === 'module' && 'js'
      : attrs.rel === 'stylesheet' && 'css';
    if (!kind) continue;
    const reference = kind === 'js' ? attrs.src : attrs.href;
    // No authority, query, fragment, encoded path, traversal or API endpoints.
    // This is deliberately limited to the assets emitted by this Vite build.
    assert.match(
      reference || '',
      new RegExp(`^(?:/|\\./)?assets/[A-Za-z0-9_-]+\\.${kind}$`),
      'entry asset must be a local build asset',
    );
    assets.push({ path: '/' + reference.replace(/^(?:\/|\.\/)/, ''), kind });
  }
  assert(
    assets.some((asset) => asset.kind === 'js'),
    'module entry missing',
  );
  assert(
    assets.some((asset) => asset.kind === 'css'),
    'stylesheet missing',
  );
  assert(assets.length <= 32, 'too many entry assets');
  return assets;
}

export function assertStaticAsset(response, kind) {
  assert.equal(response.status, 200, `${kind} asset status`);
  assert(
    (kind === 'js'
      ? ['text/javascript', 'application/javascript']
      : ['text/css']
    ).includes(contentType(response)),
    `${kind} asset MIME`,
  );
  const text = response.text.trim();
  assert(text.length > 0, `${kind} asset empty`);
  assert(!/^</.test(text), `${kind} asset returned HTML`);
  try {
    JSON.parse(text);
  } catch {
    return;
  }
  assert.fail(`${kind} asset returned JSON`);
}

export async function verifyStaticWeb(request, audience) {
  const assets = entryAssets(await request('/login'), audience);
  for (const asset of assets) {
    assertStaticAsset(await request(asset.path), asset.kind);
  }
  return assets;
}
