export async function readAllPages<T>(
  read: (
    cursor?: string,
    signal?: AbortSignal,
  ) => Promise<{ items: T[]; nextCursor?: string }>,
  signal?: AbortSignal,
) {
  const items: T[] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  do {
    signal?.throwIfAborted();
    const page = await read(cursor, signal);
    signal?.throwIfAborted();
    items.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor))
      throw new Error('Repeated pagination cursor');
    if (cursor) seen.add(cursor);
  } while (cursor);
  return items;
}
