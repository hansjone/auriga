/** Scoped layout. DSH theme tokens win when the host supplies them; the
 * fallbacks keep the anonymous page legible in light and dark without them. */
export const loginStyles = `
.auriga-login {
  /* Same values as DSH's own body / body[data-ds-dark-theme] tokens, so the
     page matches DSH even when the anonymous host does not load its theme. */
  --al-fg: var(--dsw-alias-label-primary, rgb(15 17 21));
  --al-muted: var(--dsw-alias-label-secondary, rgb(97 102 107));
  --al-page: var(--dsw-alias-bg-layer-1, rgb(255 255 255));
  --al-surface: var(--dsw-alias-bg-layer-2, rgb(255 255 255));
  --al-line: var(--dsw-alias-border-l2, #0000001a);
  --al-accent: var(--dsw-alias-state-business-primary, rgb(65 118 230));
  --al-on-accent: #fff;
  --al-error: var(--dsw-alias-state-error-primary, rgb(236 19 19));
  --al-hover: var(--dsw-alias-interactive-bg-hover, #2631480f);
  font-family: Helvetica Neue, Helvetica, Arial, PingFang SC, Hiragino Sans GB, Heiti SC, Microsoft YaHei, WenQuanYi Micro Hei, sans-serif;
  color: var(--al-fg); background: var(--al-page);
  /* The page is exclusively ours: own the whole window, not just the content height. */
  position: fixed; inset: 0; overflow: auto; padding: 12vh 20px 32px;
  display: grid; justify-items: center; align-content: start; gap: 18px;
}
@media (prefers-color-scheme: dark) {
  /* DSH drives prefers-color-scheme from its own theme setting. */
  .auriga-login {
    --al-fg: var(--dsw-alias-label-primary, rgb(249 250 251));
    --al-muted: var(--dsw-alias-label-secondary, rgb(207 211 214));
    --al-page: var(--dsw-alias-bg-layer-1, rgb(35 35 36));
    --al-surface: var(--dsw-alias-bg-layer-2, rgb(44 44 46));
    --al-line: var(--dsw-alias-border-l2, #ffffff1f);
    --al-accent: var(--dsw-alias-state-business-primary, rgb(122 170 255));
    --al-on-accent: rgb(15 17 21);
    --al-error: var(--dsw-alias-state-error-primary, rgb(242 90 90));
    --al-hover: var(--dsw-alias-interactive-bg-hover, #ffffff14);
  }
}
.auriga-login * { box-sizing: border-box; }
/* The page notice sits outside the full-window shell; keep it above it. */
.auriga-login-notice { position: fixed; inset: 12px 16px auto; z-index: 1; margin: 0; text-align: center; font-size: .9em; color: var(--dsw-alias-label-secondary, rgb(97 102 107)); }
.auriga-login-notice:empty { display: none; }
.auriga-login .brand { display: flex; align-items: center; gap: 10px; font-weight: 600; font-size: 1.05em; letter-spacing: .01em; }
.auriga-login .brand svg { width: 24px; height: 24px; color: var(--al-accent); }
.auriga-login .card { width: min(360px, 100%); padding: 28px 28px 24px; border-radius: 14px; border: 1px solid var(--al-line); background: var(--al-surface); display: grid; gap: 4px; }
.auriga-login h2 { margin: 0 0 12px; font-size: 1.25em; font-weight: 600; }
.auriga-login p, .auriga-login label { margin: 0; overflow-wrap: anywhere; }
.auriga-login form { display: grid; gap: 14px; }
.auriga-login .field { display: grid; gap: 6px; }
.auriga-login label { font-size: .9em; color: var(--al-muted); }
.auriga-login .muted { color: var(--al-muted); font-size: .9em; }
.auriga-login input, .auriga-login select { font: inherit; color: inherit; background: transparent; border: 1px solid var(--al-line); border-radius: 8px; padding: 10px 12px; width: 100%; min-width: 0; }
.auriga-login input[type=checkbox] { width: auto; justify-self: start; }
.auriga-login input:hover, .auriga-login select:hover { border-color: var(--al-muted); }
.auriga-login [aria-invalid=true] { border-color: var(--al-error); }
.auriga-login button { font: inherit; cursor: pointer; border-radius: 8px; padding: 10px 14px; width: 100%; margin-top: 6px; border: 1px solid var(--al-line); background: transparent; color: inherit; }
.auriga-login button.primary { background: var(--al-accent); border-color: var(--al-accent); color: var(--al-on-accent); font-weight: 600; }
.auriga-login button.primary:hover:not(:disabled) { filter: brightness(1.08); }
.auriga-login button.ghost { border-color: transparent; color: var(--al-muted); }
.auriga-login button.ghost:hover:not(:disabled), .auriga-login button:not(.primary):not(.link):hover:not(:disabled) { background: var(--al-hover); }
.auriga-login button.link { width: auto; margin: 0; padding: 6px 10px; border: 0; background: none; color: var(--al-muted); font-size: .9em; }
.auriga-login button.link:hover { color: var(--al-fg); text-decoration: underline; }
.auriga-login button:disabled { opacity: .55; cursor: default; }
.auriga-login :is(input, select, button):focus-visible { outline: var(--dsw-focus-ring-width, 2px) solid var(--dsw-focus-ring-color, var(--al-accent)); outline-offset: 2px; }
.auriga-login .error { color: var(--al-error); }
.auriga-login .banner { padding: 10px 12px; border-radius: 8px; font-size: .92em; background: color-mix(in srgb, var(--al-error) 10%, transparent); }
.auriga-login [role=status]:not(.banner) { color: var(--al-muted); font-size: .9em; margin-top: 6px; }
@media (prefers-reduced-motion: no-preference) {
  .auriga-login button, .auriga-login input { transition: border-color .15s, background-color .15s, filter .15s; }
}
`;
