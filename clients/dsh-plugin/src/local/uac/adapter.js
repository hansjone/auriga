import { createHash } from 'node:crypto';
import { canonicalize, parseStrictJson } from '../../network/trust/jcs.js';
import {
  createUacHttp,
  MAX_BODY,
  parseIpAddress,
  uacTransportReason,
} from './http.js';
import { resolveCookieDomain } from './browser-cookies.js';
import { memberName, memberAvatar } from '../member-profile.js';

/*
 * Local port of the platform `UacConfiguredAdapter` (services/iam): the same
 * private adapter.json drives both sides. Only synthetic protocol mappings live
 * in this public repository; real hosts, system codes and field mappings are
 * read from the protected local enterprise configuration.
 */

const RESERVED = new Set([
  'x',
  'y',
  'systemCode',
  'token',
  'empNo',
  'clientIP',
  'purpose',
]);
const VARIABLE = /\$\{([A-Za-z][A-Za-z0-9]{0,63})\}/g;

const invalid = () => new Error('invalid UAC configuration');

/** @param {unknown} value */
const isPointer = (value) =>
  typeof value === 'string' &&
  (value === '' || value.startsWith('/')) &&
  value.length <= 512 &&
  !/~(?![01])/.test(value);

/** @param {unknown} value */
const isScanStatus = (value) =>
  typeof value === 'string' && value.length <= 128 && !!value.trim();

/** @param {any} qr @param {any} department */
function validateMappings(qr, department) {
  const scanPointers = [qr?.statusPointer, qr?.tokenPointer, qr?.empNoPointer];
  const states = [qr?.success];
  for (const pending of ['waiting', 'scanned'])
    if (qr && Object.hasOwn(qr, pending)) states.push(qr[pending]);
  const departmentPointers = department?.departmentPointers;
  if (
    !scanPointers.every(isPointer) ||
    new Set(scanPointers).size !== scanPointers.length ||
    !states.every(isScanStatus) ||
    new Set(states.map((value) => value.trim())).size !== states.length ||
    ![
      department?.successPointer,
      department?.listPointer,
      department?.empNoPointer,
    ].every(isPointer) ||
    !Array.isArray(departmentPointers) ||
    !departmentPointers.length ||
    !departmentPointers.every(isPointer) ||
    !Object.hasOwn(qr, 'body') ||
    !Object.hasOwn(department, 'body') ||
    !Object.hasOwn(department, 'successValue')
  )
    throw invalid();
  for (const field of ['namePointers', 'avatarPointers'])
    if (
      Object.hasOwn(department, field) &&
      (!Array.isArray(department[field]) || !department[field].every(isPointer))
    )
      throw invalid();
  canonicalize(department.successValue);
}

/** RFC 6901 pointer lookup; undefined when absent.
 * @param {any} root @param {unknown} pointer */
export function at(root, pointer) {
  if (typeof pointer !== 'string' || !isPointer(pointer)) return undefined;
  if (pointer === '') return root;
  let node = root;
  for (const raw of pointer.slice(1).split('/')) {
    const key = raw.replaceAll('~1', '/').replaceAll('~0', '~');
    if (
      node === null ||
      typeof node !== 'object' ||
      (Array.isArray(node) && !/^(?:0|[1-9][0-9]*)$/.test(key)) ||
      !Object.hasOwn(node, key)
    )
      return undefined;
    node = node[key];
  }
  return node;
}

/** @param {string} value */
function literalIp(value) {
  if (value === '') return;
  if (!parseIpAddress(value)) throw invalid();
}

/** @param {string} name */
function privateVariable(name) {
  if (!/^[A-Za-z][A-Za-z0-9]{0,63}$/.test(name) || RESERVED.has(name))
    throw invalid();
}

/** @param {string} template @param {Record<string,string>} values */
function render(template, values) {
  if (template.length > 48 * 1024) throw invalid();
  const output = template.replace(VARIABLE, (_all, name) => {
    if (typeof values[name] !== 'string') throw invalid();
    return values[name];
  });
  if (output.length > 48 * 1024 || output.includes('${')) throw invalid();
  return output;
}

/** @param {any} template @param {Record<string,string>} values @returns {any} */
function expand(template, values) {
  if (typeof template === 'string' && template.includes('${'))
    return render(template, values);
  if (Array.isArray(template)) return template.map((v) => expand(v, values));
  if (template && typeof template === 'object')
    return Object.fromEntries(
      Object.entries(template).map(([k, v]) => [k, expand(v, values)]),
    );
  return template;
}

/** @param {string} value */
function stripDepartmentText(value) {
  let start = 0;
  let end = value.length;
  while (start < end && value.charCodeAt(start) <= 0x20) start++;
  while (end > start && value.charCodeAt(end - 1) <= 0x20) end--;
  return value.slice(start, end);
}
// Same fixed set as IAM's UacTokenVerification: Unicode White_Space plus BOM
// and the legacy Java separators, so formerly blank tokens stay invalid.
const uacWhitespace = new Set([
  0x0085, 0x00a0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff,
]);
/** @param {number} code */
function isWhitespace(code) {
  return (
    (code >= 0x0009 && code <= 0x000d) ||
    (code >= 0x001c && code <= 0x0020) ||
    (code >= 0x2000 && code <= 0x200a) ||
    uacWhitespace.has(code)
  );
}
/** @param {string} value */
function stripWhitespace(value) {
  let start = 0;
  let end = value.length;
  while (start < end && isWhitespace(value.charCodeAt(start))) start++;
  while (end > start && isWhitespace(value.charCodeAt(end - 1))) end--;
  return value.slice(start, end);
}
/** ASCII-only case fold, matching the platform identity comparison. @param {string} value */
export const fold = (value) =>
  stripWhitespace(value).replace(/[A-Z]/g, (c) => c.toLowerCase());
const validUser = (/** @type {unknown} */ v) => {
  if (typeof v !== 'string') return false;
  const user = stripWhitespace(v);
  return user.length > 0 && user.length <= 128;
};
const validToken = (/** @type {unknown} */ v) =>
  typeof v === 'string' && v.length <= 16384 && stripWhitespace(v).length > 0;

/** Reject duplicate fields and malformed upstream payloads before interpreting status.
 * @param {Buffer} body */
function parse(body) {
  return parseStrictJson(body, {
    allowFiniteNumbers: true,
    maxBytes: MAX_BODY,
    maxArray: 8192,
    maxNodes: 16384,
  });
}

/**
 * @param {any} settings adapter.json contents (private)
 * @param {{baseUrl:string,systemCode:string}} provider private provider binding
 * @param {{http?:ReturnType<typeof createUacHttp>,lookup?:any}} [options]
 */
export function createUacAdapter(settings, provider, options = {}) {
  if (!settings || typeof settings !== 'object' || Array.isArray(settings))
    throw invalid();
  if (settings.clientIP !== undefined && typeof settings.clientIP !== 'string')
    throw invalid();
  const clientIP = settings.clientIP ?? '';
  literalIp(clientIP);
  const timeout = settings.qr?.timeoutSeconds;
  if (
    timeout !== undefined &&
    (!Number.isInteger(timeout) || timeout < 1 || timeout > 15)
  )
    throw invalid();
  const scanTimeout = timeout ?? 15;
  const http =
    options.http ??
    createUacHttp(settings, {
      lookup: options.lookup,
    });

  /** @param {Record<string,string>} input @param {any} operation */
  function values(input, operation) {
    /** @type {Record<string,string>} */
    const out = { ...input, systemCode: provider.systemCode };
    if (out.clientIP === undefined) out.clientIP = clientIP;
    if (settings.constants !== undefined) {
      if (
        !settings.constants ||
        typeof settings.constants !== 'object' ||
        Array.isArray(settings.constants) ||
        Object.keys(settings.constants).length > 32
      )
        throw invalid();
      for (const [name, value] of Object.entries(settings.constants)) {
        privateVariable(name);
        if (typeof value !== 'string' || value.length > 16384) throw invalid();
        out[name] = value;
      }
    }
    if (operation?.derived !== undefined) {
      const derived = operation.derived;
      if (
        !derived ||
        typeof derived !== 'object' ||
        Array.isArray(derived) ||
        Object.keys(derived).length > 16
      )
        throw invalid();
      for (const [name, definition] of Object.entries(derived)) {
        privateVariable(name);
        if (
          typeof definition?.template !== 'string' ||
          !['md5', 'sha256'].includes(definition.algorithm)
        )
          throw invalid();
        out[name] = createHash(definition.algorithm)
          .update(render(definition.template, out), 'utf8')
          .digest('hex');
      }
    }
    return out;
  }

  /** @param {any} mapping @param {Record<string,string>} vals */
  function headers(mapping, vals) {
    /** @type {Record<string,string>} */ const out = {};
    for (const [key, value] of Object.entries(mapping?.headers ?? {})) {
      if (key.toLowerCase() === 'host') throw invalid();
      const rendered = expand(value, vals);
      if (typeof rendered !== 'string') throw invalid();
      out[key] = rendered;
    }
    return out;
  }

  /** @param {{x:string,y:string}} coordinates */
  function qrContent(coordinates) {
    let template = settings.qr?.contentTemplate;
    if (typeof template !== 'string') throw invalid();
    for (const [name, value] of Object.entries(
      values(coordinates, settings.qr),
    ))
      template = template.replaceAll('${' + name + '}', formEncode(value));
    if (!template.trim() || template.length > 2048 || template.includes('${'))
      throw invalid();
    return template;
  }

  /** Same structural validation as the platform before enabling. */
  function validate() {
    if (
      typeof provider?.baseUrl !== 'string' ||
      typeof provider.systemCode !== 'string' ||
      !provider.systemCode.trim() ||
      provider.systemCode.length > 128
    )
      throw invalid();
    for (const [value, base] of /** @type {[unknown,boolean][]} */ ([
      [provider.baseUrl, true],
      [settings.tokenVerifyUrl, false],
      [settings.department?.url, false],
    ])) {
      if (typeof value !== 'string' || !value.trim()) throw invalid();
      http.validate(new URL(value), base);
    }
    if (typeof settings.qr?.path !== 'string' || !settings.qr.path.trim())
      throw invalid();
    validateMappings(settings.qr, settings.department);
    http.validate(new URL(provider.baseUrl + settings.qr.path));
    const coordinates = { x: '0'.repeat(32), y: '0'.repeat(32) };
    const scanValues = values(coordinates, settings.qr);
    expand(settings.qr.body, scanValues);
    headers(settings.qr, scanValues);
    qrContent(coordinates);
    const departmentValues = values(
      {
        token: 'validation',
        empNo: 'validation',
        clientIP: '',
        purpose: 'login_info',
      },
      settings.department,
    );
    expand(settings.department.body, departmentValues);
    headers(settings.department, departmentValues);
  }

  /** Bind owner identity to the service endpoints, system code and protocol
   * mapping used by the local adapter. Platform metadata and transport tuning
   * do not affect the binding. Derived fields evaluate in configured order. */
  function identityDomain() {
    const { qr, department, constants, clientIP } = settings;
    /** @param {any} mapping */
    const requestFields = (mapping) => ({
      body: mapping.body,
      ...(mapping.headers === undefined ? {} : { headers: mapping.headers }),
      derived: Object.entries(mapping.derived ?? {}).map(([name, value]) => {
        const { algorithm, template } = /** @type {any} */ (value);
        return [name, { algorithm, template }];
      }),
    });
    return createHash('sha256')
      .update(
        canonicalize(
          {
            version: 1,
            baseUrl: new URL(provider.baseUrl).href,
            systemCode: provider.systemCode,
            protocol: {
              ...(constants === undefined ? {} : { constants }),
              ...(clientIP === undefined ? {} : { clientIP }),
              tokenVerifyUrl: new URL(settings.tokenVerifyUrl).href,
              qr: {
                url: new URL(provider.baseUrl + qr.path).href,
                ...requestFields(qr),
                contentTemplate: qr.contentTemplate,
                statusPointer: qr.statusPointer,
                success: qr.success,
                ...(qr.waiting === undefined ? {} : { waiting: qr.waiting }),
                ...(qr.scanned === undefined ? {} : { scanned: qr.scanned }),
                tokenPointer: qr.tokenPointer,
                empNoPointer: qr.empNoPointer,
              },
              department: {
                url: new URL(department.url).href,
                ...requestFields(department),
                successPointer: department.successPointer,
                successValue: department.successValue,
                listPointer: department.listPointer,
                empNoPointer: department.empNoPointer,
                departmentPointers: department.departmentPointers,
              },
            },
          },
          { maxBytes: 131072, maxDepth: 24 },
        ),
      )
      .digest('hex');
  }

  /** @param {string} state @param {string} code */
  const failure = (state, code) => ({ state, failureCode: code });

  /** One upstream scan poll for the given public coordinates.
   * @param {{x:string,y:string}} coordinates */
  async function scan(coordinates) {
    const mapping = settings.qr;
    try {
      const vals = values(coordinates, mapping);
      const response = await http.post(
        new URL(provider.baseUrl + mapping.path),
        expand(mapping.body, vals),
        headers(mapping, vals),
        scanTimeout,
      );
      if (response.status < 200 || response.status >= 300)
        return failure('unavailable', 'transport_error');
      let root;
      try {
        root = parse(response.body);
      } catch {
        return failure('failed', 'invalid_response');
      }
      const state = at(root, mapping.statusPointer);
      if (!isScanStatus(state)) return failure('failed', 'invalid_scan_state');
      for (const pending of ['waiting', 'scanned'])
        if (state === mapping[pending]) return { state: pending };
      // The platform-shaped mapping treats every other explicit state as denial.
      if (state !== mapping.success)
        return failure('denied', 'upstream_rejected');
      const token = at(root, mapping.tokenPointer),
        empNo = at(root, mapping.empNoPointer);
      if (!validToken(token)) return failure('failed', 'missing_token');
      if (!validUser(empNo)) return failure('failed', 'missing_emp_no');
      return {
        state: 'verified',
        token: /** @type {string} */ (token),
        empNo: stripWhitespace(/** @type {string} */ (empNo)),
        clientIP,
      };
    } catch (error) {
      return /** @type {any} */ (error)?.message === 'invalid UAC configuration'
        ? failure('failed', 'invalid_response')
        : failure('unavailable', uacTransportReason(error));
    }
  }

  /** Fixed token-verify request/decision, identical to the platform.
   * @param {{empNo:string,token:string,clientIP?:string}} credential */
  async function verify(credential) {
    if (!validUser(credential.empNo) || !validToken(credential.token))
      return { success: false, failureClass: 'business_rejected' };
    const account = stripWhitespace(credential.empNo);
    const ip = credential.clientIP ?? '';
    const body = {
      account,
      token: credential.token,
      clientIP: ip,
      systemCode: provider.systemCode,
      verifyCode: createHash('md5')
        .update(account + credential.token + ip + provider.systemCode, 'utf8')
        .digest('hex'),
    };
    let response;
    try {
      response = await http.post(
        new URL(settings.tokenVerifyUrl),
        body,
        { 'X-Emp-No': account },
        3,
      );
    } catch (error) {
      return fault(uacTransportReason(error));
    }
    if (response.status !== 200) return fault('http_error');
    let root;
    try {
      root = parse(response.body);
    } catch {
      return fault('invalid_json');
    }
    if (!root || typeof root !== 'object' || Array.isArray(root))
      return fault('invalid_json');
    const outer = root.code?.code,
      inner = root.bo?.code;
    if (outer === undefined || inner === undefined)
      return fault('missing_code');
    if (
      typeof outer !== 'string' ||
      !outer.trim() ||
      typeof inner !== 'string' ||
      !inner.trim()
    )
      return fault('invalid_code');
    if (outer !== '0000' || inner !== '0000')
      return { success: false, failureClass: 'business_rejected' };
    // Only an absent field permits the legacy token location. Use a validated
    // echo's spelling for a new binding; the session keeps its existing owner.
    const other = root.other;
    if (
      other !== undefined &&
      (!other || typeof other !== 'object' || Array.isArray(other))
    )
      return fault('invalid_success_payload');
    const user = root.bo?.user,
      token = other?.token === undefined ? root.bo?.token : other.token;
    if ((user !== undefined && !validUser(user)) || !validToken(token))
      return fault('invalid_success_payload');
    if (user !== undefined && fold(user) !== fold(account))
      return {
        success: false,
        failureClass: 'identity_mismatch',
        identityMatched: false,
      };
    return {
      success: true,
      identityMatched: true,
      token: /** @type {string} */ (token),
      empNo: stripWhitespace(user === undefined ? account : user),
    };
  }

  /** Department strategy used only for `department` revalidation.
   * @param {{empNo:string,token:string,clientIP?:string}} credential @param {'login_info'|'revalidate'} purpose */
  async function department(credential, purpose) {
    const mapping = settings.department;
    try {
      const vals = values(
        {
          token: credential.token,
          empNo: credential.empNo,
          clientIP: credential.clientIP ?? clientIP,
          purpose,
        },
        mapping,
      );
      const response = await http.post(
        new URL(mapping.url),
        expand(mapping.body, vals),
        headers(mapping, vals),
        3,
      );
      if (response.status < 200 || response.status >= 300)
        return { available: false, faultReason: 'http_error' };
      let root;
      try {
        root = parse(response.body);
      } catch {
        return { available: false, faultReason: 'invalid_json' };
      }
      const state = at(root, mapping.successPointer);
      if (
        state === undefined ||
        JSON.stringify(state) !== JSON.stringify(mapping.successValue)
      )
        return { available: false, failureClass: 'business_rejected' };
      const list = at(root, mapping.listPointer);
      if (!Array.isArray(list))
        return { available: false, faultReason: 'invalid_success_payload' };
      const matches = list.filter((entry) => {
        const id = at(entry, mapping.empNoPointer);
        return typeof id === 'string' && fold(id) === fold(credential.empNo);
      });
      if (matches.length !== 1)
        return {
          available: false,
          failureClass: 'identity_mismatch',
          matchingCount: matches.length,
        };
      const displayName = (
        mapping.namePointers ?? [
          '/empName',
          '/employeeName',
          '/userName',
          '/name',
          '/empNameCn',
          '/employeeNameCn',
          '/userNameCn',
          '/chineseName',
          '/cnName',
          '/lastName',
        ]
      )
        .map((/** @type {string} */ pointer) =>
          memberName(at(matches[0], pointer)),
        )
        .find(Boolean);
      const avatarUrl = (
        mapping.avatarPointers ?? [
          '/headIcon',
          '/avatar',
          '/avatarUrl',
          '/photoUrl',
        ]
      )
        .map((/** @type {string} */ pointer) =>
          memberAvatar(at(matches[0], pointer)),
        )
        .find(Boolean);
      for (const pointer of mapping.departmentPointers ?? []) {
        const value = at(matches[0], pointer);
        if (
          typeof value === 'string' &&
          stripDepartmentText(value) &&
          value.length <= 1024
        )
          return {
            available: true,
            department: stripDepartmentText(value),
            ...(displayName ? { displayName } : {}),
            ...(avatarUrl ? { avatarUrl } : {}),
          };
      }
      return { available: false, faultReason: 'missing_department' };
    } catch (error) {
      return { available: false, faultReason: uacTransportReason(error) };
    }
  }
  /** Extract one cookie field per UacConfiguredAdapter rules (optional urlEncoded + JSON pointer).
   * @param {any} spec @param {Map<string,string>} cookieMap */
  function readCookieField(spec, cookieMap) {
    const name = typeof spec?.cookie === 'string' ? spec.cookie.trim() : '';
    if (!name) return undefined;
    let raw = cookieMap.get(name);
    if (typeof raw !== 'string' || !raw) return undefined;
    if (spec.urlEncoded) {
      try {
        raw = decodeURIComponent(raw);
      } catch {
        return undefined;
      }
    }
    if (typeof spec.pointer === 'string' && spec.pointer !== '') {
      try {
        const parsed = parseStrictJson(Buffer.from(raw, 'utf8'), {
          maxBytes: 16384,
          maxDepth: 16,
        });
        const found = at(parsed, spec.pointer);
        return typeof found === 'string' ? found : undefined;
      } catch {
        return undefined;
      }
    }
    return raw;
  }

  /** Return cookie names and domain suffix for silent local browser SSO, or null if unconfigured. */
  function ssoCookieSpec() {
    const cookies = settings.cookies;
    if (!cookies || typeof cookies !== 'object' || Array.isArray(cookies))
      return null;
    const names = [];
    for (const pair of [cookies.portal, cookies.uds]) {
      if (!pair || typeof pair !== 'object') continue;
      for (const key of ['token', 'account', 'clientIP']) {
        const name = pair[key]?.cookie;
        if (typeof name === 'string' && name.trim()) names.push(name.trim());
      }
    }
    if (!names.length) return null;
    const domain = resolveCookieDomain(cookies.domain, provider.baseUrl);
    if (!domain) return null;
    return { domain, names: [...new Set(names)] };
  }

  /** Extract a complete (empNo, token, clientIP) credential pair from local browser cookies.
   * Portal pair takes precedence over UDS pair; mixing pairs is forbidden.
   * @param {Map<string,string>} cookieMap */
  function extractSsoCredentials(cookieMap) {
    const cookies = settings.cookies;
    if (!cookies || typeof cookies !== 'object' || !(cookieMap instanceof Map))
      return null;
    for (const [kind, pair] of /** @type {const} */ ([
      ['portal', cookies.portal],
      ['uds', cookies.uds],
    ])) {
      if (!pair || typeof pair !== 'object') continue;
      const account = readCookieField(pair.account, cookieMap);
      const token = readCookieField(pair.token, cookieMap);
      if (validUser(account) && validToken(token)) {
        let ip = clientIP;
        if (kind === 'uds' && pair.clientIP) {
          const candidateIp = readCookieField(pair.clientIP, cookieMap);
          if (typeof candidateIp === 'string' && parseIpAddress(candidateIp))
            ip = candidateIp;
        }
        return {
          source: kind,
          empNo: stripWhitespace(/** @type {string} */ (account)),
          token: /** @type {string} */ (token),
          clientIP: ip,
        };
      }
    }
    return null;
  }

  return {
    validate,
    identityDomain,
    qrContent,
    scan,
    verify,
    department,
    ssoCookieSpec,
    extractSsoCredentials,
  };
}

/** java.net.URLEncoder (UTF-8) semantics: keep [A-Za-z0-9.*_-], space as '+'.
 * @param {string} value */
export function formEncode(value) {
  return encodeURIComponent(value)
    .replace(
      /[!'()~]/g,
      (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase(),
    )
    .replace(/%20/g, '+');
}

/** @param {string} reason */
function fault(reason) {
  return {
    success: false,
    failureClass: 'upstream_fault',
    faultReason: reason,
  };
}
