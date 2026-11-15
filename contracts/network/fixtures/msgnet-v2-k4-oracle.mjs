import { jcs, digest } from './msgnet-v2-oracle.mjs';
const fail = (code) => {
  throw Error(code);
};
export function checkReaderSelectors(i) {
  const ids = i.bundle.payload.required_reader_ids;
  if (jcs(Object.keys(i.expected_selectors).sort()) !== jcs([...ids].sort()))
    fail('invalid_chain');
  if (i.expected_identities) {
    if (jcs(Object.keys(i.expected_identities).sort()) !== jcs([...ids].sort()))
      fail('invalid_chain');
    for (const row of i.evidence.payload.readers) {
      if (
        ids.includes(row.reader_id) &&
        i.expected_identities[row.reader_id] !== row.identity_id
      )
        fail('invalid_chain');
    }
  }
  for (const row of i.evidence.payload.readers.filter(
    (row) => ids.includes(row.reader_id) && row.phase === 'isolated',
  )) {
    if (!row.selector) {
      if (i.expected_selectors[row.reader_id] !== null) fail('invalid_chain');
      if (i.expected_identities?.[row.reader_id] !== row.identity_id)
        fail('invalid_chain');
    } else {
      if (row.selector.installation_id !== row.identity_id)
        fail('invalid_chain');
      if (row.selector.bundle_digest !== digest(i.bundle))
        fail('invalid_chain');
      if (row.selector.bundle_version !== i.bundle.payload.bundle_version)
        fail('invalid_chain');
      if (i.expected_selectors[row.reader_id] !== digest(row.selector))
        fail('invalid_chain');
    }
  }
}
export function evaluateK4(v) {
  try {
    const i = v.input,
      r = i.response,
      b = i.bundle.payload,
      c = i.control.payload;
    if (!i.authenticated_c_ledger) fail('scope_denied');
    for (const field of ['tenant_id', 'realm']) {
      if (
        r[field] !== i[field] ||
        b[field] !== i[field] ||
        c[field] !== i[field] ||
        i.query[field] !== i[field]
      )
        fail('scope_denied');
    }
    for (const field of [
      'generation',
      'bundle_version',
      'phase',
      'authority_id',
      'authority_digest',
    ]) {
      if (r[field] !== b[field]) fail('invalid_chain');
    }
    if (
      r.bundle_digest !== digest(i.bundle) ||
      i.query.bundle_digest !== r.bundle_digest ||
      i.query.bundle_version !== r.bundle_version
    )
      fail('invalid_chain');
    if (
      r.control_revision !== c.control_revision ||
      i.query.control_revision !== c.control_revision ||
      r.control_digest !== digest(i.control) ||
      i.query.control_digest !== r.control_digest
    )
      fail('control_required');
    if (r.challenge !== i.query.challenge) fail('invalid_chain');
    if (
      Date.parse(r.issued_at) > Date.parse(i.now) ||
      Date.parse(i.now) - Date.parse(r.issued_at) > 60000
    )
      fail('projection_pending');
    const ids = [...b.required_reader_ids].sort();
    if (jcs([...r.required_reader_ids].sort()) !== jcs(ids))
      fail('invalid_chain');
    for (const map of [r.expected_selectors, r.reader_identities]) {
      if (jcs(Object.keys(map).sort()) !== jcs(ids)) fail('invalid_chain');
    }
    return 'accept';
  } catch (e) {
    return e.message;
  }
}
