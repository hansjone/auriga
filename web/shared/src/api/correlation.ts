export const UAC_CORRELATION_HEADER = 'X-Auriga-Correlation-Id';
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export function validCorrelationId(value: unknown): string | undefined {
  return typeof value === 'string' && uuid.test(value) ? value : undefined;
}
export function navigationCorrelationId(doc: Document): string | undefined {
  return validCorrelationId(
    doc
      .querySelector('meta[name="auriga-uac-correlation"]')
      ?.getAttribute('content'),
  );
}
import type { components } from '../generated/iam';
export function navigationProviderId(doc: Document): string | undefined {
  return validCorrelationId(
    doc
      .querySelector('meta[name="auriga-uac-provider"]')
      ?.getAttribute('content'),
  );
}
export function takeNavigationContext(
  doc: Document,
  providerId: string,
): components['schemas']['UacNavigationContext'] | undefined {
  const node = doc.getElementById('auriga-uac-navigation');
  if (!node) return undefined;
  node.remove();
  if (navigationProviderId(doc) !== providerId) return undefined;
  try {
    return JSON.parse(node.textContent ?? '');
  } catch {
    return undefined;
  }
}
