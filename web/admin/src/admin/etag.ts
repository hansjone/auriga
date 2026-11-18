export const strongEtag = (value: string | null): value is string =>
  value !== null && /^"[\x21\x23-\x7e\x80-\xff]*"$/.test(value);
