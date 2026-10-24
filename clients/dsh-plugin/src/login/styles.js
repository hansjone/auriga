/** Scoped layout only; the anonymous host must supply DSH theme tokens. */
export const loginStyles = `
.auriga-login { font: inherit; color: var(--dsw-alias-label-primary, inherit); padding: 24px; display: grid; justify-items: center; gap: 20px; }
.auriga-login * { box-sizing: border-box; }
.auriga-login .brand { font-weight: 600; display: flex; align-items: center; gap: 9px; }
.auriga-login .brand svg { width: 26px; height: 26px; }
.auriga-login .card { width: min(380px, 100%); padding: 30px; border-radius: 24px; border: 1px solid var(--dsw-alias-border-l1, currentColor); background: var(--dsw-alias-bg-layer-1, transparent); }
.auriga-login h2 { margin: 0 0 20px; text-align: center; font-size: 1.3em; }
.auriga-login p, .auriga-login label { overflow-wrap: anywhere; }
.auriga-login .field { display: grid; gap: 7px; margin-block: 16px; }
.auriga-login .muted { color: var(--dsw-alias-label-secondary, inherit); font-size: .9em; }
.auriga-login input, .auriga-login select, .auriga-login button { font: inherit; color: inherit; background: transparent; border: 1px solid var(--dsw-alias-border-l1, currentColor); border-radius: 10px; padding: 11px 14px; min-width: 0; max-width: 100%; }
.auriga-login input:not([type=checkbox]), .auriga-login select, .auriga-login button { width: 100%; }
.auriga-login input[type=checkbox] { justify-self: start; }
.auriga-login button { cursor: pointer; margin-top: 12px; }
.auriga-login button:hover { background: var(--dsw-alias-interactive-bg-hover, transparent); }
.auriga-login button:disabled { opacity: .55; cursor: default; }
.auriga-login :is(input, select, button):focus-visible { outline: var(--dsw-focus-ring-width, 2px) solid var(--dsw-focus-ring-color, var(--dsw-alias-state-business-primary, currentColor)); outline-offset: 3px; }
.auriga-login .error { color: var(--dsw-alias-state-error-primary, inherit); }
`;
