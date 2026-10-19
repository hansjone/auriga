import { defineCustomElement } from 'vue';
import TeamPanel from './TeamPanel.ce.vue';
export const AurigaTeamPanel = defineCustomElement(TeamPanel);
export function registerPanel() {
  if (!customElements.get('auriga-team-panel'))
    customElements.define('auriga-team-panel', AurigaTeamPanel);
}
registerPanel();
