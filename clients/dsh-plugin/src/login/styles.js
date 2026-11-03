/** Scoped layout. DSH theme tokens win when the host supplies them; the
 * fallbacks keep the anonymous page legible in light and dark without them. */
export const loginStyles = `
.auriga-login {
  --al-fg: var(--dsw-alias-label-primary, #16202a);
  --al-muted: var(--dsw-alias-label-secondary, #5b6875);
  --al-surface: var(--dsw-alias-bg-layer-1, #ffffff);
  --al-page: var(--dsw-alias-bg-layer-2, #f3f5f7);
  --al-line: var(--dsw-alias-border-l1, #d5dbe1);
  --al-accent: var(--dsw-alias-state-business-primary, #1f6fd1);
  --al-error: var(--dsw-alias-state-error-primary, #c4352b);
  font: inherit; color: var(--al-fg); background: var(--al-page);
  min-height: 100%; padding: 12vh 20px 32px;
  display: grid; justify-items: center; align-content: start; gap: 18px;
}
@media (prefers-color-scheme: dark) {
  .auriga-login {
    --al-fg: var(--dsw-alias-label-primary, #e6ebf0);
    --al-muted: var(--dsw-alias-label-secondary, #93a0ad);
    --al-surface: var(--dsw-alias-bg-layer-1, #161c22);
    --al-page: var(--dsw-alias-bg-layer-2, #0e1317);
    --al-line: var(--dsw-alias-border-l1, #2a333c);
    --al-accent: var(--dsw-alias-state-business-primary, #4c8fe6);
    --al-error: var(--dsw-alias-state-error-primary, #f0776c);
  }
}
.auriga-login * { box-sizing: border-box; }
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
.auriga-login button.primary { background: var(--al-accent); border-color: var(--al-accent); color: #fff; font-weight: 600; }
.auriga-login button.primary:hover:not(:disabled) { filter: brightness(1.08); }
.auriga-login button.ghost { border-color: transparent; color: var(--al-muted); }
.auriga-login button.ghost:hover:not(:disabled), .auriga-login button:not(.primary):not(.link):hover:not(:disabled) { background: var(--dsw-alias-interactive-bg-hover, color-mix(in srgb, var(--al-fg) 6%, transparent)); }
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
