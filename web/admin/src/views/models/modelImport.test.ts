import { describe, expect, it } from 'vitest';
import {
  capabilityLimits,
  applyKnownLimits,
  suggestPublicId,
  rowError,
  samePool,
  type ImportRow,
  type CatalogModel,
} from './modelImport';

describe('model import limits and identity', () => {
  it('keeps unknown limits unknown and never infers from a model name', () => {
    expect(capabilityLimits({ id: 'gpt-long-1m' })).toEqual({
      context: undefined,
      output: undefined,
      inputOnly: false,
      limited: false,
    });
  });
  it('distinguishes input-only metadata without imposing platform ceilings', () => {
    expect(
      capabilityLimits({
        id: 'x',
        maxInputTokens: 2000000,
        maxOutputTokens: 64000,
      }),
    ).toEqual({
      context: 2000000,
      output: 64000,
      inputOnly: true,
      limited: false,
    });
    expect(
      capabilityLimits({
        id: 'x',
        contextWindow: 128000,
        maxInputTokens: 64000,
        maxOutputTokens: 8192,
      }).context,
    ).toBe(64000);
  });
  it('explicitly refreshes capabilities from complete route metadata, including larger values', () => {
    const models: CatalogModel[] = [
      { id: 'a', contextWindow: 64000, maxOutputTokens: 8192 },
      { id: 'b', contextWindow: 32000, maxOutputTokens: 2048 },
    ];
    expect(applyKnownLimits(16000, 1024, models)).toEqual({
      context: 32000,
      output: 2048,
    });
    expect(applyKnownLimits(64000, 8192, models)).toEqual({
      context: 32000,
      output: 2048,
    });
    expect(
      applyKnownLimits(64000, 8192, [models[0], { id: 'unknown' }]),
    ).toBeUndefined();
  });
  it('does not treat malformed metadata as known', () => {
    expect(
      capabilityLimits({ id: 'x', contextWindow: -1, maxOutputTokens: 1.5 })
        .context,
    ).toBeUndefined();
    expect(
      capabilityLimits({
        id: 'x',
        contextWindow: 0,
        maxOutputTokens: Number.MAX_SAFE_INTEGER + 1,
      }).output,
    ).toBeUndefined();
  });
  it('validates public ID collisions and limits before any write', () => {
    const row: ImportRow = {
      model: { id: 'source/name' },
      id: suggestPublicId('source/name'),
      context: 32000,
      output: 4096,
      key: 'key',
      status: 'pending',
      message: '',
    };
    expect(row.id).toBe('source-name');
    expect(rowError(row, [row], new Set())).toBeUndefined();
    expect(rowError(row, [row], new Set([row.id]))).toBe('importExists');
    expect(rowError(row, [row, { ...row }], new Set())).toBe('importDuplicate');
    expect(
      rowError({ ...row, context: undefined }, [row], new Set()),
    ).toBeUndefined();
    expect(rowError({ ...row, context: 4096 }, [row], new Set())).toBe(
      'importInvalidLimits',
    );
  });
  it('compares complete persisted configuration before recovering an ambiguous write', () => {
    const pool = {
      id: 'public',
      enabled: true,
      contextWindow: 32000,
      maxOutputTokens: 4096,
      routes: [{ providerId: 'p', model: 'real', weight: 1 }],
      price: {
        currency: 'USD' as const,
        inputMicrosPerMillionTokens: 0,
        outputMicrosPerMillionTokens: 0,
        version: 'v',
        effectiveAt: '2026-01-01T00:00:00Z',
      },
    };
    expect(samePool(pool, structuredClone(pool))).toBe(true);
    const canonical = {
      ...pool,
      price: { ...pool.price, effectiveAt: '2026-01-01T00:00:00.120Z' },
    };
    expect(
      samePool(canonical, {
        ...canonical,
        price: { ...canonical.price, effectiveAt: '2026-01-01T00:00:00.12Z' },
      }),
    ).toBe(true);
    expect(
      samePool(canonical, {
        ...canonical,
        price: {
          ...canonical.price,
          effectiveAt: '2026-01-01T09:00:00.120000000+09:00',
        },
      }),
    ).toBe(true);
    expect(
      samePool(canonical, {
        ...canonical,
        price: {
          ...canonical.price,
          effectiveAt: '2026-01-01T00:00:00.120000001Z',
        },
      }),
    ).toBe(false);
    expect(
      samePool(canonical, {
        ...canonical,
        price: { ...canonical.price, effectiveAt: '2026-01-01T00:00:00.121Z' },
      }),
    ).toBe(false);
    expect(
      samePool(canonical, {
        ...canonical,
        price: { ...canonical.price, effectiveAt: 'invalid' },
      }),
    ).toBe(false);
    expect(
      samePool(canonical, {
        ...canonical,
        price: { ...canonical.price, inputMicrosPerMillionTokens: 1 },
      }),
    ).toBe(false);
    expect(samePool(pool, { ...pool, enabled: false })).toBe(false);
    expect(
      samePool(pool, {
        ...pool,
        routes: [{ providerId: 'p', model: 'different', weight: 1 }],
      }),
    ).toBe(false);
    expect(
      samePool(pool, { ...pool, price: { ...pool.price, version: 'other' } }),
    ).toBe(false);
  });
});
