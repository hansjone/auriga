// Test-only trusted DSH admission. Cookies and self-claimed headers do not bind.
export function ownerRequest(runtime, request = { headers: {} }) {
  runtime.bindNativeRequest({ principal: { kind: 'owner' } }, request);
  return request;
}
