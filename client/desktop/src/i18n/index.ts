import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import zh from "./zh";
import en from "./en";

export type Language = "zh" | "en";
const KEY = "jieshangai:language";

// Chinese stays the default so existing users and tests see unchanged copy.
function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(KEY);
    if (saved === "zh" || saved === "en") return saved;
  } catch {
    /* Storage can be unavailable; fall back to Chinese. */
  }
  return "zh";
}

void i18n.use(initReactI18next).init({
  resources: { zh: { translation: zh }, en: { translation: en } },
  lng: initialLanguage(),
  fallbackLng: "zh",
  interpolation: { escapeValue: false },
});

export function setLanguage(language: Language) {
  try {
    localStorage.setItem(KEY, language);
  } catch {
    /* Preference just won't persist. */
  }
  void i18n.changeLanguage(language);
  document.documentElement.lang = language === "zh" ? "zh-CN" : "en";
}

export default i18n;
