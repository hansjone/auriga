import type { LoginProvider } from './api/iam';
import { navigationProviderId } from './api/correlation';

/** Server-rendered deployment preference, never a query or local-storage choice. */
export function loginPreference(doc: Document): 'company' | 'team' {
  const tags = doc.querySelectorAll('meta[name="auriga-login-preference"]');
  return tags.length === 1 && tags[0].getAttribute('content') === 'team'
    ? 'team'
    : 'company';
}

export function preferredLoginProvider(
  providers: LoginProvider[],
  doc: Document,
): LoginProvider | undefined {
  if (loginPreference(doc) === 'team') {
    const team = providers.find((p) => p.connectorType === 'team-account');
    if (team) return team;
  }
  const bound = providers.find(
    (p) => p.connectorType === 'uac' && p.id === navigationProviderId(doc),
  );
  return (
    (doc.getElementById('auriga-uac-navigation') ? bound : undefined) ??
    providers.find((p) => p.connectorType === 'uac')
  );
}

export function orderLoginProviders(providers: LoginProvider[], doc: Document) {
  const preferred = loginPreference(doc) === 'team' ? 'team-account' : 'uac';
  const rank = (p: LoginProvider) =>
    p.connectorType === preferred ? 0 : p.connectorType === 'emergency' ? 2 : 1;
  return providers
    .slice()
    .sort((a, b) => rank(a) - rank(b) || a.order - b.order);
}
