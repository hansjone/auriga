/**
 * Auriga overview home modules.
 * Keep presentation modular so future surfaces can opt into each block.
 */
import { createAurigaIcons } from './icons.js';

/**
 * @param {any} React
 */
export function createOverviewUI(React) {
  const h = React.createElement;
  const icons = createAurigaIcons(h);

  function childList(children) {
    if (children == null || children === false) return [];
    return Array.isArray(children) ? children.flat(Infinity) : [children];
  }

  function OverviewWelcome({
    name,
    signedIn,
    title,
    tagline = '探索更多能力，连接无限可能。',
    avatar,
    'aria-label': ariaLabel = '欢迎',
  }) {
    const resolvedTitle =
      title || (signedIn ? '欢迎回来，' + (name || '成员') : '欢迎使用 Auriga');
    const initials = String(name || 'A')
      .trim()
      .slice(0, 1)
      .toUpperCase();
    return h(
      'section',
      { className: 'auriga-overview-welcome', 'aria-label': ariaLabel },
      h(
        'div',
        { className: 'auriga-overview-welcome-copy' },
        h('h1', { className: 'auriga-overview-welcome-title' }, resolvedTitle),
        h('p', { className: 'auriga-overview-welcome-tagline' }, tagline),
      ),
      avatar !== false
        ? h(
            'div',
            {
              className: 'auriga-overview-welcome-avatar',
              'aria-hidden': true,
            },
            avatar || initials,
          )
        : null,
    );
  }

  function OverviewStatusItem({ tone = 'idle', label, detail }) {
    return h(
      'div',
      { className: 'auriga-overview-status-item' },
      h('span', {
        className: 'auriga-overview-status-dot is-' + tone,
        'aria-hidden': true,
      }),
      h(
        'div',
        { className: 'auriga-overview-status-copy' },
        h('span', { className: 'auriga-overview-status-label' }, label),
        detail
          ? h('span', { className: 'auriga-overview-status-detail' }, detail)
          : null,
      ),
    );
  }

  function OverviewStatus({ items, alert, actions }) {
    const list = Array.isArray(items) ? items.filter(Boolean) : [];
    if (!list.length && !alert && !actions) return null;
    return h(
      'section',
      { className: 'auriga-overview-status', 'aria-label': '运行状态' },
      list.length
        ? h(
            'div',
            { className: 'auriga-overview-status-row' },
            ...list.map((item, index) =>
              h(OverviewStatusItem, { key: item.id || index, ...item }),
            ),
          )
        : null,
      alert
        ? h(
            'div',
            {
              className: 'auriga-overview-status-alert',
              role: 'status',
            },
            h('p', null, alert),
            actions
              ? h(
                  'div',
                  { className: 'auriga-overview-status-actions' },
                  ...childList(actions),
                )
              : null,
          )
        : actions
          ? h(
              'div',
              { className: 'auriga-overview-status-actions' },
              ...childList(actions),
            )
          : null,
    );
  }

  function OverviewNotifications({ items }) {
    const list = Array.isArray(items) ? items.filter(Boolean) : [];
    if (!list.length) return null;
    return h(
      'section',
      { className: 'auriga-overview-notifications', 'aria-label': '重要通知' },
      ...list.map((item) =>
        h(
          'div',
          {
            key: item.id,
            className: 'auriga-overview-notice',
            role: item.role || 'status',
          },
          h(
            'div',
            { className: 'auriga-overview-notice-copy' },
            item.title ? h('strong', null, item.title) : null,
            item.detail ? h('span', null, item.detail) : null,
          ),
          item.action
            ? h(
                'div',
                { className: 'auriga-overview-notice-action' },
                item.action,
              )
            : null,
        ),
      ),
    );
  }

  function OverviewCapabilityCard({
    icon,
    title,
    description,
    onClick,
    'aria-label': ariaLabel,
  }) {
    const Icon = icon ? icons[icon] : null;
    const Arrow = icons.ArrowUpRight;
    return h(
      'button',
      {
        type: 'button',
        className: 'auriga-overview-capability',
        'aria-label': ariaLabel || title,
        onClick,
      },
      h(
        'div',
        { className: 'auriga-overview-capability-top' },
        Icon
          ? h(Icon, {
              size: 20,
              className: 'auriga-overview-capability-icon',
            })
          : null,
        h(Arrow, {
          size: 16,
          className: 'auriga-overview-capability-arrow',
        }),
      ),
      h('div', { className: 'auriga-overview-capability-title' }, title),
      h('p', { className: 'auriga-overview-capability-desc' }, description),
    );
  }

  function OverviewCapabilities({ title = '探索 Auriga', items, onNavigate }) {
    const list = Array.isArray(items) ? items.filter(Boolean) : [];
    if (!list.length) return null;
    return h(
      'section',
      { className: 'auriga-overview-capabilities', 'aria-label': title },
      h('h2', { className: 'auriga-overview-section-title' }, title),
      h(
        'div',
        { className: 'auriga-overview-capability-grid' },
        ...list.map((item) =>
          h(OverviewCapabilityCard, {
            key: item.id,
            icon: item.icon,
            title: item.title,
            description: item.description,
            onClick: () => onNavigate?.(item.route || item.id),
          }),
        ),
      ),
    );
  }

  /**
   * Compact activity feed. Hide when there is no real activity payload.
   * @param {{title?:string,items?:any[],onMore?:()=>void,moreLabel?:string,onOpen?:(item:any)=>void}} props
   */
  function OverviewActivity({
    title = '生态动态',
    items,
    onMore,
    moreLabel = '查看更多',
    onOpen,
  }) {
    const list = Array.isArray(items) ? items.filter(Boolean) : [];
    if (!list.length) return null;
    return h(
      'section',
      { className: 'auriga-overview-activity', 'aria-label': title },
      h(
        'div',
        { className: 'auriga-overview-activity-head' },
        h('h2', { className: 'auriga-overview-section-title' }, title),
        onMore
          ? h(
              'button',
              {
                type: 'button',
                className: 'auriga-overview-more',
                onClick: onMore,
              },
              moreLabel,
            )
          : null,
      ),
      h(
        'ul',
        { className: 'auriga-overview-activity-list' },
        ...list.slice(0, 5).map((item) =>
          h(
            'li',
            { key: item.id },
            h(
              'button',
              {
                type: 'button',
                className: 'auriga-overview-activity-row',
                onClick: () => onOpen?.(item),
              },
              h(
                'span',
                {
                  className:
                    'auriga-overview-activity-icon is-' +
                    (item.kind || 'plugin'),
                  'aria-hidden': true,
                },
                item.kind === 'skill' ? 'S' : 'P',
              ),
              h(
                'div',
                { className: 'auriga-overview-activity-copy' },
                h(
                  'div',
                  { className: 'auriga-overview-activity-title' },
                  item.title,
                ),
                h(
                  'div',
                  { className: 'auriga-overview-activity-meta' },
                  [item.type, item.version, item.time]
                    .filter(Boolean)
                    .join(' · '),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  function OverviewPage({ children }) {
    return h(
      'div',
      { className: 'auriga-overview' },
      h('div', { className: 'auriga-overview-inner' }, ...childList(children)),
    );
  }

  return {
    OverviewPage,
    OverviewWelcome,
    OverviewStatus,
    OverviewStatusItem,
    OverviewNotifications,
    OverviewCapabilities,
    OverviewCapabilityCard,
    OverviewActivity,
    icons,
  };
}

/** Capability entries currently shipped in the Auriga shell. */
export const OVERVIEW_CAPABILITIES = [
  {
    id: 'market',
    route: 'market',
    icon: 'Store',
    title: '资源市场',
    description: '发现插件与技能，拓展 AI 能力。',
  },
  {
    id: 'mine',
    route: 'mine',
    icon: 'LibraryBig',
    title: '我的资源',
    description: '管理已安装的插件与技能。',
  },
  {
    id: 'team',
    route: 'team',
    icon: 'UsersRound',
    title: 'Team 工具',
    description: '连接团队能力，协同完成工作。',
  },
  {
    id: 'inbox',
    route: 'inbox',
    icon: 'Inbox',
    title: '收件箱',
    description: '查看成员消息与请求。',
  },
];
