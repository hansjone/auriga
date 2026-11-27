/**
 * Lucide-style icons for settings navigation (16px default via props.size).
 * Kept in-module so /auriga/settings/*.js can load without cross-tree imports.
 */

const DEFAULT_SIZE = 16;
const STROKE = 1.8;

/**
 * @param {(type: any, props: any, ...children: any[]) => any} h
 * @param {any[]} children
 * @param {Record<string, unknown>} [props]
 */
export function lucideIcon(h, children, props = {}) {
  const { size = DEFAULT_SIZE, className, ...rest } = props;
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
      className,
      ...rest,
    },
    ...children,
  );
}

/** @param {(type: any, props: any, ...children: any[]) => any} h */
export function createSettingsIcons(h) {
  const icon = (children, props) => lucideIcon(h, children, props);
  return {
    User: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2',
          }),
          h('circle', { key: 'b', cx: '12', cy: '7', r: '4' }),
        ],
        props,
      ),
    Link2: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M9 17H7A5 5 0 0 1 7 7h2' }),
          h('path', { key: 'b', d: 'M15 7h2a5 5 0 1 1 0 10h-2' }),
          h('line', { key: 'c', x1: '8', x2: '16', y1: '12', y2: '12' }),
        ],
        props,
      ),
    Globe: (props) =>
      icon(
        [
          h('circle', { key: 'a', cx: '12', cy: '12', r: '10' }),
          h('path', {
            key: 'b',
            d: 'M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20',
          }),
          h('path', { key: 'c', d: 'M2 12h20' }),
        ],
        props,
      ),
    Download: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4',
          }),
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
          h('path', {
            key: 'a',
            d: 'M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8',
          }),
          h('path', { key: 'b', d: 'M21 3v5h-5' }),
          h('path', {
            key: 'c',
            d: 'M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16',
          }),
          h('path', { key: 'd', d: 'M8 16H3v5' }),
        ],
        props,
      ),
    Monitor: (props) =>
      icon(
        [
          h('rect', {
            key: 'a',
            width: '20',
            height: '14',
            x: '2',
            y: '3',
            rx: '2',
          }),
          h('line', { key: 'b', x1: '8', x2: '16', y1: '21', y2: '21' }),
          h('line', { key: 'c', x1: '12', x2: '12', y1: '17', y2: '21' }),
        ],
        props,
      ),
    Cloud: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M17.5 19H9a7 7 0 1 1 6.71-9h1.79a4.5 4.5 0 1 1 0 9Z',
          }),
        ],
        props,
      ),
    Activity: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2',
          }),
        ],
        props,
      ),
    Plus: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M5 12h14' }),
          h('path', { key: 'b', d: 'M12 5v14' }),
        ],
        props,
      ),
    Settings2: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M20 7h-9',
          }),
          h('path', { key: 'b', d: 'M14 17H5' }),
          h('path', { key: 'c', d: 'M20 17h-2' }),
          h('path', { key: 'd', d: 'M8 7H5' }),
          h('circle', { key: 'e', cx: '17', cy: '17', r: '3' }),
          h('circle', { key: 'f', cx: '11', cy: '7', r: '3' }),
        ],
        props,
      ),
    Package: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M11 21.73a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73z',
          }),
          h('path', { key: 'b', d: 'M12 22V12' }),
          h('polyline', { key: 'c', points: '3.29 7 12 12 20.71 7' }),
          h('path', { key: 'd', d: 'm7.5 4.27 9 5.15' }),
        ],
        props,
      ),
    MessagesSquare: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M16 3H6a3 3 0 0 0-3 3v8a3 3 0 0 0 3 3h2l3 3v-3h5a3 3 0 0 0 3-3V6a3 3 0 0 0-3-3z',
          }),
          h('path', {
            key: 'b',
            d: 'M8 10h.01',
          }),
          h('path', { key: 'c', d: 'M12 10h.01' }),
          h('path', { key: 'd', d: 'M16 10h.01' }),
        ],
        props,
      ),
    MessageSquare: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z',
          }),
        ],
        props,
      ),
    Copy: (props) =>
      icon(
        [
          h('rect', {
            key: 'a',
            width: '14',
            height: '14',
            x: '8',
            y: '8',
            rx: '2',
            ry: '2',
          }),
          h('path', {
            key: 'b',
            d: 'M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2',
          }),
        ],
        props,
      ),
    X: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'M18 6 6 18' }),
          h('path', { key: 'b', d: 'm6 6 12 12' }),
        ],
        props,
      ),
    Check: (props) =>
      icon([h('path', { key: 'a', d: 'M20 6 9 17l-5-5' })], props),
    ShieldCheck: (props) =>
      icon(
        [
          h('path', {
            key: 'a',
            d: 'M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z',
          }),
          h('path', { key: 'b', d: 'm9 12 2 2 4-4' }),
        ],
        props,
      ),
    ListChecks: (props) =>
      icon(
        [
          h('path', { key: 'a', d: 'm3 17 2 2 4-4' }),
          h('path', { key: 'b', d: 'm3 7 2 2 4-4' }),
          h('path', { key: 'c', d: 'M13 6h8' }),
          h('path', { key: 'd', d: 'M13 12h8' }),
          h('path', { key: 'e', d: 'M13 18h8' }),
        ],
        props,
      ),
    /**
     * Official Auriga DSH mark (same geometry as shell sidebar brand).
     * @param {{ size?: number, className?: string }} [props]
     */
    AurigaMark: (props = {}) => {
      const { size = 48, className, ...rest } = props;
      return h(
        'svg',
        {
          width: size,
          height: size,
          viewBox: '0 0 64 64',
          fill: 'none',
          'aria-hidden': true,
          focusable: 'false',
          className,
          ...rest,
        },
        h('rect', { width: 64, height: 64, rx: 16, fill: '#10283b' }),
        h('path', {
          d: 'M32 12 51.97 26.51 44.34 49.99H19.66L12.03 26.51Z',
          stroke: '#8fb9d6',
          strokeWidth: 3,
          strokeLinejoin: 'round',
        }),
        h('path', {
          d: 'M23.5 44 32 23l8.5 21M26.5 37h11',
          stroke: '#e8f0f7',
          strokeWidth: 3,
          strokeLinecap: 'round',
          strokeLinejoin: 'round',
        }),
        ...[
          [32, 12],
          [51.97, 26.51],
          [44.34, 49.99],
          [19.66, 49.99],
          [12.03, 26.51],
        ].map(([cx, cy]) =>
          h('circle', { key: String(cx) + ',' + String(cy), cx, cy, r: 3, fill: '#8fb9d6' }),
        ),
      );
    },
  };
}
