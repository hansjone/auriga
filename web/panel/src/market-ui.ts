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
  title.append(element('h3', name), element('span', label, 'resource-kind'));
  header.append(icon, title);
  return header;
}
export function fieldList(fields: [string, string][]) {
  const list = element('dl', undefined, 'resource-fields');
  for (const [label, value] of fields)
    list.append(element('dt', label), element('dd', value));
  return list;
}
