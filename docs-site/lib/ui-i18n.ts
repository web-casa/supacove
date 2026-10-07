import { defineI18nUI } from "fumadocs-ui/i18n";
import { i18n } from "@/lib/i18n";

// Chrome translations for the docs UI (search dialog, TOC, switcher).
// Keys are the English display names from fumadocs-ui's key list.
export const uiI18n = defineI18nUI(i18n, {
  zh: {
    displayName: "简体中文",
    "Search(search trigger)": "搜索",
    "Search(search dialog)": "搜索文档",
    "Open Search(search trigger)(aria-label)": "打开搜索",
    "Close Search(search dialog)(aria-label)": "关闭搜索",
    "On this page(table of contents)": "本页目录",
    "Table of Contents(inline table of contents)": "目录",
    "Choose a language(language switcher)": "选择语言",
    "Choose a language(language switcher)(aria-label)": "选择语言",
    "Next page(page footer)": "下一页",
    "Previous page(page footer)": "上一页",
    "Copy Text(code block)(aria-label)": "复制",
    "Copied Text(code block)(aria-label)": "已复制",
    "Edit on GitHub(page actions)": "在 GitHub 上编辑",
  },
  en: {
    displayName: "English",
  },
});
