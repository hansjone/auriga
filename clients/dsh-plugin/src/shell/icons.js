/**
 * Lucide-style SVG icons for Auriga Activity Bar / Settings.
 * Paths follow Lucide icon geometry (viewBox 0 0 24 24).
 */

const DEFAULT_SIZE = 20;
const STROKE = 1.8;

/**
 * @param {(type: any, props: any, ...children: any[]) => any} h
 * @param {any[]} children
 * @param {Record<string, unknown>} [props]
 */
export function lucideIcon(h, children, props = {}) {
  const { size = DEFAULT_SIZE, ...rest } = props;
  return h(
    'svg',
    {
      xmlns: 'http://www.w3.org/2000/svg',
      width: size,
      height: size,
      viewBox: '0 0 24 24',
      fill: 'none',
      stroke: 'currentColor',
      strokeWidth: STROKE,
      strokeLinecap: 'round',
      strokeLinejoin: 'round',
      'aria-hidden': true,
      focusable: 'false',
      ...rest,
    },
    ...children,
  );
}

/** @param {(type: any, props: any, ...children: any[]) => any} h */
export function createAurigaIcons(h) {
  const icon = (children, props) => lucideIcon(h, children, props);
  return {
    LayoutDashboard: (props) =>
      icon(
        [
          h('rect', { key: 'a', width: '7', height: '7', x: '3', y: '3', rx: '1' }),
          h('rect', { key: 'b', width: '7', height: '7', x: '14', y: '3', rx: '1' }),
          h('rect', { key: 'c', width: '7', height: '7', x: '14', y: '14', rx: '1' }),
          h('rect', { key: 'd', width: '7', height: '7', x: '3', y: '14', rx: '1' }),
        ],
        props,
      ),
    Store: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'm2 7 4.41-4.41A2 2 0 0 1 7.83 2h8.34a2 2 0 0 1 1.42.59L22 7',
          }),
          h('path', { key: 'b', d: 'M4 12v8a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-8' }),
          h('path', { key: 'c', d: 'M15 22v-4a2 2 0 0 0-2-2h-2a2 2 0 0 0-2 2v4' }),
          h('path', { key: 'd', d: 'M2 7h20' }),
          h('path', {
            key: 'e',
            d: 'M22 7v3a2 2 0 0 1-2 2 2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 16 12a2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 12 12a2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 8 12a2.7 2.7 0 0 1-1.59-.63.7.7 0 0 0-.82 0A2.7 2.7 0 0 1 4 12a2 2 0 0 1-2-2V7',
          }),
        ],
        props,
      ),
    LibraryBig: (props) =>
      icon(
        [
          h('rect', {
            key: 'a',
            width: '8',
            height: '18',
            x: '3',
            y: '3',
            rx: '1',
          }),
          h('path', { key: 'b', d: 'M7 3v18' }),
          h('path', {
            key: 'c',
            d: 'M20.4 18.9c.2.5-.1 1.1-.6 1.3l-1.9.7c-.5.2-1.1-.1-1.3-.6L11.1 5.1c-.2-.5.1-1.1.6-1.3l1.9-.7c.5-.2 1.1.1 1.3.6Z',
          }),
        ],
        props,
      ),
    UsersRound: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M18 21a8 8 0 0 0-16 0' }),
          h('circle', { key: 'b', cx: '10', cy: '8', r: '5' }),
          h('path', { key: 'c', d: 'M22 20c0-3.37-2-6.5-4-8a5 5 0 0 0-.45-8.3' }),
        ],
        props,
      ),
    Inbox: (props) =>
      icon(
        [
          h('polyline', { key: 'a', points: '22 12 16 12 14 15 10 15 8 12 2 12' }),
          h('path', {
            key: 'b',
            d: 'M5.45 5.11 2 12v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2v-6l-3.45-6.89A2 2 0 0 0 16.76 4H7.24a2 2 0 0 0-1.79 1.11z',
          }),
        ],
        props,
      ),
    Settings: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z',
          }),
          h('circle', { key: 'b', cx: '12', cy: '12', r: '3' }),
        ],
        props,
      ),
    ArrowUpRight: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M7 7h10v10' }),
          h('path', { key: 'b', d: 'M7 17 17 7' }),
        ],
        props,
      ),
    ChevronRight: (props) =>
      icon([h('path', { key: 'a', d: 'm9 18 6-6-6-6' })], props),
    PanelLeftOpen: (props) =>
      icon(
        [
          h('rect', {
            key: 'a',
            width: '18',
            height: '18',
            x: '3',
            y: '3',
            rx: '2',
          }),
          h('path', { key: 'b', d: 'M9 3v18' }),
          h('path', { key: 'c', d: 'm14 9 3 3-3 3' }),
        ],
        props,
      ),
    PanelLeftClose: (props) =>
      icon(
        [
          h('rect', {
            key: 'a',
            width: '18',
            height: '18',
            x: '3',
            y: '3',
            rx: '2',
          }),
          h('path', { key: 'b', d: 'M9 3v18' }),
          h('path', { key: 'c', d: 'm16 15-3-3 3-3' }),
        ],
        props,
      ),
    User: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2' }),
          h('circle', { key: 'b', cx: '12', cy: '7', r: '4' }),
        ],
        props,
      ),
    Link2: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M9 17H7A5 5 0 0 1 7 7h2',
          }),
          h('path', {
            key: 'b',
            d: 'M15 7h2a5 5 0 1 1 0 10h-2',
          }),
          h('line', { key: 'c', x1: '8', x2: '16', y1: '12', y2: '12' }),
        ],
        props,
      ),
    Globe: (props) =>
      icon(
        [
          h('circle', { key: 'a', cx: '12', cy: '12', r: '10' }),
          h('path', { key: 'b', d: 'M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20' }),
          h('path', { key: 'c', d: 'M2 12h20' }),
        ],
        props,
      ),
    Download: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4' }),
          h('polyline', { key: 'b', points: '7 10 12 15 17 10' }),
          h('line', { key: 'c', x1: '12', x2: '12', y1: '15', y2: '3' }),
        ],
        props,
      ),
    Radio: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M4.9 19.1C1 15.2 1 8.8 4.9 4.9' }),
          h('path', { key: 'b', d: 'M7.8 16.2c-2.3-2.3-2.3-6.1 0-8.5' }),
          h('circle', { key: 'c', cx: '12', cy: '12', r: '2' }),
          h('path', { key: 'd', d: 'M16.2 7.8c2.3 2.3 2.3 6.1 0 8.5' }),
          h('path', { key: 'e', d: 'M19.1 4.9C23 8.8 23 15.1 19.1 19' }),
        ],
        props,
      ),
    Shield: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z',
          }),
        ],
        props,
      ),
    Stethoscope: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M11 2v2' }),
          h('path', { key: 'b', d: 'M5 2v2' }),
          h('path', {
            key: 'c',
            d: 'M5 3H4a2 2 0 0 0-2 2v4a6 6 0 0 0 12 0V5a2 2 0 0 0-2-2h-1',
          }),
          h('path', { key: 'd', d: 'M8 15a6 6 0 0 0 12 0v-3' }),
          h('circle', { key: 'e', cx: '20', cy: '10', r: '2' }),
        ],
        props,
      ),
    RefreshCw: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8' }),
          h('path', { key: 'b', d: 'M21 3v5h-5' }),
          h('path', { key: 'c', d: 'M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16' }),
          h('path', { key: 'd', d: 'M8 16H3v5' }),
        ],
        props,
      ),
  };
}
