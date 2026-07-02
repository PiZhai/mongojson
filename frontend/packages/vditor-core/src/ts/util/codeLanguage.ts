import {Constants} from "../constants";

type WindowWithHljs = Window & {
    hljs?: {
        listLanguages?: () => string[];
    };
};

export const PLAIN_TEXT_LANGUAGE = "plaintext";

export const getCodeLanguageLabel = (language: string) => {
    return language === PLAIN_TEXT_LANGUAGE ? "text" : language;
};

export const normalizeCodeLanguage = (value: string) => {
    const language = value.trim().toLowerCase();
    return language === "" || language === "text" ? PLAIN_TEXT_LANGUAGE : language;
};

export const getConfiguredCodeLanguages = (
    options: Pick<IHljs, "langs"> = {},
    includePlainText = false,
) => {
    const hljsLanguages = ((window as WindowWithHljs).hljs?.listLanguages?.() ?? []).filter(Boolean);
    const languages = new Set<string>([
        ...(includePlainText ? [PLAIN_TEXT_LANGUAGE] : []),
        ...(options.langs || []),
        ...Constants.ALIAS_CODE_LANGUAGES,
        ...hljsLanguages,
    ]);
    languages.delete("");

    return Array.from(languages).sort((left, right) => left.localeCompare(right));
};

export const getFilteredCodeLanguages = (
    key: string,
    options: Pick<IHljs, "langs"> = {},
    includePlainText = false,
) => {
    const normalizedKey = key.toLowerCase();
    return getConfiguredCodeLanguages(options, includePlainText).filter((language) => {
        return language.includes(normalizedKey) || getCodeLanguageLabel(language).includes(normalizedKey);
    });
};
