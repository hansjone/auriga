import type {
  ConnectorType,
  Provider,
  ProviderInput,
  ProviderPatch,
} from './providersApi';
export type JsonObject = Record<string, unknown>;
export const object = (value: unknown): value is JsonObject =>
  !!value && typeof value === 'object' && !Array.isArray(value);
export const secretSet = (value: unknown): boolean =>
  object(value) && value['x-secret-set'] === true;
const containsMask = (value: unknown): boolean =>
  secretSet(value) ||
  (Array.isArray(value)
    ? value.some(containsMask)
    : object(value) && Object.values(value).some(containsMask));
const composition = (schema: JsonObject) =>
  [
    '$ref',
    'allOf',
    'anyOf',
    'oneOf',
    'if',
    'then',
    'else',
    'dependentSchemas',
    'patternProperties',
  ].some((key) => key in schema);
export type ConfigField = {
  path: string[];
  schema: JsonObject;
  kind: 'string' | 'integer' | 'number' | 'boolean' | 'array';
  secret: boolean;
  set: boolean;
  present: boolean;
  configured: boolean;
  required: boolean;
  text: string;
  readonly: boolean;
};
export type ConfigForm = {
  fields: ConfigField[];
  unsupported: string[];
  createBlocked: boolean;
};
export function configForm(
  connector: ConnectorType | undefined,
  config: unknown,
  creating: boolean,
): ConfigForm {
  const result: ConfigForm = {
    fields: [],
    unsupported: [],
    createBlocked: false,
  };
  function unsupported(path: string[], required: boolean) {
    result.unsupported.push(path.join('.') || 'config');
    if (creating && required) result.createBlocked = true;
  }
  function walk(
    schema: unknown,
    value: unknown,
    path: string[],
    required: boolean,
    opaque = false,
  ) {
    if (
      path.some((key) =>
        ['__proto__', 'constructor', 'prototype'].includes(key),
      )
    ) {
      unsupported(path, required);
      return;
    }
    if (!object(schema) || composition(schema) || secretSet(schema)) {
      unsupported(path, required);
      return;
    }
    if (schema.type === 'object') {
      if (schema['x-secret'] === true || secretSet(value)) {
        unsupported(path, required);
        return;
      }
      if (!object(schema.properties)) {
        if (path.length || schema.additionalProperties !== false)
          unsupported(path, required);
        return;
      }
      const original = object(value) ? value : {};
      for (const [key, child] of Object.entries(schema.properties)) {
        walk(
          child,
          original[key],
          [...path, key],
          (required || value !== undefined) &&
            Array.isArray(schema.required) &&
            schema.required.includes(key),
          opaque || (path.length > 0 && containsMask(value)),
        );
      }
      for (const key of Object.keys(original))
        if (!(key in schema.properties)) unsupported([...path, key], false);
      return;
    }
    const array =
      schema.type === 'array' &&
      object(schema.items) &&
      schema.items.type === 'string' &&
      !composition(schema.items) &&
      schema.items['x-secret'] !== true;
    const scalar = ['string', 'integer', 'number', 'boolean'].includes(
      String(schema.type),
    );
    if (
      (!array && !scalar) ||
      (schema['x-secret'] === true && schema.type !== 'string')
    ) {
      unsupported(path, required);
      return;
    }
    const secret = schema['x-secret'] === true;
    const validOriginal =
      value === undefined ||
      secretSet(value) ||
      (array
        ? Array.isArray(value) && value.every((v) => typeof v === 'string')
        : typeof value ===
          (schema.type === 'integer' ? 'number' : schema.type));
    const numericUnsafe =
      typeof value === 'number' &&
      (!Number.isFinite(value) ||
        Math.abs(value) > Number.MAX_SAFE_INTEGER ||
        (schema.type === 'integer' && !Number.isSafeInteger(value)));
    const readonly =
      opaque ||
      !validOriginal ||
      numericUnsafe ||
      (array &&
        Array.isArray(value) &&
        value.some(
          (entry) => typeof entry === 'string' && /[\r\n]/.test(entry),
        )) ||
      (!secret && containsMask(value));
    if (readonly) unsupported(path, required);
    result.fields.push({
      path,
      schema,
      kind: array ? 'array' : (schema.type as ConfigField['kind']),
      secret,
      set: creating && required,
      present: value !== undefined,
      configured: secretSet(value),
      required,
      text:
        secret || readonly || value === undefined
          ? ''
          : Array.isArray(value)
            ? value.join('\n')
            : String(value),
      readonly,
    });
  }
  if (
    !connector ||
    !object(connector.descriptor.configSchema) ||
    connector.descriptor.configSchema.type !== 'object'
  ) {
    result.createBlocked = creating;
    result.unsupported.push('config');
  } else walk(connector.descriptor.configSchema, config, [], true);
  return result;
}
function fieldValue(field: ConfigField): unknown {
  if (field.kind === 'array')
    return field.text === '' ? [] : field.text.split(/\r?\n/);
  if (field.kind === 'boolean') return field.text === 'true';
  if (field.kind === 'integer' || field.kind === 'number')
    return Number(field.text);
  return field.text;
}
function valid(schema: JsonObject, value: unknown): boolean {
  if (typeof value === 'string') {
    if (
      typeof schema.minLength === 'number' &&
      Array.from(value).length < schema.minLength
    )
      return false;
    if (
      typeof schema.maxLength === 'number' &&
      Array.from(value).length > schema.maxLength
    )
      return false;
    if (typeof schema.pattern === 'string') {
      try {
        if (!new RegExp(schema.pattern, 'u').test(value)) return false;
      } catch {
        return false;
      }
    }
    if (schema.format === 'uri') {
      try {
        new URL(value);
      } catch {
        return false;
      }
    }
  }
  if (
    typeof value === 'number' &&
    (!Number.isFinite(value) ||
      Math.abs(value) > Number.MAX_SAFE_INTEGER ||
      (schema.type === 'integer' && !Number.isSafeInteger(value)) ||
      (typeof schema.minimum === 'number' && value < schema.minimum) ||
      (typeof schema.maximum === 'number' && value > schema.maximum))
  )
    return false;
  if (Array.isArray(schema.enum) && !schema.enum.includes(value)) return false;
  if (Array.isArray(value)) {
    if (typeof schema.minItems === 'number' && value.length < schema.minItems)
      return false;
    if (typeof schema.maxItems === 'number' && value.length > schema.maxItems)
      return false;
    if (schema.uniqueItems === true && new Set(value).size !== value.length)
      return false;
    if (
      object(schema.items) &&
      !value.every((entry) => valid(schema.items as JsonObject, entry))
    )
      return false;
  }
  return true;
}
export function configError(form: ConfigForm): string | undefined {
  if (form.createBlocked) return form.unsupported.join(', ');
  for (const field of form.fields) {
    if (!field.set || field.readonly) continue;
    if (
      (field.kind === 'integer' && !/^-?\d+$/.test(field.text)) ||
      (field.kind === 'number' && field.text.trim() === '') ||
      (field.kind === 'boolean' && !['true', 'false'].includes(field.text)) ||
      !valid(field.schema, fieldValue(field))
    )
      return field.path.join('.');
  }
  return undefined;
}
export function configChanges(
  form: ConfigForm,
  original: unknown,
): ProviderInput['config'] {
  const result: JsonObject = {};
  const source = object(original) ? original : {};
  for (const field of form.fields) {
    if (!field.set || field.readonly) continue;
    const [top, ...tail] = field.path;
    if (!top) continue;
    const value = fieldValue(field);
    if (!tail.length) {
      result[top] = value;
      continue;
    }
    // ProviderManager replaces one top-level config member, so preserve all nested siblings.
    if (!(top in result))
      result[top] = object(source[top])
        ? JSON.parse(JSON.stringify(source[top]))
        : {};
    let target = result[top] as JsonObject;
    tail.forEach((key, index) => {
      if (index === tail.length - 1) target[key] = value;
      else {
        if (!object(target[key])) target[key] = {};
        target = target[key] as JsonObject;
      }
    });
  }
  return result as ProviderInput['config'];
}
export type DisplayDraft = {
  nameEn: string;
  nameZh: string;
  buttonEn: string;
  buttonZh: string;
  icon: string;
  order: string;
};
export function displayDraft(provider?: Provider): DisplayDraft {
  return {
    nameEn: provider?.display.name.en ?? '',
    nameZh: provider?.display.name['zh-CN'] ?? '',
    buttonEn: provider?.display.buttonText?.en ?? '',
    buttonZh: provider?.display.buttonText?.['zh-CN'] ?? '',
    icon: provider?.display.icon ?? '',
    order: String(provider?.display.order ?? 100),
  };
}
export function displayValue(
  draft: DisplayDraft,
  original?: Provider['display'],
): Provider['display'] {
  const result: Provider['display'] = {
    ...original,
    name: { ...original?.name, en: draft.nameEn },
    order: Number(draft.order),
  };
  if (draft.nameZh || original?.name['zh-CN'] !== undefined)
    result.name['zh-CN'] = draft.nameZh;
  if (draft.buttonEn || draft.buttonZh || original?.buttonText)
    result.buttonText = {
      ...original?.buttonText,
      en: draft.buttonEn,
      ...(draft.buttonZh || original?.buttonText?.['zh-CN'] !== undefined
        ? { 'zh-CN': draft.buttonZh }
        : {}),
    };
  if (draft.icon || original?.icon !== undefined) result.icon = draft.icon;
  return result;
}
export function providerBody(
  form: ConfigForm,
  draft: DisplayDraft,
  enabled: boolean,
  connectorType: string,
  original?: Provider,
): ProviderInput | ProviderPatch {
  const display = displayValue(draft, original?.display);
  const config = configChanges(form, original?.config);
  if (!original) return { connectorType, display, config, enabled };
  return {
    ...(JSON.stringify(display) !== JSON.stringify(original.display)
      ? { display }
      : {}),
    ...(Object.keys(config).length ? { config } : {}),
    ...(enabled !== original.enabled ? { enabled } : {}),
  };
}
