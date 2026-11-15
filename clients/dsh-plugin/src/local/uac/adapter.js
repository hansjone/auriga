import { createHash } from 'node:crypto';
import { canonicalize, parseStrictJson } from '../../network/trust/jcs.js';
import { createUacHttp, MAX_BODY, parseIpAddress } from './http.js';

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
function strip(value) {
  let start = 0;
  let end = value.length;
  while (start < end && value.charCodeAt(start) <= 0x20) start++;
  while (end > start && value.charCodeAt(end - 1) <= 0x20) end--;
  return value.slice(start, end);
}
/** ASCII-only case fold, matching the platform identity comparison. @param {string} value */
export const fold = (value) =>
  strip(value).replace(/[A-Z]/g, (c) => c.toLowerCase());
const validUser = (/** @type {unknown} */ v) =>
  typeof v === 'string' && strip(v).length > 0 && strip(v).length <= 128;
const validToken = (/** @type {unknown} */ v) =>
  typeof v === 'string' && v.trim().length > 0 && v.length <= 16384;

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
        empNo: strip(/** @type {string} */ (empNo)),
        clientIP,
      };
    } catch (error) {
      return /** @type {any} */ (error)?.message === 'invalid UAC configuration'
        ? failure('failed', 'invalid_response')
        : failure('unavailable', 'transport_error');
    }
  }

  /** Fixed token-verify request/decision, identical to the platform.
   * @param {{empNo:string,token:string,clientIP?:string}} credential */
  async function verify(credential) {
    const account = credential.empNo;
    if (!validUser(account) || !validToken(credential.token))
      return { success: false, failureClass: 'business_rejected' };
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
    } catch {
      return fault('transport_error');
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
    const user = root.bo?.user,
      token = root.bo?.token;
    if (!validUser(user) || !validToken(token))
      return fault('invalid_success_payload');
    if (fold(user) !== fold(account))
      return {
        success: false,
        failureClass: 'identity_mismatch',
        identityMatched: false,
      };
    return {
      success: true,
      identityMatched: true,
      token: /** @type {string} */ (token),
      empNo: strip(/** @type {string} */ (user)),
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
      for (const pointer of mapping.departmentPointers ?? []) {
        const value = at(matches[0], pointer);
        if (typeof value === 'string' && strip(value) && value.length <= 1024)
          return { available: true, department: strip(value) };
      }
      return { available: false, faultReason: 'missing_department' };
    } catch {
      return { available: false, faultReason: 'transport_error' };
    }
  }
  return { validate, identityDomain, qrContent, scan, verify, department };
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
