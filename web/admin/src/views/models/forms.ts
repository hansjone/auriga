import type {
  ProviderInput,
  ModelInput,
  ModelGrants,
} from '../../admin/modelProvidersApi';
export const providerNumbers = [
  ['connectTimeoutSeconds', 120],
  ['firstByteTimeoutSeconds', 300],
  ['idleTimeoutSeconds', 300],
  ['totalTimeoutSeconds', 3600],
  ['maxConcurrency', 10000],
] as const;
export const modelNumbers = ['contextWindow', 'maxOutputTokens'] as const;
export const priceNumbers = [
  'inputMicrosPerMillionTokens',
  'outputMicrosPerMillionTokens',
] as const;
export const uuid =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const integer = (n: number, min = 1, max = Number.MAX_SAFE_INTEGER) =>
  Number.isSafeInteger(n) && n >= min && n <= max;
export function providerDraft(): ProviderInput {
  return {
    name: '',
    kind: 'openai_compatible',
    baseUrl: '',
    enabled: true,
    connectTimeoutSeconds: 10,
    firstByteTimeoutSeconds: 60,
    idleTimeoutSeconds: 60,
    totalTimeoutSeconds: 300,
    maxConcurrency: 10,
  };
}
export function modelDraft(providerId: string): ModelInput {
  return {
    id: '',
    providerId,
    upstreamModel: '',
    displayName: '',
    enabled: true,
    contextWindow: 8192,
    maxOutputTokens: 1024,
    price: {
      currency: 'USD',
      inputMicrosPerMillionTokens: 0,
      outputMicrosPerMillionTokens: 0,
      version: '',
      effectiveAt: '',
    },
  };
}
export function validProvider(p: ProviderInput) {
  try {
    const url = new URL(p.baseUrl);
    if (
      !['https:', 'http:'].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.hash
    )
      return false;
  } catch {
    return false;
  }
  return (
    !!p.name.trim() &&
    p.name.length <= 128 &&
    providerNumbers.every(([k, max]) => integer(p[k], 1, max))
  );
}
export function validModel(m: ModelInput) {
  return (
    /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(m.id) &&
    uuid.test(m.providerId) &&
    !!m.upstreamModel.trim() &&
    m.upstreamModel.length <= 128 &&
    modelNumbers.every((k) => integer(m[k])) &&
    priceNumbers.every((k) => integer(m.price[k], 0)) &&
    /^[A-Z]{3}$/.test(m.price.currency) &&
    !!m.price.version.trim() &&
    m.price.version.length <= 128 &&
    /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?(?:Z|[+-]\d\d:\d\d)$/.test(
      m.price.effectiveAt,
    ) &&
    Number.isFinite(Date.parse(m.price.effectiveAt))
  );
}
export function parseGrants(text: string): ModelGrants | undefined {
  const items: ModelGrants['items'] = [];
  for (const line of text
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)) {
    const [kind, id, extra] = line.split(/\s+/);
    if (
      (kind !== 'member' && kind !== 'group' && kind !== 'role') ||
      !id ||
      !uuid.test(id) ||
      extra
    )
      return;
    if (items.some((g) => g.kind === kind && g.id === id)) return;
    items.push({ kind, id });
  }
  return items.length <= 1000 ? { items } : undefined;
}

// Copy only contract input fields; never forward read-only/unknown response data.
export function providerInput(
  p: import('../../admin/modelProvidersApi').Provider,
): ProviderInput {
  return {
    name: p.name,
    kind: p.kind,
    baseUrl: p.baseUrl,
    enabled: p.enabled,
    connectTimeoutSeconds: p.connectTimeoutSeconds,
    firstByteTimeoutSeconds: p.firstByteTimeoutSeconds,
    idleTimeoutSeconds: p.idleTimeoutSeconds,
    totalTimeoutSeconds: p.totalTimeoutSeconds,
    maxConcurrency: p.maxConcurrency,
  };
}
export function modelInput(
  m: import('../../admin/modelProvidersApi').Model,
): ModelInput {
  return {
    id: m.id,
    providerId: m.providerId,
    upstreamModel: m.upstreamModel,
    displayName: m.displayName,
    enabled: m.enabled,
    contextWindow: m.contextWindow,
    maxOutputTokens: m.maxOutputTokens,
    price: {
      currency: m.price.currency,
      inputMicrosPerMillionTokens: m.price.inputMicrosPerMillionTokens,
      outputMicrosPerMillionTokens: m.price.outputMicrosPerMillionTokens,
      version: m.price.version,
      effectiveAt: m.price.effectiveAt,
    },
  };
}
