import { en, type MessageKey } from './en';
import { zhTW } from './zh-TW';
import { detectLocale, isLocale, type LocaleId } from './detect';

export type { LocaleId, MessageKey };

const catalogs: Record<LocaleId, Record<MessageKey, string>> = { en, 'zh-TW': zhTW };

/** Shown in the language toggle. Labels are endonyms so they stay findable in any UI language. */
export const localeOptions: readonly { id: LocaleId; label: string; htmlLang: string }[] = [
  { id: 'en', label: 'English', htmlLang: 'en' },
  { id: 'zh-TW', label: '繁體中文', htmlLang: 'zh-Hant' },
];

const STORAGE_KEY = 'linzone.lang';

function loadSavedLocale(): LocaleId | null {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    return isLocale(value) ? value : null;
  } catch {
    return null;
  }
}

function systemLanguages(): string[] {
  if (typeof navigator === 'undefined') return [];
  if (navigator.languages?.length) return [...navigator.languages];
  return navigator.language ? [navigator.language] : [];
}

// An explicit choice made via the toggle wins; otherwise follow the system language,
// falling back to English when none of the preferred languages is supported.
let current = $state<LocaleId>(loadSavedLocale() ?? detectLocale(systemLanguages()));

export function getLocale(): LocaleId {
  return current;
}

export function setLocale(id: LocaleId): void {
  current = id;
  try {
    localStorage.setItem(STORAGE_KEY, id);
  } catch {
    /* storage unavailable: the choice just lasts for this session */
  }
}

/** Translate a key, substituting `{name}` placeholders. Reactive: re-evaluates when the locale changes. */
export function t(key: MessageKey, params?: Record<string, string | number>): string {
  const template = catalogs[current][key] ?? en[key] ?? key;
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (match, name: string) => (name in params ? String(params[name]) : match));
}
