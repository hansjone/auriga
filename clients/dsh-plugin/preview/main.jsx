import React from 'react';
import { createRoot } from 'react-dom/client';
import { createSettingsView } from '../src/settings/view.js';
import { createSettingsUI } from '../src/settings/ui.js';
import { createAurigaIcons } from '../src/shell/icons.js';
import {
  createOverviewUI,
  OVERVIEW_CAPABILITIES,
} from '../src/shell/overview.js';
import {
  createAurigaNavigationRegistry,
  readActivityBarExpanded,
  writeActivityBarExpanded,
} from '../src/shell/navigation-registry.js';
import clientSource from '../src/client.js?raw';

function extractPluginCss(source) {
  const match = source.match(/const css = `([\s\S]*?)`;\s*\n/);
  if (!match) throw new Error('未能从 client.js 提取 CSS');
  return match[1];
}

const style = document.createElement('style');
style.textContent = extractPluginCss(clientSource);
document.head.appendChild(style);

const h = React.createElement;
const icons = createAurigaIcons(h);
const navRegistry = createAurigaNavigationRegistry();
const ui = createSettingsUI(React);
const overviewUI = createOverviewUI(React);

const labels = {
  tab_overview: '概览',
  tab_market: '资源市场',
  tab_market_short: '市场',
  tab_mine: '我的资源',
  tab_mine_short: '资源',
  tab_team: 'Team 工具',
  tab_team_short: '团队',
  tab_inbox: '收件箱',
  tab_settings: '设置',
  nav_expand: '展开导航',
  nav_collapse: '收起导航',
  nav_label: 'Auriga 导航',
};

let settingsState = {
  mode: 'connect',
  platformOrigin: 'https://auriga.example.com',
  activeOrigin: 'https://auriga.example.com',
  deployment: 'local',
  deviceName: '周同学的电脑',
  networkControl: { available: true, enabled: true },
  proxy: {
    mode: 'custom',
    globalProxy: 'http://127.0.0.1:7890',
    noProxy: ['localhost', '127.0.0.1', '*.corp.local'],
    rules: [
      {
        id: 'rule-corp',
        pattern: '*.corp.local',
        action: 'direct',
        proxy: '',
      },
      {
        id: 'rule-cdn',
        pattern: 'cdn.example.com',
        action: 'proxy',
        proxy: 'http://127.0.0.1:7890',
      },
    ],
    source: 'user',
  },
  desktop: {
    enabled: true,
    channel: 'stable',
    available: { version: '0.2.0' },
    downloading: false,
    required: false,
    restartRequired: false,
    confirmationRequired: false,
    phase: 'idle',
    busy: false,
    downloadedBytes: 0,
    totalBytes: 0,
  },
};

const account = {
  loggedIn: true,
  connected: true,
  account: {
    subject: 'zhou-001',
    displayName: '周同学',
    empNo: 'Z001',
  },
  uacLoginInfo: {
    status: 'available',
    empNo: 'Z001',
    department: '研发中心',
  },
  deviceName: '周同学的电脑',
  loginTime: '2026-11-27T08:00:00Z',
  csrf: 'preview-csrf',
  communication: 'ready',
  network: {
    connected: true,
    state: 'ready',
    address: 'nats://preview.local',
    received: 12,
  },
  connection: {
    endpointId: 'endpoint-preview-8f3a',
    received: 12,
  },
  models: { available: true },
  session: { available: true },
  capabilities: { consent: true },
};

const localUac = {
  available: true,
  state: 'authenticated',
  empNo: 'Z001',
  displayName: '周同学',
  department: '研发中心',
  loginAt: '2026-11-27T07:55:00Z',
  revalidation: 'ok',
  platformLink: 'matched',
  csrf: 'local-csrf',
};

let consentState = {
  policyRevision: 3,
  defaults: { conversation: 'ask_each', execution: 'same_org' },
  rules: [
    {
      id: 'rule-1',
      layer: 'conversation',
      scope: 'source_user',
      choice: 'allow_source',
      source: {
        tenant: 'example',
        realm: '1',
        node: '2',
        userId: 'alice',
        displayName: 'Alice Chen',
        empNo: 'A100',
        department: '平台组',
      },
    },
    {
      id: 'rule-2',
      layer: 'execution',
      scope: 'source_node',
      choice: 'block',
      source: {
        tenant: 'example',
        realm: '1',
        node: '9',
        userId: 'bob',
        displayName: 'Bob Li',
        empNo: 'B200',
        department: '运维组',
      },
    },
    {
      id: 'rule-3',
      layer: 'conversation',
      scope: 'source_user',
      choice: 'allow_source',
      source: {
        tenant: 'example',
        realm: '1',
        node: '4',
        userId: 'carol',
        displayName: 'Carol Wang',
        empNo: 'C300',
      },
    },
  ],
  organization: {
    available: true,
    revision: 'org-preview',
    conversation: 'allow',
    execution: 'allow',
  },
  pending: [
    {
      id: 'pending-1',
      layer: 'conversation',
      source: {
        displayName: 'Dana Zhou',
        empNo: 'D400',
        department: '产品组',
        userId: 'dana',
        realm: '1',
        node: '7',
      },
      requestedAt: '2026-11-27T16:20:00Z',
    },
    {
      id: 'pending-2',
      layer: 'execution',
      source: {
        displayName: 'Evan Wu',
        empNo: 'E500',
        userId: 'evan',
        realm: '1',
        node: '3',
      },
      requestedAt: '2026-11-27T17:05:00Z',
    },
  ],
};

const catalogPackages = [
  {
    id: 'netx-ops',
    name: 'NetX 运维助手',
    kind: 'plugin',
    version: '1.4.2',
    description: '告警、网元、纳管 CLI 与巡检技能包。',
    author: 'Auriga',
    department: '平台组',
    installs: 128,
    stars: 36,
    installed: true,
    enabled: true,
  },
  {
    id: 'topology',
    name: '拓扑分析',
    kind: 'plugin',
    version: '0.9.1',
    description: '核心眼抽取与邻接打标，支持多算法下沉。',
    author: 'Auriga',
    department: '平台组',
    installs: 86,
    stars: 22,
    installed: true,
    enabled: true,
    updateAvailable: true,
  },
  {
    id: 'biz-monitor',
    name: '业务监控',
    kind: 'skill',
    version: '0.3.0',
    description: '割接前后业务状态对比与影响面分析。',
    author: '运维工具组',
    department: '运维中心',
    installs: 41,
    stars: 12,
    installed: false,
  },
  {
    id: 'kb-writer',
    name: '知识库整理',
    kind: 'skill',
    version: '1.1.0',
    description: '将会话结论整理为可检索笔记草稿。',
    author: 'Carol Wang',
    department: '产品组',
    installs: 19,
    stars: 7,
    installed: false,
  },
  {
    id: 'cutover-checklist',
    name: '割接检查单',
    kind: 'skill',
    version: '0.2.4',
    description: '本地私有技能：按检查单核对割接前后动作。',
    author: '周同学',
    department: '研发中心',
    installs: 1,
    stars: 0,
    installed: true,
    enabled: true,
    private: true,
  },
];

function kindIcon(kind) {
  const d =
    kind === 'skill'
      ? 'M4 4h6a3 3 0 0 1 3 3v14a4 4 0 0 0-4-2H4V4Zm9 3a3 3 0 0 1 3-3h4v15h-3a4 4 0 0 0-4 2M7 8h3M7 11h3'
      : 'M9 4H4v5a3 3 0 1 1 0 6v5h5a3 3 0 1 1 6 0h5v-5a3 3 0 1 1 0-6V4h-5a3 3 0 1 0-6 0Z';
  return h(
    'span',
    { className: 'resource-icon', 'aria-hidden': true },
    h(
      'svg',
      {
        width: 20,
        height: 20,
        viewBox: '0 0 24 24',
        fill: 'none',
        stroke: 'currentColor',
        strokeWidth: 1.5,
      },
      h('path', { d }),
    ),
  );
}

function searchIcon() {
  return h(
    'span',
    { className: 'market-search-icon', 'aria-hidden': true },
    h(
      'svg',
      {
        width: 16,
        height: 16,
        viewBox: '0 0 24 24',
        fill: 'none',
        stroke: 'currentColor',
        strokeWidth: 2,
      },
      h('circle', { cx: 11, cy: 11, r: 8 }),
      h('path', { d: 'm21 21-4.3-4.3' }),
    ),
  );
}

function pathOf(url) {
  return String(url).replace(/^https?:\/\/[^/]+\/?/, '');
}

async function mockFetch(url, init = {}) {
  const path = pathOf(url);
  const method = (init.method || 'GET').toUpperCase();
  const body = init.body ? JSON.parse(String(init.body)) : undefined;

  if (path === 'auriga/settings/state') return Response.json(settingsState);
  if (path === 'auriga/shell/status') return Response.json(account);
  if (path === 'auriga/uac/status') return Response.json(localUac);
  if (path === 'auriga/shell/messaging/snapshot')
    return Response.json({
      sessions: [
        {
          local: '1',
          sessionId: '演示会话 A',
          title: '演示会话 A',
          enabled: true,
          receiveEnabled: true,
          pending: false,
          pendingEnabled: null,
          address: 'nats://preview.local/1',
        },
        {
          local: '2',
          sessionId: '演示会话 B',
          title: '演示会话 B',
          enabled: false,
          receiveEnabled: false,
          pending: false,
          pendingEnabled: null,
          address: 'nats://preview.local/2',
        },
        {
          local: '3',
          sessionId: '割接协同',
          title: '割接协同',
          enabled: true,
          receiveEnabled: true,
          pending: true,
          pendingEnabled: false,
          address: 'nats://preview.local/3',
        },
      ],
    });
  if (path === 'auriga/shell/consent/snapshot')
    return Response.json(consentState);

  if (method === 'POST') {
    if (path === 'auriga/settings/test')
      return Response.json({ reachable: true });
    if (path === 'auriga/settings/save') {
      settingsState = {
        ...settingsState,
        platformOrigin: body.platformOrigin,
        activeOrigin: body.platformOrigin,
        deployment: body.deployment ?? settingsState.deployment,
      };
      return Response.json(settingsState);
    }
    if (
      path === 'auriga/settings/proxy' ||
      path === 'auriga/settings/proxy/reset'
    ) {
      settingsState = {
        ...settingsState,
        proxy: {
          ...(path.endsWith('/reset')
            ? {
                mode: 'direct',
                globalProxy: '',
                noProxy: [],
                rules: [],
                source: 'default',
              }
            : { ...settingsState.proxy, ...body, source: 'user' }),
        },
      };
      return Response.json(settingsState);
    }
    if (path === 'auriga/settings/proxy/test')
      return Response.json({
        url: body?.url || '',
        action: body?.url?.includes('corp') ? 'direct' : 'proxy',
        proxy: body?.url?.includes('corp') ? '' : 'http://127.0.0.1:7890',
        matchedBy: body?.url?.includes('corp') ? 'rule' : 'global',
        reachable: true,
        status: 200,
        latencyMs: 42,
      });
    if (path === 'auriga/settings/desktop') {
      if (typeof body?.enabled === 'boolean') {
        settingsState = {
          ...settingsState,
          desktop: { ...settingsState.desktop, enabled: body.enabled },
        };
        return Response.json(settingsState.desktop);
      }
      if (body?.channel) {
        settingsState = {
          ...settingsState,
          desktop: {
            ...settingsState.desktop,
            channel: body.channel,
            available: null,
          },
        };
        return Response.json(settingsState.desktop);
      }
      if (body?.action === 'check') {
        settingsState = {
          ...settingsState,
          desktop: {
            ...settingsState.desktop,
            available: { version: '0.2.0' },
            phase: 'idle',
            busy: false,
            error: null,
          },
        };
        return Response.json(settingsState.desktop);
      }
      if (body?.action === 'update') {
        settingsState = {
          ...settingsState,
          desktop: {
            ...settingsState.desktop,
            downloading: true,
            busy: true,
            phase: 'downloading',
            downloadedBytes: 524288,
            totalBytes: 2097152,
          },
        };
        return Response.json({ queued: true });
      }
      if (body?.action === 'cancel') {
        settingsState = {
          ...settingsState,
          desktop: {
            ...settingsState.desktop,
            downloading: false,
            busy: false,
            phase: 'cancelled',
          },
        };
        return Response.json(settingsState.desktop);
      }
      return Response.json(settingsState.desktop);
    }
    if (path === 'auriga/settings/network-control') {
      settingsState = {
        ...settingsState,
        networkControl: {
          ...settingsState.networkControl,
          enabled: !!body.enabled,
        },
      };
      return Response.json({ receipt: { package_id: 'preview-receipt' } });
    }
    if (path === 'auriga/shell/consent/default') {
      consentState = {
        ...consentState,
        defaults: {
          ...consentState.defaults,
          [body.layer]: body.mode,
        },
        policyRevision: consentState.policyRevision + 1,
      };
      return Response.json({ ok: true });
    }
    if (path === 'auriga/shell/consent/revoke') {
      consentState = {
        ...consentState,
        rules: consentState.rules.filter((rule) => rule.id !== body.ruleId),
        policyRevision: consentState.policyRevision + 1,
      };
      return Response.json({ ok: true });
    }
    if (path === 'auriga/shell/consent/decide') {
      consentState = {
        ...consentState,
        pending: consentState.pending.filter((item) => item.id !== body.id),
        policyRevision: consentState.policyRevision + 1,
      };
      return Response.json({ ok: true });
    }
    if (path === 'auriga/shell/messaging/receive')
      return Response.json({ ok: true });
    if (path === 'auriga/shell/command') {
      if (body?.action === 'retry_local_uac') {
        account.loggedIn = true;
        account.connected = true;
        account.canRetryLocalUac = false;
      }
      if (body?.action === 'logout') {
        account.loggedIn = false;
        account.connected = false;
      }
      return Response.json({ ok: true });
    }
    if (path === 'auriga/uac/logout') return Response.json({ ok: true });
  }

  console.warn('[preview] unhandled', method, path, body);
  return new Response(JSON.stringify({ reason: 'preview_unhandled' }), {
    status: 404,
    headers: { 'content-type': 'application/json' },
  });
}

const SettingsView = createSettingsView(React, {
  localURL: (p) => 'https://preview.local/' + String(p).replace(/^\//, ''),
  fetcher: mockFetch,
  openLogin: () => {
    window.alert('预览模式：打开登录弹层');
    return true;
  },
  navigate: (to) => console.info('[preview] navigate', to),
});

function ConsentSettingsPreview() {
  const {
    SettingsCard,
    SettingRow,
    SettingSelect,
    SettingAction,
    SettingStatus,
    SettingInput,
  } = ui;
  const [defaults, setDefaults] = React.useState(consentState.defaults);
  const [rules, setRules] = React.useState(consentState.rules);
  const [query, setQuery] = React.useState({ allow_source: '', block: '' });
  const modeOptions = [
    {
      value: 'ask_each',
      label: '每次询问（推荐）',
      description: '每次首联都需确认',
    },
    {
      value: 'same_org',
      label: '同组织自动允许',
      description: '同组织来源自动放行',
    },
    {
      value: 'allowlist',
      label: '仅白名单自动允许',
      description: '仅名单内来源自动放行',
    },
    {
      value: 'deny_all',
      label: '全部拒绝',
      description: '拒绝全部首联请求',
    },
  ];
  const layers = [
    { id: 'conversation', label: '建立对话', hint: '是否允许对方建立对话' },
    { id: 'execution', label: '自动执行', hint: '是否允许自动执行工具' },
  ];

  return h(
    'div',
    {
      className: 'auriga-consent auriga-consent-settings',
      'aria-label': '首联授权',
    },
    h(
      SettingsCard,
      { icon: 'ShieldCheck', title: '默认授权策略' },
      ...layers.map((layer) =>
        h(SettingRow, {
          key: layer.id,
          label: layer.label,
          description: layer.hint,
          control: h(SettingSelect, {
            rich: true,
            'aria-label': layer.label,
            value: defaults[layer.id],
            options: modeOptions,
            onChange: async (mode) => {
              await mockFetch('auriga/shell/consent/default', {
                method: 'POST',
                body: JSON.stringify({ layer: layer.id, mode }),
              });
              setDefaults({ ...consentState.defaults });
            },
          }),
        }),
      ),
    ),
    h(
      SettingsCard,
      { icon: 'ListChecks', title: '授权名单' },
      ...['allow_source', 'block'].map((choice) => {
        const list = rules.filter((rule) => rule.choice === choice);
        const needle = (query[choice] || '').trim().toLowerCase();
        const visible = needle
          ? list.filter((rule) =>
              [
                rule.source.displayName,
                rule.source.empNo,
                rule.source.userId,
                rule.source.department,
              ]
                .filter(Boolean)
                .join(' ')
                .toLowerCase()
                .includes(needle),
            )
          : list;
        return h(
          'details',
          { key: choice, className: 'auriga-consent-list' },
          h(
            'summary',
            { className: 'auriga-consent-list-entry' },
            h(
              'div',
              { className: 'auriga-consent-list-entry-meta' },
              h(
                'div',
                { className: 'auriga-consent-list-entry-title' },
                choice === 'block' ? '屏蔽名单' : '总是允许名单',
              ),
              h(
                'div',
                { className: 'auriga-consent-list-entry-count' },
                `${list.length} 项`,
              ),
            ),
            h('span', { className: 'auriga-consent-list-entry-count' }, '查看'),
          ),
          h(
            'div',
            { className: 'auriga-consent-list-panel' },
            list.length > 8 &&
              h(SettingInput, {
                className: 'auriga-consent-list-search',
                'aria-label': '搜索名单',
                placeholder: '搜索名单',
                value: query[choice] || '',
                onChange: (e) =>
                  setQuery((old) => ({ ...old, [choice]: e.target.value })),
              }),
            list.length === 0 &&
              h('p', { className: 'auriga-settings-card-empty' }, '暂无规则'),
            ...visible.map((rule) =>
              h(
                'div',
                { key: rule.id, className: 'auriga-consent-member' },
                h(
                  'div',
                  { className: 'auriga-consent-member-copy' },
                  h(
                    'div',
                    { className: 'auriga-consent-member-name' },
                    h(
                      'span',
                      { className: 'auriga-consent-member-primary' },
                      rule.source.displayName || rule.source.userId,
                    ),
                    rule.source.empNo
                      ? h(
                          'span',
                          { className: 'auriga-consent-member-secondary' },
                          rule.source.empNo,
                        )
                      : null,
                  ),
                  h(
                    'div',
                    { className: 'auriga-consent-member-desc' },
                    [
                      rule.layer === 'conversation' ? '建立对话' : '自动执行',
                      rule.scope === 'source_user'
                        ? '此用户的所有 DSH'
                        : '此 DSH',
                      rule.source.department,
                    ]
                      .filter(Boolean)
                      .join(' · '),
                  ),
                ),
                h(
                  SettingAction,
                  {
                    compact: true,
                    quiet: true,
                    onClick: async () => {
                      await mockFetch('auriga/shell/consent/revoke', {
                        method: 'POST',
                        body: JSON.stringify({
                          layer: rule.layer,
                          ruleId: rule.id,
                        }),
                      });
                      setRules([...consentState.rules]);
                    },
                  },
                  '撤销',
                ),
              ),
            ),
          ),
        );
      }),
      h(
        SettingStatus,
        null,
        '撤销后重新核权；已执行的动作无法撤回，拒绝冷却仍然有效。',
      ),
    ),
  );
}

function Dot({ tone }) {
  return h('span', {
    className: 'auriga-dot',
    'data-tone': tone,
    'aria-hidden': true,
  });
}

function OverviewPanel({ onNavigate, pendingCount = 0, desktop }) {
  const {
    OverviewPage,
    OverviewWelcome,
    OverviewStatus,
    OverviewNotifications,
    OverviewCapabilities,
    OverviewActivity,
  } = overviewUI;
  const localSignedIn = localUac.state === 'authenticated';
  const cloudConnected = account.loggedIn && account.connected;
  const networkReady = account.network?.connected === true;
  const displayName =
    (localSignedIn && localUac.displayName) ||
    account.account?.displayName ||
    '';
  const signedIn = localSignedIn || account.loggedIn;

  const statusItems = [
    {
      id: 'local',
      tone: localSignedIn ? 'success' : 'idle',
      label: '本地登录',
      detail: localSignedIn ? '已登录' : '未登录',
    },
    {
      id: 'cloud',
      tone: cloudConnected ? 'success' : account.loggedIn ? 'warn' : 'idle',
      label: '云平台',
      detail: cloudConnected
        ? '已连接'
        : account.loggedIn
          ? '已认证，未连接'
          : '未连接',
    },
    {
      id: 'network',
      tone: networkReady ? 'success' : 'idle',
      label: '消息网络',
      detail: networkReady ? '就绪' : '未就绪',
    },
  ];

  const localOnlyAlert =
    localSignedIn && !cloudConnected
      ? '本地功能仍可用，部分云平台功能暂不可用。'
      : null;

  const notices = [];
  if (desktop?.available) {
    notices.push({
      id: 'desktop-update',
      title: '发现新版本',
      detail: desktop.available.version,
      action: h(
        'button',
        {
          type: 'button',
          className: 'auriga-primary',
          onClick: () => onNavigate('settings'),
        },
        '前往更新',
      ),
    });
  }
  if (pendingCount > 0) {
    notices.push({
      id: 'pending-consent',
      title: '待处理授权请求',
      detail: pendingCount + ' 项',
      action: h(
        'button',
        {
          type: 'button',
          onClick: () => onNavigate('inbox'),
        },
        '去处理',
      ),
    });
  }

  // No formal activity feed API yet — keep the module but hide without real data.
  const activityItems = undefined;

  return h(
    OverviewPage,
    null,
    h(OverviewWelcome, {
      name: displayName,
      signedIn,
    }),
    h(OverviewStatus, {
      items: statusItems,
      alert: localOnlyAlert,
      actions: !signedIn
        ? [
            h(
              'button',
              {
                key: 'login',
                type: 'button',
                className: 'auriga-primary',
                onClick: () => window.alert('预览模式：打开登录弹层'),
              },
              '登录平台',
            ),
          ]
        : localSignedIn && !cloudConnected
          ? [
              h(
                'button',
                {
                  key: 'retry',
                  type: 'button',
                  className: 'auriga-primary',
                  onClick: () => {
                    account.loggedIn = true;
                    account.connected = true;
                    window.alert('预览模式：重试连接（本地身份）');
                  },
                },
                '重试连接',
              ),
            ]
          : null,
    }),
    h(OverviewNotifications, { items: notices }),
    h(OverviewCapabilities, {
      items: OVERVIEW_CAPABILITIES,
      onNavigate,
    }),
    h(OverviewActivity, {
      items: activityItems,
      onMore: () => onNavigate('market'),
    }),
  );
}

function ResourcesBrowser({ mode, items, onChange }) {
  const mine = mode === 'mine';
  const [kindFilter, setKindFilter] = React.useState(mine ? 'plugin' : 'all');
  const [query, setQuery] = React.useState('');

  const visible = items.filter((pack) => {
    if (mine && !pack.installed) return false;
    if (kindFilter !== 'all' && pack.kind !== kindFilter) return false;
    const needle = query.trim().toLowerCase();
    if (!needle) return true;
    return [pack.name, pack.description, pack.author, pack.department]
      .join(' ')
      .toLowerCase()
      .includes(needle);
  });

  const kindLabel = (kind) => (kind === 'skill' ? '技能' : '插件');
  const kindButtons = mine
    ? [
        ['plugin', '插件'],
        ['skill', '技能'],
      ]
    : [
        ['all', '全部'],
        ['plugin', '插件'],
        ['skill', '技能'],
      ];

  return h(
    'aside',
    {
      className: 'resources',
      'aria-label': mine ? '我的资源' : '资源市场',
    },
    h(
      'div',
      { className: 'market-chrome' },
      h(
        'div',
        { className: 'market-search' },
        searchIcon(),
        h('input', {
          type: 'search',
          'aria-label': mine ? '搜索已安装资源' : '搜索市场资源',
          placeholder: mine
            ? '搜索本地已安装的插件 / 技能'
            : '搜索插件、技能、作者或部门',
          value: query,
          onChange: (event) => setQuery(event.target.value),
        }),
      ),
      h(
        'div',
        { className: 'market-toolbar' },
        h(
          'div',
          { className: 'market-kind-filters' },
          ...kindButtons.map(([value, label]) =>
            h(
              'button',
              {
                key: value,
                type: 'button',
                className: 'market-kind-filter',
                'aria-pressed': kindFilter === value,
                onClick: () => setKindFilter(value),
              },
              label,
            ),
          ),
        ),
        h(
          'div',
          { className: 'market-toolbar-tools' },
          h(
            'button',
            {
              type: 'button',
              title: '刷新',
              'aria-label': '刷新',
              onClick: () => onChange([...catalogPackages]),
            },
            '刷新',
          ),
        ),
      ),
      h(
        'p',
        { className: 'market-count' },
        mine
          ? `本地已安装 ${visible.length} 个资源`
          : `共 ${visible.length} 个资源`,
      ),
    ),
    visible.length === 0
      ? h(
          'p',
          { className: 'notice', role: 'status' },
          mine ? '暂无共享资源，请前往资源市场选择。' : '没有符合条件的资源。',
        )
      : h(
          'div',
          { className: 'cards' },
          ...visible.map((pack) =>
            h(
              'article',
              { key: pack.id, className: 'card resource-card' },
              h(
                'div',
                { className: 'resource-card-top' },
                h(
                  'header',
                  { className: 'resource-heading' },
                  kindIcon(pack.kind),
                  h(
                    'div',
                    null,
                    h('h3', null, pack.name),
                    h(
                      'span',
                      { className: 'resource-kind' },
                      kindLabel(pack.kind) + (pack.private ? ' · 私有' : ''),
                    ),
                  ),
                ),
                h(
                  'span',
                  { className: 'resource-version-corner' },
                  pack.version,
                ),
              ),
              h('p', { className: 'resource-description' }, pack.description),
              h(
                'p',
                { className: 'resource-attribution' },
                h('span', { className: 'resource-author' }, pack.author),
                h('span', { className: 'resource-attribution-sep' }, '·'),
                h(
                  'span',
                  { className: 'resource-department' },
                  pack.department,
                ),
              ),
              mine
                ? h(
                    'div',
                    { className: 'resource-card-footer' },
                    h(
                      'div',
                      { className: 'resource-card-meta' },
                      h(
                        'span',
                        {
                          className:
                            'resource-badge' +
                            (pack.updateAvailable
                              ? ' is-warn'
                              : pack.enabled
                                ? ' is-success'
                                : ''),
                        },
                        pack.updateAvailable
                          ? '可更新'
                          : pack.enabled
                            ? '已启用'
                            : '已关闭',
                      ),
                    ),
                    h(
                      'div',
                      { className: 'resource-actions' },
                      h(
                        'button',
                        {
                          type: 'button',
                          className: 'resource-card-action',
                          onClick: () =>
                            onChange(
                              items.map((item) =>
                                item.id === pack.id
                                  ? { ...item, enabled: !item.enabled }
                                  : item,
                              ),
                            ),
                        },
                        pack.enabled ? '关闭' : '启用',
                      ),
                      h(
                        'button',
                        {
                          type: 'button',
                          className: 'resource-card-action',
                          onClick: () =>
                            onChange(
                              items.map((item) =>
                                item.id === pack.id
                                  ? {
                                      ...item,
                                      installed: false,
                                      enabled: false,
                                    }
                                  : item,
                              ),
                            ),
                        },
                        '卸载',
                      ),
                    ),
                  )
                : h(
                    'div',
                    { className: 'resource-card-footer' },
                    h(
                      'div',
                      { className: 'resource-card-meta' },
                      h(
                        'span',
                        { className: 'resource-stat' },
                        `安装 ${pack.installs}`,
                      ),
                      h(
                        'span',
                        { className: 'resource-stat' },
                        `点赞 ${pack.stars}`,
                      ),
                      pack.installed
                        ? h(
                            'span',
                            { className: 'resource-badge is-success' },
                            '已安装',
                          )
                        : null,
                    ),
                    h(
                      'button',
                      {
                        type: 'button',
                        className: 'primary resource-card-action',
                        onClick: () => {
                          if (pack.installed) return;
                          onChange(
                            items.map((item) =>
                              item.id === pack.id
                                ? {
                                    ...item,
                                    installed: true,
                                    enabled: true,
                                    installs: item.installs + 1,
                                  }
                                : item,
                            ),
                          );
                        },
                      },
                      pack.installed ? '已安装' : '安装',
                    ),
                  ),
            ),
          ),
        ),
  );
}

function TeamPanel() {
  return h(
    'div',
    { className: 'auriga-card' },
    h(
      'div',
      { className: 'auriga-row' },
      h(Dot, { tone: 'done' }),
      h('strong', null, 'Team 工具已连接'),
    ),
    h('p', null, '以下工具由消息网络下发，可在会话中直接调用。'),
    h(
      'ul',
      { className: 'auriga-tools' },
      ...[
        'auriga_list_endpoints',
        'auriga_open_thread',
        'auriga_reply_thread',
        'auriga_read_thread',
      ].map((name) => h('li', { key: name }, h('code', null, name))),
    ),
    h(
      'div',
      { className: 'auriga-actions' },
      h('button', { type: 'button', className: 'auriga-primary' }, '新建会话'),
    ),
  );
}

function InboxPanel({ pending, onDecide }) {
  return h(
    'div',
    null,
    h('h2', { style: { margin: '0 0 8px', fontSize: 20 } }, '收件箱'),
    h(
      'p',
      {
        style: {
          margin: '0 0 16px',
          color: 'var(--dsw-alias-label-secondary)',
          fontSize: 13,
        },
      },
      '待处理的首联授权请求。批准或拒绝后会出现在同意策略名单中。',
    ),
    pending.length === 0
      ? h(
          'div',
          { className: 'auriga-blocked', role: 'status' },
          h(
            'div',
            { className: 'auriga-row' },
            h(Dot, { tone: 'idle' }),
            h('strong', null, '暂无待处理请求'),
          ),
        )
      : h(
          'div',
          { className: 'auriga-card' },
          ...pending.map((item) =>
            h(
              'div',
              { key: item.id, className: 'preview-inbox-item' },
              h(
                'div',
                { className: 'preview-inbox-meta' },
                h(
                  'div',
                  { className: 'preview-inbox-title' },
                  item.source.displayName || item.source.userId,
                ),
                h(
                  'div',
                  { className: 'preview-inbox-desc' },
                  [
                    item.layer === 'conversation'
                      ? '请求建立对话'
                      : '请求自动执行',
                    item.source.empNo,
                    item.source.department,
                    item.source.realm + '.' + item.source.node,
                  ]
                    .filter(Boolean)
                    .join(' · '),
                ),
              ),
              h(
                'div',
                { className: 'auriga-actions' },
                h(
                  'button',
                  {
                    type: 'button',
                    className: 'auriga-primary',
                    onClick: () => onDecide(item.id, 'allow'),
                  },
                  '允许',
                ),
                h(
                  'button',
                  {
                    type: 'button',
                    onClick: () => onDecide(item.id, 'block'),
                  },
                  '拒绝',
                ),
              ),
            ),
          ),
        ),
  );
}

function ActivityBar({ value, onChange, expanded, onToggle, badges }) {
  const top = navRegistry.list('top');
  const bottom = navRegistry.list('bottom');
  const ToggleIcon = expanded ? icons.PanelLeftClose : icons.PanelLeftOpen;
  const renderItem = (item) => {
    const Icon = icons[item.icon];
    const active = value === item.route;
    const badge = badges?.[item.id];
    const label =
      labels[expanded ? item.labelKey : item.shortLabelKey || item.labelKey];
    return h(
      'button',
      {
        key: item.id,
        type: 'button',
        className: 'auriga-nav-item',
        'aria-current': active ? 'page' : undefined,
        title: labels[item.labelKey],
        onClick: () => onChange(item.route),
      },
      h('span', { className: 'auriga-nav-icon' }, Icon ? h(Icon) : null),
      h('span', { className: 'auriga-nav-label' }, label),
      badge
        ? h('span', { className: 'auriga-nav-badge' }, String(badge))
        : null,
    );
  };
  return h(
    'nav',
    { className: 'auriga-activity-bar', 'aria-label': labels.nav_label },
    h(
      'button',
      {
        type: 'button',
        className: 'auriga-nav-toggle',
        'aria-label': expanded ? labels.nav_collapse : labels.nav_expand,
        'aria-expanded': expanded,
        title: expanded ? labels.nav_collapse : labels.nav_expand,
        onClick: onToggle,
      },
      h('span', { className: 'auriga-nav-icon' }, h(ToggleIcon)),
      expanded
        ? h('span', { className: 'auriga-nav-label' }, labels.nav_collapse)
        : null,
    ),
    h(
      'div',
      { className: 'auriga-activity-bar-top', role: 'list' },
      ...top.map(renderItem),
    ),
    h(
      'div',
      { className: 'auriga-activity-bar-bottom', role: 'list' },
      ...bottom.map(renderItem),
    ),
  );
}

function App() {
  const [tab, setTab] = React.useState('overview');
  const [expanded, setExpanded] = React.useState(() =>
    readActivityBarExpanded(),
  );
  const [pending, setPending] = React.useState(consentState.pending);
  const [packages, setPackages] = React.useState(catalogPackages);

  const panel = (id, children) =>
    h(
      'section',
      {
        role: 'tabpanel',
        id: 'auriga-panel-' + id,
        className: 'auriga-tabpanel',
        hidden: tab !== id || undefined,
      },
      children,
    );

  return h(
    'div',
    {
      className: 'auriga-shell auriga-app',
      'data-nav-expanded': expanded ? 'true' : 'false',
    },
    h(ActivityBar, {
      value: tab,
      onChange: setTab,
      expanded,
      onToggle: () =>
        setExpanded((current) => {
          const next = !current;
          writeActivityBarExpanded(next);
          return next;
        }),
      badges: { inbox: pending.length || undefined },
    }),
    h(
      'div',
      { className: 'auriga-main' },
      h(
        'div',
        { className: 'auriga-main-body' },
        panel(
          'overview',
          h(OverviewPanel, {
            onNavigate: setTab,
            pendingCount: pending.length,
            desktop: settingsState.desktop,
          }),
        ),
        panel(
          'market',
          h(ResourcesBrowser, {
            mode: 'market',
            items: packages,
            onChange: setPackages,
          }),
        ),
        panel(
          'mine',
          h(ResourcesBrowser, {
            mode: 'mine',
            items: packages,
            onChange: setPackages,
          }),
        ),
        panel('team', h(TeamPanel)),
        panel(
          'inbox',
          h(InboxPanel, {
            pending,
            onDecide: async (id) => {
              await mockFetch('auriga/shell/consent/decide', {
                method: 'POST',
                body: JSON.stringify({ id }),
              });
              setPending([...consentState.pending]);
            },
          }),
        ),
        panel(
          'settings',
          tab === 'settings' &&
            h(
              'section',
              {
                className: 'auriga-shell auriga-settings',
                'aria-label': '设置',
              },
              h('h2', null, '设置'),
              h(SettingsView, {
                locale: 'zh',
                boot: {
                  mode: 'connect',
                  pluginVersion: '0.1.17',
                  protocol: 2,
                  source: 'desktop',
                },
                consent: h(ConsentSettingsPreview),
                gotoOverview: () => setTab('overview'),
              }),
            ),
        ),
      ),
    ),
  );
}

function bindThemeToggle() {
  const dark = document.getElementById('theme-dark');
  const light = document.getElementById('theme-light');
  const apply = (theme) => {
    document.documentElement.setAttribute('data-theme', theme);
    dark?.setAttribute('aria-pressed', theme === 'dark' ? 'true' : 'false');
    light?.setAttribute('aria-pressed', theme === 'light' ? 'true' : 'false');
  };
  dark?.addEventListener('click', () => apply('dark'));
  light?.addEventListener('click', () => apply('light'));
}

bindThemeToggle();
createRoot(document.getElementById('root')).render(h(App));
