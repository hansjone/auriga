// Deployment browser acceptance. JSON input comes over stdin; never print or
// persist credentials/cookies. Uses only a fresh admin session, no model calls.
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {mkdir} from 'node:fs/promises';
import {createHmac, randomBytes} from 'node:crypto';
import path from 'node:path';
import {setTimeout as delay} from 'node:timers/promises';
import {chromium} from '../../infra/edge/browser-tests/node_modules/playwright/index.mjs';

const input = JSON.parse(readFileSync(0, 'utf8'));
const admin = new URL(input.admin).origin;
const relay = new URL(input.relay).origin;
let browser, context, stage = 'launch';
function otp(secret) {
  let bits = '';
  for (const c of secret.replace(/=+$/, '').toUpperCase()) bits += 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'.indexOf(c).toString(2).padStart(5,'0');
  const key = Buffer.from(bits.match(/.{8}/g).map((s) => parseInt(s,2)));
  const count = Buffer.alloc(8);
  count.writeBigUInt64BE(BigInt(Math.floor(Date.now()/30000)));
  const digest = createHmac('sha1',key).update(count).digest();
  return String((digest.readUInt32BE(digest.at(-1)&15)&0x7fffffff)%1000000).padStart(6,'0');
}
async function post(route, data, extra={}) {
  const response = await context.request.post(admin+route, {data, headers:{Origin:admin, ...extra}});
  if (![200,201,204].includes(response.status())) throw new Error('admin request HTTP '+response.status());
  return response.status() === 204 ? null : response.json();
}
try {
  browser = await chromium.launch({channel:'chromium', headless:true, args:['--no-proxy-server']});
  context = await browser.newContext();
  stage = 'platform-login';
  let transaction = await post('/api/iam/v1/login/transactions', {providerId:input.credentials.providerId, returnTo:'/'});
  // Use a fresh TOTP counter so an earlier acceptance run cannot cause replay.
  await delay(30050-(Date.now()%30000));
  for (let n=0; transaction.status !== 'completed' && n<4; n++) {
    const form = transaction.step?.formId;
    let values;
    if (form === 'emergency') values = {username:input.credentials.username, password:input.credentials.password, otp:otp(input.credentials.totpSecret)};
    else if (form === 'totp' || form === 'platform_totp') values = {code:otp(input.credentials.totpSecret)};
    else throw new Error('unexpected platform login form');
    transaction = await post('/api/iam/v1/login/transactions/'+transaction.id+'/advance', {type:'form', formId:form, values});
  }
  assert.equal(transaction.status,'completed');
  const page = await context.newPage();
  page.setDefaultTimeout(45000);
  stage = 'admin-model-link';
  await page.goto(admin+'/');
  const finish = page.waitForResponse(relay+'/ai/session');
  await page.locator('a[href="/ai/ui/"]').click();
  assert.equal((await finish).status(),303);
  await page.waitForURL((url)=>url.origin===relay && url.pathname.startsWith('/ai/ui'));
  for (const [label,file,route,title] of [
    ['Models + Endpoints','models','models-and-endpoints','Model Management'],
    ['Virtual Keys','keys','api-keys','Virtual Keys'],
    ['Usage','usage','usage','Usage'],
  ]) {
    stage = file;
    await page.getByText(label,{exact:true}).first().click();
    await page.waitForURL((url)=>url.pathname.includes('/'+route));
    await page.getByRole('heading',{name:title,exact:true}).first().waitFor();
    assert.equal(await page.getByLabel('Username',{exact:true}).count(),0);
    await mkdir(input.evidence,{recursive:true});
    await page.screenshot({path:path.join(input.evidence,'nas-'+file+'.png'),fullPage:true});
    console.log('PASS deployed '+file+' page');
  }
  stage = 'platform-logout';
  const csrf = randomBytes(32).toString('base64url');
  await post('/api/iam/v1/admin/session/csrf-tokens',{token:csrf},{'X-Auriga-CSRF-Bootstrap':'admin-session-v1'});
  await post('/api/iam/v1/logout',{}, {'X-Auriga-CSRF':csrf});
  const denied = await page.goto(relay+'/ai/ui/');
  assert.equal(denied.status(),401);
  await page.screenshot({path:path.join(input.evidence,'nas-logged-out.png')});
  console.log('PASS deployed platform logout invalidates retained LiteLLM cookies');
} catch (error) {
  // No response dumps, request bodies, URLs, token values or credential details.
  console.error('FAIL deployed stage='+stage+' type='+error.constructor.name);
  process.exitCode=1;
} finally {
  await context?.close();
  await browser?.close();
}
