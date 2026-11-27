// @ts-nocheck
/**
 * Settings UI primitives for Auriga native settings.
 * @param {any} React
 */
import { createSettingsIcons } from './icons.js';

export function createSettingsUI(React) {
  const h = React.createElement;
  const icons = createSettingsIcons(h);

  function childList(children) {
    if (children == null || children === false) return [];
    return Array.isArray(children) ? children.flat(Infinity) : [children];
  }

  function SettingsLayout({ nav, children, notice }) {
    return h(
      'div',
      { className: 'auriga-settings-shell' },
      nav,
      h(
        'div',
        { className: 'auriga-settings-main' },
        h(
          'div',
          { className: 'auriga-settings-main-inner' },
          ...childList(children),
        ),
        notice
          ? h(
              'p',
              {
                className: 'auriga-settings-notice',
                role: 'status',
                'aria-live': 'polite',
              },
              notice,
            )
          : null,
      ),
    );
  }

  function SettingsNavigation({ groups, value, onChange, label }) {
    return h(
      'nav',
      { className: 'auriga-settings-nav', 'aria-label': label },
      ...groups.map((group) =>
        h(
          'div',
          { key: group.id, className: 'auriga-settings-nav-group' },
          h(
            'div',
            { className: 'auriga-settings-nav-group-label' },
            group.label,
          ),
          ...group.items.map((item) => {
            const Icon = item.icon ? icons[item.icon] : null;
            return h(
              'button',
              {
                key: item.id,
                type: 'button',
                className: 'auriga-settings-nav-item',
                'aria-current': value === item.id ? 'page' : undefined,
                onClick: () => onChange(item.id),
              },
              Icon
                ? h(Icon, { size: 16, className: 'auriga-settings-nav-icon' })
                : null,
              h('span', { className: 'auriga-settings-nav-label' }, item.label),
            );
          }),
        ),
      ),
    );
  }

  function SettingsPanel({ id, active, 'aria-label': ariaLabel, children }) {
    return h(
      'section',
      {
        className: 'auriga-settings-panel',
        'data-settings-panel': id,
        'aria-label': ariaLabel,
        hidden: !active || undefined,
      },
      ...childList(children),
    );
  }

  function SettingsPageTitle({ children }) {
    return h('h2', { className: 'auriga-settings-page-title' }, children);
  }

  function SettingsSection({
    title,
    description,
    children,
    'aria-label': ariaLabel,
  }) {
    return h(
      'section',
      {
        className: 'auriga-settings-section',
        'aria-label': ariaLabel,
      },
      title
        ? h('h3', { className: 'auriga-settings-section-title' }, title)
        : null,
      description
        ? h('p', { className: 'auriga-settings-section-desc' }, description)
        : null,
      h(
        'div',
        { className: 'auriga-settings-section-body' },
        ...childList(children),
      ),
    );
  }

  function SettingRow({
    icon,
    label,
    description,
    control,
    status,
    action,
    stack = false,
  }) {
    const Icon = icon ? icons[icon] : null;
    const isStatus = status !== undefined || action !== undefined;
    return h(
      'div',
      {
        className:
          'auriga-setting-row' +
          (stack ? ' auriga-setting-row-stack' : '') +
          (Icon ? ' has-icon' : '') +
          (isStatus ? ' is-status' : ''),
      },
      h(
        'div',
        { className: 'auriga-setting-row-meta' },
        Icon
          ? h(Icon, { size: 16, className: 'auriga-setting-row-icon' })
          : null,
        h(
          'div',
          { className: 'auriga-setting-row-copy' },
          label
            ? h('div', { className: 'auriga-setting-row-label' }, label)
            : null,
          description
            ? h('div', { className: 'auriga-setting-row-desc' }, description)
            : null,
        ),
      ),
      isStatus
        ? h('div', { className: 'auriga-setting-row-status' }, status || null)
        : null,
      isStatus
        ? h(
            'div',
            { className: 'auriga-setting-row-action' },
            action ||
              h('span', {
                className: 'auriga-setting-row-action-slot',
                'aria-hidden': true,
              }),
          )
        : control
          ? h('div', { className: 'auriga-setting-row-control' }, control)
          : null,
    );
  }

  function SettingSwitch({
    checked,
    disabled,
    onChange,
    'aria-label': ariaLabel,
  }) {
    return h('button', {
      type: 'button',
      role: 'switch',
      className: 'auriga-setting-switch',
      'aria-checked': checked ? 'true' : 'false',
      'aria-label': ariaLabel,
      disabled: disabled || undefined,
      onClick: () => {
        if (!disabled) onChange(!checked);
      },
    });
  }

  function SettingSelect({
    value,
    options,
    disabled,
    onChange,
    rich = false,
    'aria-label': ariaLabel,
  }) {
    const [open, setOpen] = React.useState(false);
    const Check = icons.Check;
    const selected =
      options.find((/** @type {any} */ o) => o.value === value) ?? options[0];
    React.useEffect(() => {
      if (!open) return;
      const close = () => setOpen(false);
      const timer = setTimeout(() => {
        document.addEventListener('click', close);
      }, 0);
      return () => {
        clearTimeout(timer);
        document.removeEventListener('click', close);
      };
    }, [open]);
    return h(
      'div',
      {
        className:
          'auriga-setting-select' +
          (open ? ' is-open' : '') +
          (rich ? ' is-rich' : ''),
      },
      h(
        'button',
        {
          type: 'button',
          className: 'auriga-setting-select-trigger',
          'aria-label': ariaLabel,
          'aria-haspopup': 'listbox',
          'aria-expanded': open,
          disabled: disabled || undefined,
          onClick: (/** @type {any} */ event) => {
            event.stopPropagation();
            if (!disabled) setOpen((v) => !v);
          },
        },
        selected?.label ?? '',
        h(
          'span',
          { className: 'auriga-setting-select-chevron', 'aria-hidden': true },
          '▾',
        ),
      ),
      open &&
        h(
          'div',
          { className: 'auriga-setting-select-menu', role: 'listbox' },
          ...options.map((/** @type {any} */ option) => {
            const active = option.value === value;
            return h(
              'button',
              {
                key: option.value,
                type: 'button',
                role: 'option',
                className:
                  'auriga-setting-select-option' +
                  (rich ? ' is-rich' : '') +
                  (active ? ' is-selected' : ''),
                'aria-selected': active,
                onClick: (/** @type {any} */ event) => {
                  event.stopPropagation();
                  setOpen(false);
                  if (option.value !== value) onChange(option.value);
                },
              },
              rich
                ? h(
                    'span',
                    { className: 'auriga-setting-select-option-copy' },
                    h(
                      'span',
                      { className: 'auriga-setting-select-option-label' },
                      option.label,
                    ),
                    option.description
                      ? h(
                          'span',
                          {
                            className: 'auriga-setting-select-option-desc',
                          },
                          option.description,
                        )
                      : null,
                  )
                : option.label,
              rich && active && Check
                ? h(Check, {
                    size: 14,
                    className: 'auriga-setting-select-check',
                  })
                : null,
            );
          }),
        ),
    );
  }

  function SettingInput(props) {
    const { multiline, rows = 3, className, ...rest } = props;
    if (multiline) {
      return h('textarea', {
        className:
          'auriga-setting-input auriga-setting-textarea ' + (className || ''),
        rows,
        ...rest,
      });
    }
    return h('input', {
      className: 'auriga-setting-input ' + (className || ''),
      ...rest,
    });
  }

  /**
   * Custom file picker that still uses a native hidden file input.
   * @param {{accept?:string,disabled?:boolean,file?:File|null,placeholder?:string,clearLabel?:string,onChange:(file:File|null)=>void,'aria-label'?:string}} props
   */
  function SettingFilePicker({
    accept,
    disabled,
    file = null,
    placeholder = '',
    clearLabel = 'Clear',
    onChange,
    'aria-label': ariaLabel,
  }) {
    const inputRef =
      typeof React.useRef === 'function'
        ? React.useRef(null)
        : { current: null };
    const Clear = icons.X;
    return h(
      'div',
      {
        className:
          'auriga-setting-filepicker' + (disabled ? ' is-disabled' : ''),
      },
      h('input', {
        ref: inputRef,
        type: 'file',
        className: 'auriga-setting-filepicker-input',
        accept,
        disabled: !!disabled,
        'aria-label': ariaLabel,
        onChange: (/** @type {any} */ event) => {
          const next = event.target.files?.[0] ?? null;
          const result = onChange(next);
          event.target.value = '';
          return result;
        },
      }),
      h(
        'button',
        {
          type: 'button',
          className: 'auriga-setting-filepicker-trigger',
          disabled: !!disabled,
          onClick: () => {
            if (!disabled) inputRef.current?.click?.();
          },
        },
        file
          ? h(
              'span',
              { className: 'auriga-setting-filepicker-name' },
              file.name || file.fileName || placeholder,
            )
          : h(
              'span',
              { className: 'auriga-setting-filepicker-placeholder' },
              placeholder,
            ),
      ),
      file
        ? h(
            'button',
            {
              type: 'button',
              className: 'auriga-setting-filepicker-clear',
              'aria-label': clearLabel,
              title: clearLabel,
              disabled: !!disabled,
              onClick: () => onChange(null),
            },
            Clear ? h(Clear, { size: 14 }) : '×',
          )
        : null,
    );
  }

  function SettingAction({
    children,
    onClick,
    disabled,
    primary = false,
    danger = false,
    quiet = false,
    compact = false,
    icon,
    type = 'button',
  }) {
    const Icon = icon ? icons[icon] : null;
    return h(
      'button',
      {
        type,
        className:
          'auriga-setting-action' +
          (primary ? ' is-primary' : '') +
          (danger ? ' is-danger' : '') +
          (quiet ? ' is-quiet' : '') +
          (compact ? ' is-compact' : '') +
          (Icon ? ' has-icon' : ''),
        disabled: !!disabled,
        onClick,
      },
      ...(Icon
        ? [
            h(Icon, {
              size: 14,
              className: 'auriga-setting-action-icon',
            }),
            children,
          ]
        : [children]),
    );
  }

  function SettingIconButton({
    icon = 'RefreshCw',
    onClick,
    disabled,
    busy = false,
    success = false,
    label,
  }) {
    const Icon = icons[success ? 'Check' : icon];
    return h(
      'button',
      {
        type: 'button',
        className:
          'auriga-setting-icon-btn' +
          (busy ? ' is-busy' : '') +
          (success ? ' is-success' : ''),
        'aria-label': label,
        title: label,
        disabled: !!disabled,
        onClick,
      },
      Icon ? h(Icon, { size: 14 }) : null,
    );
  }

  /**
   * @param {{ tone?: 'success'|'processing'|'idle'|'error'|'warn', label: any, trailing?: any }} props
   */
  function StatusIndicator({ tone = 'idle', label, trailing }) {
    return h(
      'div',
      {
        className: 'auriga-status-indicator is-' + tone,
        role: 'status',
      },
      h('span', {
        className: 'auriga-status-indicator-dot',
        'aria-hidden': true,
      }),
      h('span', { className: 'auriga-status-indicator-label' }, label),
      trailing || null,
    );
  }

  function SettingStatus({ children, tone = 'default' }) {
    return h(
      'p',
      {
        className:
          'auriga-setting-status' + (tone !== 'default' ? ' is-' + tone : ''),
        role: tone === 'error' ? 'alert' : 'status',
      },
      children,
    );
  }

  function SettingReadonly({
    label,
    value,
    status,
    onCopy,
    copyLabel,
    muted = false,
    copied = false,
    copySlot = false,
  }) {
    const showCopy = copySlot || typeof onCopy === 'function';
    return h(
      'div',
      {
        className:
          'auriga-setting-row is-readonly' + (showCopy ? ' has-copy' : ''),
      },
      h('span', { className: 'auriga-setting-row-label' }, label),
      h(
        'div',
        { className: 'auriga-setting-readonly-value-wrap' },
        status ||
          h(
            'span',
            {
              className:
                'auriga-setting-readonly-value' + (muted ? ' is-muted' : ''),
            },
            value,
          ),
      ),
      showCopy
        ? typeof onCopy === 'function'
          ? h(SettingIconButton, {
              icon: 'Copy',
              success: !!copied,
              label: copyLabel || 'Copy',
              onClick: onCopy,
            })
          : h('span', {
              className: 'auriga-setting-row-action-slot is-icon',
              'aria-hidden': true,
            })
        : null,
    );
  }

  function AccountCard({ initials, title, empNo, refresh, children }) {
    return h(
      'section',
      {
        className: 'auriga-account-card',
        'aria-label': 'Auriga DSH status',
      },
      h(
        'div',
        { className: 'auriga-account-card-header' },
        h(
          'div',
          { className: 'auriga-account-card-profile' },
          h(
            'div',
            { className: 'auriga-account-avatar', 'aria-hidden': true },
            initials,
          ),
          h(
            'div',
            { className: 'auriga-account-card-identity' },
            h('span', { className: 'auriga-account-card-name' }, title),
            empNo
              ? h('span', { className: 'auriga-account-card-empno' }, empNo)
              : null,
          ),
        ),
        refresh
          ? h('div', { className: 'auriga-account-card-refresh' }, refresh)
          : null,
      ),
      children
        ? h(
            'div',
            { className: 'auriga-account-card-status' },
            ...childList(children),
          )
        : null,
    );
  }

  function PlatformCard({ title, status, children, notice, actions }) {
    const Cloud = icons.Cloud;
    return h(
      'section',
      {
        className: 'auriga-platform-card',
        'aria-label': title,
      },
      h(
        'div',
        { className: 'auriga-platform-card-header' },
        h(
          'div',
          { className: 'auriga-platform-card-brand' },
          h(Cloud, { size: 18, className: 'auriga-platform-card-icon' }),
          h('span', { className: 'auriga-platform-card-title' }, title),
        ),
        status || null,
      ),
      h(
        'div',
        { className: 'auriga-platform-card-body' },
        ...childList(children),
      ),
      notice || null,
      actions
        ? h(
            'div',
            { className: 'auriga-platform-card-actions' },
            ...childList(actions),
          )
        : null,
    );
  }

  function PlatformAddressField({ icon = 'Link2', children }) {
    const Icon = icons[icon];
    return h(
      'div',
      { className: 'auriga-platform-address' },
      Icon
        ? h(Icon, {
            size: 16,
            className: 'auriga-platform-address-icon',
          })
        : null,
      children,
    );
  }

  /**
   * Desktop update summary: brand mark + version + primary action.
   * @param {{title:any,version?:any,action?:any,children?:any,'aria-label'?:string}} props
   */
  function DesktopSummaryCard({
    title,
    version,
    action,
    children,
    'aria-label': ariaLabel,
  }) {
    const Mark = icons.AurigaMark;
    return h(
      'section',
      {
        className: 'auriga-desktop-summary-card',
        'aria-label': ariaLabel || title,
      },
      h(
        'div',
        { className: 'auriga-desktop-summary-header' },
        h(
          'div',
          { className: 'auriga-desktop-summary-brand' },
          h(Mark, {
            size: 48,
            className: 'auriga-desktop-summary-logo',
          }),
          h(
            'div',
            { className: 'auriga-desktop-summary-copy' },
            h('div', { className: 'auriga-desktop-summary-title' }, title),
            version
              ? h(
                  'div',
                  { className: 'auriga-desktop-summary-version' },
                  version,
                )
              : null,
          ),
        ),
        action
          ? h('div', { className: 'auriga-desktop-summary-action' }, action)
          : null,
      ),
      children
        ? h(
            'div',
            { className: 'auriga-desktop-summary-body' },
            ...childList(children),
          )
        : null,
    );
  }

  /**
   * Lightweight settings card shared by proxy / platform-style pages.
   * @param {{icon?:string,title:any,status?:any,description?:any,children?:any,notice?:any,footer?:any,actions?:any,'aria-label'?:string}} props
   */
  function SettingsCard({
    icon,
    title,
    status,
    description,
    children,
    notice,
    footer,
    actions,
    'aria-label': ariaLabel,
  }) {
    const Icon = icon ? icons[icon] : null;
    const hasFooter = footer != null || actions != null;
    return h(
      'section',
      {
        className: 'auriga-settings-card',
        ...(ariaLabel ? { 'aria-label': ariaLabel } : {}),
      },
      h(
        'div',
        { className: 'auriga-settings-card-header' },
        h(
          'div',
          { className: 'auriga-settings-card-brand' },
          Icon
            ? h(Icon, { size: 18, className: 'auriga-settings-card-icon' })
            : null,
          h(
            'div',
            { className: 'auriga-settings-card-copy' },
            h('span', { className: 'auriga-settings-card-title' }, title),
            description
              ? h('p', { className: 'auriga-settings-card-desc' }, description)
              : null,
          ),
        ),
        status || null,
      ),
      children
        ? h(
            'div',
            { className: 'auriga-settings-card-body' },
            ...childList(children),
          )
        : null,
      notice || null,
      hasFooter
        ? h(
            'div',
            { className: 'auriga-settings-card-footer' },
            h(
              'div',
              { className: 'auriga-settings-card-footer-left' },
              footer || null,
            ),
            actions
              ? h(
                  'div',
                  { className: 'auriga-settings-card-actions' },
                  ...childList(actions),
                )
              : null,
          )
        : null,
    );
  }

  return {
    SettingsLayout,
    SettingsNavigation,
    SettingsPanel,
    SettingsPageTitle,
    SettingsSection,
    SettingRow,
    SettingSwitch,
    SettingSelect,
    SettingInput,
    SettingFilePicker,
    SettingAction,
    SettingIconButton,
    StatusIndicator,
    SettingStatus,
    SettingReadonly,
    AccountCard,
    PlatformCard,
    PlatformAddressField,
    SettingsCard,
    DesktopSummaryCard,
    icons,
  };
}

export const SETTINGS_CATEGORY_KEY = 'auriga.settings.category';

export function readSettingsCategory(fallback = 'account') {
  try {
    const value = sessionStorage.getItem(SETTINGS_CATEGORY_KEY);
    return value || fallback;
  } catch {
    return fallback;
  }
}

export function writeSettingsCategory(id) {
  try {
    sessionStorage.setItem(SETTINGS_CATEGORY_KEY, id);
  } catch {
    /* private mode */
  }
}
