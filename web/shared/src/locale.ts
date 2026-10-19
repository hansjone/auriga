import type { LocalizedText } from './generated/steps';

/** Pick text for the active locale; fall back to en, then any value. */
export function pickLocalized(
  text: LocalizedText | undefined,
  locale: string,
): string {
  if (!text) return '';
  return text[locale] ?? text.en ?? Object.values(text)[0] ?? '';
}
