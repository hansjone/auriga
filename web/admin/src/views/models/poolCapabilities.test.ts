import { expect, it } from 'vitest';
import type { RelayPool } from '../../admin/relayApi';
import {
  applyCapabilityPolicy,
  capabilityOrigin,
  captureUpstreamCapabilities,
  createCapabilityPolicy,
  reconcileCapabilitySource,
} from './poolCapabilities';

const make = (): RelayPool => {
  const pool: RelayPool = {
    id: 'auto',
    enabled: true,
    contextWindow: 0,
    maxOutputTokens: 0,
    routes: [
      { providerId: 'provider', model: 'small-fallback', weight: 0 },
      { providerId: 'provider', model: 'large', weight: 1 },
    ],
    price: {
      currency: 'USD',
      version: 'v1',
      effectiveAt: '2026-01-01T00:00:00Z',
      inputMicrosPerMillionTokens: 1,
      outputMicrosPerMillionTokens: 1,
    },
  };
  pool.capabilities = createCapabilityPolicy(pool);
  return pool;
};
it('inherits 2M/128K from the default positive route without shrinking to a zero-weight fallback', () => {
  const pool = make();
  expect(pool.capabilities?.sourceModel).toBe('large');
  captureUpstreamCapabilities(pool, {
    id: 'large',
    contextWindow: 2000000,
    maxInputTokens: 1000000,
    maxOutputTokens: 128000,
  });
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([2000000, 128000]);
  expect(capabilityOrigin(pool, 'context')).toBe('upstream');
});
it('uses configured recommendations only for missing fields and drops fields removed by a successful refresh', () => {
  const pool = make();
  pool.capabilities!.recommendedContextWindow = 1000000;
  pool.capabilities!.recommendedMaxOutputTokens = 64000;
  captureUpstreamCapabilities(pool, {
    id: 'large',
    contextWindow: 2000000,
    maxOutputTokens: 128000,
  });
  captureUpstreamCapabilities(pool, { id: 'large', maxOutputTokens: 256000 });
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([1000000, 256000]);
  expect(capabilityOrigin(pool, 'context')).toBe('recommended');
  expect(pool.capabilities!.upstream?.model.contextWindow).toBeUndefined();
  delete pool.capabilities!.recommendedContextWindow;
  captureUpstreamCapabilities(pool, undefined);
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([0, 64000]);
  expect(capabilityOrigin(pool, 'context')).toBe('unknown');
});
it('keeps explicit old values and per-field overrides while upstream values change', () => {
  const pool = make();
  pool.contextWindow = 1000000;
  pool.maxOutputTokens = 4096;
  pool.capabilities = createCapabilityPolicy(pool, 'manual');
  captureUpstreamCapabilities(pool, {
    id: 'large',
    contextWindow: 2000000,
    maxOutputTokens: 128000,
  });
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([1000000, 4096]);
  pool.capabilities.maxOutputTokensMode = 'inherit';
  applyCapabilityPolicy(pool);
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([1000000, 128000]);
});
it('clears another model snapshot on an explicit source switch or source route removal', () => {
  const pool = make();
  captureUpstreamCapabilities(pool, {
    id: 'large',
    contextWindow: 2000000,
    maxOutputTokens: 128000,
  });
  pool.capabilities!.sourceModel = 'small-fallback';
  reconcileCapabilitySource(pool);
  expect(pool.capabilities!.upstream).toBeUndefined();
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([0, 0]);
  pool.routes[0].weight = 1;
  captureUpstreamCapabilities(pool, {
    id: 'small-fallback',
    contextWindow: 32768,
    maxOutputTokens: 4096,
  });
  pool.routes.splice(0, 1);
  reconcileCapabilitySource(pool);
  expect(pool.capabilities!.sourceModel).toBe('large');
  expect(pool.capabilities!.upstream).toBeUndefined();
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([0, 0]);
});
it('does not substitute an input limit for total context or invent a recommendation', () => {
  const pool = make();
  captureUpstreamCapabilities(pool, { id: 'large', maxInputTokens: 2000000 });
  expect([pool.contextWindow, pool.maxOutputTokens]).toEqual([0, 0]);
  expect(pool.capabilities!.upstream?.model.maxInputTokens).toBe(2000000);
});
