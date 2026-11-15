// Public runtime projection injected into the portal HTML by deployment.
// Absence keeps the existing instance entry enabled.
export function readPlacementConfig(doc: Document = document) {
  function content(name: string) {
    const tags = doc.querySelectorAll(`meta[name="${name}"]`);
    return tags.length === 1 ? tags[0].getAttribute('content') : null;
  }
  const parent = content('auriga-instance-parent-domain');
  const validParent =
    parent &&
    parent.length <= 253 &&
    parent.split('.').length >= 2 &&
    parent
      .split('.')
      .every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label));
  return {
    sharedPlacementEnabled:
      content('auriga-shared-placement-enabled') === 'true',
    instanceParentDomain: validParent ? parent : undefined,
  };
}
