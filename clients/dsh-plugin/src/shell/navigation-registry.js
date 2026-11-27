/**
 * Config-driven Auriga navigation registry.
 * Layout shells consume this; new surfaces register entries instead of editing chrome.
 *
 * @typedef {'top' | 'bottom'} AurigaNavPlacement
 * @typedef {{
 *   id: string,
 *   labelKey: string,
 *   shortLabelKey?: string,
 *   icon: string,
 *   route: string,
 *   group?: string,
 *   order?: number,
 *   placement: AurigaNavPlacement,
 *   visible?: boolean | ((ctx: object) => boolean),
 *   disabled?: boolean | ((ctx: object) => boolean),
 *   badge?: number | string | ((ctx: object) => number | string | undefined),
 *   permission?: string,
 * }} AurigaNavigationEntry
 */

/** @type {AurigaNavigationEntry[]} */
export const AURIGA_NAVIGATION_ENTRIES = [
  {
    id: 'overview',
    labelKey: 'tab_overview',
    shortLabelKey: 'tab_overview',
    icon: 'LayoutDashboard',
    route: 'overview',
    group: 'workspace',
    order: 10,
    placement: 'top',
  },
  {
    id: 'market',
    labelKey: 'tab_market',
    shortLabelKey: 'tab_market_short',
    icon: 'Store',
    route: 'market',
    group: 'workspace',
    order: 20,
    placement: 'top',
  },
  {
    id: 'mine',
    labelKey: 'tab_mine',
    shortLabelKey: 'tab_mine_short',
    icon: 'LibraryBig',
    route: 'mine',
    group: 'workspace',
    order: 30,
    placement: 'top',
  },
  {
    id: 'team',
    labelKey: 'tab_team',
    shortLabelKey: 'tab_team_short',
    icon: 'UsersRound',
    route: 'team',
    group: 'workspace',
    order: 40,
    placement: 'top',
  },
  {
    id: 'inbox',
    labelKey: 'tab_inbox',
    shortLabelKey: 'tab_inbox',
    icon: 'Inbox',
    route: 'inbox',
    group: 'system',
    order: 10,
    placement: 'bottom',
  },
  {
    id: 'settings',
    labelKey: 'tab_settings',
    shortLabelKey: 'tab_settings',
    icon: 'Settings',
    route: 'settings',
    group: 'system',
    order: 20,
    placement: 'bottom',
  },
];

/**
 * @param {AurigaNavigationEntry[]} [seed]
 */
export function createAurigaNavigationRegistry(
  seed = AURIGA_NAVIGATION_ENTRIES,
) {
  /** @type {AurigaNavigationEntry[]} */
  const entries = seed.map((entry) => ({ ...entry }));

  const sortEntries = () =>
    entries.sort(
      (a, b) =>
        (a.order ?? 0) - (b.order ?? 0) ||
        String(a.id).localeCompare(String(b.id)),
    );

  sortEntries();

  return {
    /** @param {AurigaNavigationEntry} entry */
    register(entry) {
      if (!entry?.id || !entry.placement || !entry.route || !entry.icon) {
        throw new TypeError(
          'navigation entry requires id, placement, route, icon',
        );
      }
      const index = entries.findIndex((item) => item.id === entry.id);
      if (index >= 0) entries.splice(index, 1, { ...entry });
      else entries.push({ ...entry });
      sortEntries();
      return this;
    },
    /** @param {string} id */
    unregister(id) {
      const index = entries.findIndex((item) => item.id === id);
      if (index >= 0) entries.splice(index, 1);
      return this;
    },
    /** @param {string} id */
    get(id) {
      return entries.find((item) => item.id === id);
    },
    /**
     * @param {AurigaNavPlacement} placement
     * @param {{ can?: (permission: string) => boolean }} [ctx]
     */
    list(placement, ctx = {}) {
      return entries.filter((item) => {
        if (item.placement !== placement) return false;
        const visible = item.visible;
        if (visible === false) return false;
        if (typeof visible === 'function' && !visible(ctx)) return false;
        if (
          item.permission &&
          typeof ctx.can === 'function' &&
          !ctx.can(item.permission)
        )
          return false;
        return true;
      });
    },
    all() {
      return entries.slice();
    },
  };
}

export const AURIGA_ACTIVITY_BAR_STORAGE_KEY = 'auriga.activityBar.expanded';

export function readActivityBarExpanded(storage = globalThis.localStorage) {
  try {
    return storage?.getItem?.(AURIGA_ACTIVITY_BAR_STORAGE_KEY) === '1';
  } catch {
    return false;
  }
}

/** @param {boolean} expanded @param {{setItem?:(key:string,value:string)=>void}|null} [storage] */
export function writeActivityBarExpanded(
  expanded,
  storage = globalThis.localStorage,
) {
  try {
    storage?.setItem?.(AURIGA_ACTIVITY_BAR_STORAGE_KEY, expanded ? '1' : '0');
  } catch {
    /* private mode */
  }
}
