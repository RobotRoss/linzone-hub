// Pure locale-matching helpers (no Svelte / DOM dependencies, so they are easy to test).

export type LocaleId = 'en' | 'zh-TW';

export const SUPPORTED_LOCALES: readonly LocaleId[] = ['en', 'zh-TW'];
export const FALLBACK_LOCALE: LocaleId = 'en';

export function isLocale(value: unknown): value is LocaleId {
  return typeof value === 'string' && (SUPPORTED_LOCALES as readonly string[]).includes(value);
}

/**
 * Map a BCP-47 / POSIX-style language tag to a supported locale, or null if unsupported.
 *
 * - `en`, `en-US`, `en_GB.UTF-8` ...      -> en
 * - `zh-TW`, `zh-HK`, `zh-MO`, `zh-Hant*` -> zh-TW (Traditional Chinese)
 * - `zh`, `zh-CN`, `zh-SG`, `zh-Hans*`    -> null (we only ship Traditional Chinese)
 */
export function matchLocale(tag: string): LocaleId | null {
  const cleaned = tag.trim().split(/[.@]/)[0].toLowerCase().replace(/_/g, '-');
  const [language, ...subtags] = cleaned.split('-').filter(Boolean);
  if (language === 'en') return 'en';
  if (language === 'zh') {
    if (subtags.includes('hans')) return null;
    if (subtags.includes('hant') || subtags.some((s) => s === 'tw' || s === 'hk' || s === 'mo')) return 'zh-TW';
  }
  return null;
}

/** Pick the first supported language from an ordered preference list, else fall back to English. */
export function detectLocale(preferred: readonly string[]): LocaleId {
  for (const tag of preferred) {
    const match = matchLocale(tag);
    if (match) return match;
  }
  return FALLBACK_LOCALE;
}
