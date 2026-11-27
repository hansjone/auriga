export function element<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text?: string,
  className?: string,
) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
}

/** Lucide outline icons (ISC), inlined to avoid a package dependency in the panel. */
type LucideNode =
  | { tag: 'path'; d: string }
  | { tag: 'circle'; cx: number; cy: number; r: number };

const lucideNodes: Record<string, LucideNode[]> = {
  'refresh-cw': [
    { tag: 'path', d: 'M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8' },
    { tag: 'path', d: 'M21 3v5h-5' },
    { tag: 'path', d: 'M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16' },
    { tag: 'path', d: 'M8 16H3v5' },
  ],
  search: [
    { tag: 'circle', cx: 11, cy: 11, r: 8 },
    { tag: 'path', d: 'm21 21-4.3-4.3' },
  ],
  download: [
    { tag: 'path', d: 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4' },
    { tag: 'path', d: 'M7 10l5 5 5-5' },
    { tag: 'path', d: 'M12 15V3' },
  ],
  heart: [
    {
      tag: 'path',
      d: 'M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7Z',
    },
  ],
  'chevron-down': [{ tag: 'path', d: 'm6 9 6 6 6-6' }],
  'chevron-left': [{ tag: 'path', d: 'm15 18-6-6 6-6' }],
  'chevron-right': [{ tag: 'path', d: 'm9 18 6-6-6-6' }],
  check: [{ tag: 'path', d: 'M20 6 9 17l-5-5' }],
};

/** Compact page list with ellipsis, e.g. [1, '…', 4, 5, 6, '…', 12]. */
export function pageWindow(
  page: number,
  totalPages: number,
): Array<number | '…'> {
  if (totalPages <= 7)
    return Array.from({ length: totalPages }, (_, i) => i + 1);
  const pages = new Set<number>([1, totalPages, page]);
  for (let i = page - 1; i <= page + 1; i++)
    if (i > 1 && i < totalPages) pages.add(i);
  const sorted = [...pages].sort((a, b) => a - b);
  const result: Array<number | '…'> = [];
  for (let i = 0; i < sorted.length; i++) {
    if (i > 0 && sorted[i] - sorted[i - 1] > 1) result.push('…');
    result.push(sorted[i]);
  }
  return result;
}

export type LucideName = keyof typeof lucideNodes;

export function lucideIcon(
  name: LucideName,
  size = 16,
  options?: { filled?: boolean },
) {
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('width', String(size));
  svg.setAttribute('height', String(size));
  svg.setAttribute('fill', options?.filled ? 'currentColor' : 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '2');
  svg.setAttribute('stroke-linecap', 'round');
  svg.setAttribute('stroke-linejoin', 'round');
  svg.setAttribute('aria-hidden', 'true');
  for (const node of lucideNodes[name]) {
    if (node.tag === 'circle') {
      const circle = document.createElementNS(svg.namespaceURI, 'circle');
      circle.setAttribute('cx', String(node.cx));
      circle.setAttribute('cy', String(node.cy));
      circle.setAttribute('r', String(node.r));
      svg.append(circle);
      continue;
    }
    const path = document.createElementNS(svg.namespaceURI, 'path');
    path.setAttribute('d', node.d);
    svg.append(path);
  }
  return svg;
}

export function refreshCwIcon(size = 16) {
  return lucideIcon('refresh-cw', size);
}

export function resourceHeading(
  name: string,
  kind: 'plugin' | 'skill',
  label: string,
) {
  const header = element('header', undefined, 'resource-heading');
  const icon = element('span', undefined, 'resource-icon');
  icon.setAttribute('aria-hidden', 'true');
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.5');
  const path = document.createElementNS(svg.namespaceURI, 'path');
  path.setAttribute(
    'd',
    kind === 'skill'
      ? 'M4 4h6a3 3 0 0 1 3 3v14a4 4 0 0 0-4-2H4V4Zm9 3a3 3 0 0 1 3-3h4v15h-3a4 4 0 0 0-4 2M7 8h3M7 11h3'
      : 'M9 4H4v5a3 3 0 1 1 0 6v5h5a3 3 0 1 1 6 0h5v-5a3 3 0 1 1 0-6V4h-5a3 3 0 1 0-6 0Z',
  );
  svg.append(path);
  icon.append(svg);
  const title = element('div');
  title.append(
    element('h3', name),
    element('span', label, 'resource-kind'),
  );
  header.append(icon, title);
  return header;
}
export function fieldList(fields: [string, string][]) {
  const list = element('dl', undefined, 'resource-fields');
  for (const [label, value] of fields)
    list.append(element('dt', label), element('dd', value));
  return list;
}
