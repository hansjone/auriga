export interface PluginMarket {
  authorId: string;
  authorName?: string | null;
  authorDepartment?: string | null;
  publishedAt?: string | null;
  version: string;
  dshVersionRange: string;
  description: string;
  tags: string[];
  installationCount: number;
  starCount: number;
  starred: boolean;
}

export type StarResult = Pick<PluginMarket, 'starred' | 'starCount'>;
export type MarketSort = '' | 'publishedAt' | 'installationCount' | 'starCount';

interface MarketPackage {
  id: string;
  name: string;
  market?: PluginMarket | null;
}

export function validateStar(value: unknown): StarResult {
  const result = value as StarResult | null;
  if (
    typeof result?.starred !== 'boolean' ||
    !Number.isSafeInteger(result.starCount) ||
    result.starCount < 0
  )
    throw new Error('invalid');
  return { starred: result.starred, starCount: result.starCount };
}

export class PluginPaginationError extends Error {
  constructor() {
    super('Invalid plugin catalog pagination');
  }
}

export async function allPluginPackages<T extends { id: string }>(
  read: (cursor?: string) => Promise<{ packages: T[]; cursor?: string }>,
) {
  const packages: T[] = [];
  const seenIds = new Set<string>();
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  do {
    const next = await read(cursor);
    for (const pack of next.packages) {
      if (typeof pack.id !== 'string' || !pack.id || seenIds.has(pack.id))
        throw new PluginPaginationError();
      seenIds.add(pack.id);
      packages.push(pack);
    }
    cursor = next.cursor;
    if (cursor) {
      if (seenCursors.has(cursor) || seenCursors.size >= 999)
        throw new PluginPaginationError();
      seenCursors.add(cursor);
    }
  } while (cursor);
  return { packages, cursor: undefined };
}

export const marketAuthor = (market: PluginMarket) =>
  market.authorName?.trim() || undefined;
export const marketDepartment = (market: PluginMarket) =>
  market.authorDepartment?.trim() || undefined;
export const marketDate = (market: PluginMarket) => {
  const timestamp = Date.parse(market.publishedAt ?? '');
  return Number.isFinite(timestamp) ? timestamp : undefined;
};

export function marketOptions(packages: MarketPackage[], locale: string) {
  const authors = new Map<string, string | undefined>();
  const departments = new Set<string>();
  const tags = new Set<string>();
  for (const { market } of packages) {
    if (!market) continue;
    const author = marketAuthor(market);
    if (author || !authors.has(market.authorId))
      authors.set(market.authorId, author);
    const department = marketDepartment(market);
    if (department) departments.add(department);
    market.tags.forEach((tag) => tags.add(tag));
  }
  const alphabetic = (a: string, b: string) => a.localeCompare(b, locale);
  return {
    authors: [...authors].sort(([, a], [, b]) => alphabetic(a ?? '', b ?? '')),
    departments: [...departments].sort(alphabetic),
    tags: [...tags].sort(alphabetic),
  };
}

export function filterPlugins<T extends MarketPackage>(
  packages: T[],
  filters: {
    search: string;
    author: string;
    department: string;
    tag: string;
    sort: MarketSort;
  },
  legacyDescription: (pack: T) => string | undefined = () => undefined,
) {
  const { author, department, tag, sort } = packages.some((pack) => pack.market)
    ? filters
    : { author: '', department: '', tag: '', sort: '' as const };
  const query = filters.search.trim().toLowerCase();
  const result = packages.filter((pack) => {
    const market = pack.market;
    return (
      (!query ||
        [
          pack.name,
          market?.description ?? legacyDescription(pack),
          ...(market?.tags ?? []),
        ].some((text) => text?.toLowerCase().includes(query))) &&
      (!author || market?.authorId === author) &&
      (!department || (market && marketDepartment(market) === department)) &&
      (!tag || market?.tags.includes(tag))
    );
  });
  if (!sort) return result;
  const score = (pack: T) =>
    !pack.market
      ? -Infinity
      : sort === 'publishedAt'
        ? (marketDate(pack.market) ?? -Infinity)
        : pack.market[sort];
  return result.sort((a, b) => {
    const left = score(a);
    const right = score(b);
    return left === right ? 0 : right < left ? -1 : 1;
  });
}
