export function redactDiagnostics(value, secrets = []) {
  if (typeof value === 'string') {
    for (const secret of secrets
      .filter((v) => typeof v === 'string' && v.length)
      .sort((a, b) => b.length - a.length))
      value = value.replaceAll(secret, '[redacted]');
    return value
      .replace(
        /-----BEGIN [^-]*PRIVATE KEY-----[\s\S]*?-----END [^-]*PRIVATE KEY-----/g,
        '[redacted private key]',
      )
      .replace(/\bBearer\s+[^\s]+/gi, 'Bearer [redacted]')
      .replace(
        /\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g,
        '[redacted token]',
      );
  }
  if (Array.isArray(value))
    return value.map((v) => redactDiagnostics(v, secrets));
  if (value && typeof value === 'object')
    return Object.fromEntries(
      Object.entries(value).map(([k, v]) => [k, redactDiagnostics(v, secrets)]),
    );
  return value;
}

function summary(value) {
  if (value === null || ['number', 'boolean'].includes(typeof value))
    return value;
  if (typeof value === 'bigint')
    return { type: 'bigint', decimal: value.toString() };
  if (typeof value === 'string' || Array.isArray(value))
    return {
      type: typeof value === 'string' ? 'string' : 'array',
      length: value.length,
    };
  return { type: typeof value };
}

export function describeBusinessFailure(error, phase, secrets = []) {
  const assertion = error.name === 'AssertionError';
  const label =
    assertion && error.generatedMessage
      ? `${phase}: ${error.operator} assertion failed`
      : String(error.message).split('\n')[0];
  return redactDiagnostics(
    {
      stage: 'failed',
      name: error.name,
      code: error.code,
      phase,
      assertion: assertion ? label : `${phase}: ${error.name}`,
      message: assertion ? label : error.message,
      ...(assertion
        ? {
            operator: error.operator,
            actual: { meaning: 'observed value', value: summary(error.actual) },
            expected: {
              meaning: 'required value',
              value: summary(error.expected),
            },
          }
        : {}),
      // Keep code locations, never a generated deep diff containing arbitrary values.
      stack: String(error.stack ?? '')
        .split(/\r?\n/)
        .filter((line) => /^\s+at /.test(line) && /:\d+:\d+\)?$/.test(line))
        .slice(0, 12)
        .join('\n'),
    },
    secrets,
  );
}
