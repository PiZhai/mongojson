import {
    getCodeLanguageLabel,
    getConfiguredCodeLanguages,
    normalizeCodeLanguage,
    PLAIN_TEXT_LANGUAGE,
} from "../util/codeLanguage";
import {getMarkdown} from "./getMarkdown";
import {setMarkdown} from "../util/setMarkdown";

const getCodeLanguage = (code: HTMLElement) => {
    const languageClass = Array.from(code.classList).find((className) => className.startsWith("language-"));
    return languageClass?.replace("language-", "") || PLAIN_TEXT_LANGUAGE;
};

const getRenderedCodeBlockIndex = (code: HTMLElement) => {
    const root = code.closest(".vditor") ?? document;
    const codeBlocks = Array.from(root.querySelectorAll("pre > code")).filter((item): item is HTMLElement => {
        if (!(item instanceof HTMLElement)) {
            return false;
        }
        const parent = item.parentElement;
        return Boolean(parent && !parent.classList.contains("vditor-wysiwyg__pre") &&
            !parent.classList.contains("vditor-ir__marker--pre"));
    });
    return codeBlocks.indexOf(code);
};

export const replaceCodeFenceLanguage = (markdown: string, targetIndex: number, nextLanguage: string) => {
    const lines = markdown.split("\n");
    let blockIndex = -1;
    let fenceMarker = "";
    let fenceLength = 0;
    let inFence = false;

    for (let index = 0; index < lines.length; index += 1) {
        const line = lines[index];
        if (!inFence) {
            const openMatch = /^(\s*)(`{3,}|~{3,})(.*)$/.exec(line);
            if (!openMatch) {
                continue;
            }

            blockIndex += 1;
            fenceMarker = openMatch[2][0];
            fenceLength = openMatch[2].length;
            inFence = true;

            if (blockIndex === targetIndex) {
                const nextInfo = nextLanguage === PLAIN_TEXT_LANGUAGE ? "" : nextLanguage;
                lines[index] = `${openMatch[1]}${openMatch[2]}${nextInfo}`;
            }
            continue;
        }

        const closePattern = new RegExp(`^\\s*\\${fenceMarker}{${fenceLength},}\\s*$`);
        if (closePattern.test(line)) {
            inFence = false;
            fenceMarker = "";
            fenceLength = 0;
        }
    }

    return blockIndex >= targetIndex ? lines.join("\n") : markdown;
};

export const renderCodeLanguageMenu = (
    code: HTMLElement,
    menuElement: HTMLElement,
    option: IHljs,
    vditor: IVditor,
) => {
    const currentLanguage = getCodeLanguage(code);
    const stopMenuEvent = (event: Event) => {
        event.stopPropagation();
    };

    const details = document.createElement("details");
    details.className = "vditor-code-language-menu";
    details.onclick = stopMenuEvent;
    details.onkeydown = stopMenuEvent;
    details.onmousedown = stopMenuEvent;

    const summary = document.createElement("summary");
    summary.className = "vditor-code-language-trigger";
    summary.setAttribute("aria-label", "代码块格式");
    summary.textContent = getCodeLanguageLabel(currentLanguage);
    details.appendChild(summary);

    const panel = document.createElement("div");
    panel.className = "vditor-code-language-panel";
    panel.onmousedown = stopMenuEvent;
    panel.onclick = stopMenuEvent;
    details.appendChild(panel);

    const searchInput = document.createElement("input");
    searchInput.className = "vditor-code-language-search";
    searchInput.type = "text";
    searchInput.autocomplete = "off";
    searchInput.spellcheck = false;
    searchInput.setAttribute("aria-label", "搜索或输入代码块格式");
    searchInput.placeholder = "搜索或输入格式";
    searchInput.value = currentLanguage === PLAIN_TEXT_LANGUAGE ? "" : currentLanguage;
    ["beforeinput", "compositionstart", "compositionend", "focus", "focusin", "keyup", "paste"].forEach((eventName) => {
        searchInput.addEventListener(eventName, stopMenuEvent);
    });
    panel.appendChild(searchInput);

    const list = document.createElement("div");
    list.className = "vditor-code-language-list";
    list.setAttribute("role", "listbox");
    panel.appendChild(list);

    const restoreEditorFocus = () => {
        window.requestAnimationFrame(() => {
            vditor[vditor.currentMode]?.element?.focus();
        });
    };

    const applyLanguage = (language: string) => {
        const blockIndex = getRenderedCodeBlockIndex(code);
        if (blockIndex < 0) {
            restoreEditorFocus();
            return;
        }
        const currentMarkdown = getMarkdown(vditor);
        const nextMarkdown = replaceCodeFenceLanguage(currentMarkdown, blockIndex, language);
        if (nextMarkdown === currentMarkdown) {
            restoreEditorFocus();
            return;
        }
        setMarkdown(vditor, nextMarkdown, {
            enableAddUndoStack: true,
            enableAfterRender: true,
            enableHint: false,
            enableInput: true,
        });
        restoreEditorFocus();
    };

    const renderOptions = () => {
        list.replaceChildren();
        const searchValue = searchInput.value.trim().toLowerCase();
        const availableLanguages = getConfiguredCodeLanguages(option, true);
        if (!availableLanguages.includes(currentLanguage)) {
            availableLanguages.unshift(currentLanguage);
        }
        const filteredLanguages = availableLanguages.filter((language) => {
            if (!searchValue) {
                return true;
            }
            return language.includes(searchValue) || getCodeLanguageLabel(language).includes(searchValue);
        });
        const typedLanguage = normalizeCodeLanguage(searchInput.value);
        const visibleLanguages = typedLanguage && !filteredLanguages.includes(typedLanguage)
            ? [typedLanguage, ...filteredLanguages]
            : filteredLanguages;

        if (visibleLanguages.length === 0) {
            const empty = document.createElement("div");
            empty.className = "vditor-code-language-empty";
            empty.textContent = "回车使用当前输入";
            list.appendChild(empty);
            return;
        }

        visibleLanguages.forEach((language) => {
            const item = document.createElement("button");
            item.className = `vditor-code-language-option${language === currentLanguage ? " vditor-code-language-option-active" : ""}`;
            item.setAttribute("role", "option");
            item.setAttribute("aria-selected", String(language === currentLanguage));
            item.textContent = getCodeLanguageLabel(language);
            item.type = "button";
            item.onclick = (event) => {
                event.preventDefault();
                event.stopPropagation();
                applyLanguage(language);
                details.open = false;
            };
            list.appendChild(item);
        });
    };

    details.ontoggle = () => {
        menuElement.classList.toggle("vditor-code-language-menu-active", details.open);
        if (!details.open) {
            return;
        }
        searchInput.value = currentLanguage === PLAIN_TEXT_LANGUAGE ? "" : currentLanguage;
        renderOptions();
        window.requestAnimationFrame(() => {
            searchInput.focus();
            searchInput.select();
        });
    };

    searchInput.oninput = (event) => {
        event.stopPropagation();
        renderOptions();
    };
    searchInput.onkeydown = (event) => {
        event.stopPropagation();
        if (event.key === "Enter") {
            event.preventDefault();
            const currentOption = list.querySelector<HTMLButtonElement>(".vditor-code-language-option");
            const nextLanguage = currentOption?.textContent
                ? normalizeCodeLanguage(currentOption.textContent)
                : normalizeCodeLanguage(searchInput.value);
            applyLanguage(nextLanguage);
            details.open = false;
            return;
        }
        if (event.key === "Escape") {
            event.preventDefault();
            details.open = false;
            summary.focus();
            return;
        }
        if (event.key === "ArrowDown") {
            const currentOption = list.querySelector<HTMLButtonElement>(".vditor-code-language-option");
            if (currentOption) {
                event.preventDefault();
                currentOption.focus();
            }
        }
    };

    const separator = document.createElement("i");
    separator.className = "vditor-code-language-separator";
    separator.setAttribute("aria-hidden", "true");
    separator.textContent = "|";

    const hiddenTextarea = menuElement.querySelector("textarea");
    hiddenTextarea?.insertAdjacentElement("beforebegin", details);
    details.insertAdjacentElement("afterend", separator);
};
