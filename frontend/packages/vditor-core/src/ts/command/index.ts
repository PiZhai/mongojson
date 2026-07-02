import {fixBlockquote, fixList, fixTable, execAfterRender} from "../util/fixBrowserBehavior";
import {hasClosestByAttribute, hasClosestByMatchTag, hasClosestBlock} from "../util/hasClosest";
import {processPaste} from "../sv/process";
import {insertHTML, setRangeByWbr, setSelectionFocus} from "../util/selection";

export class CommandBus {
    private commands: IEditorCommand[] = [];

    constructor(commands: IEditorCommand[] = []) {
        this.register(commands);
    }

    public register(command: IEditorCommand | IEditorCommand[]) {
        if (Array.isArray(command)) {
            command.forEach((item) => {
                this.register(item);
            });
            return;
        }

        if (!command || !command.id) {
            return;
        }

        const existedIndex = this.commands.findIndex((item) => item.id === command.id);
        if (existedIndex === -1) {
            this.commands.push(command);
            return;
        }

        this.commands[existedIndex] = command;
    }

    public unregister(commandId: string) {
        if (!commandId) {
            return;
        }
        this.commands = this.commands.filter((item) => item.id !== commandId);
    }

    public reset(commands: IEditorCommand[]) {
        this.commands = [];
        this.register(commands);
    }

    public getAll() {
        return this.commands.slice(0);
    }

    public getById(commandId: string) {
        if (!commandId) {
            return;
        }
        return this.commands.find((item) => item.id === commandId);
    }

    public resolve(context: IEditorCommandContext, vditor: IVditor) {
        if (!context.splitChar) {
            return [];
        }

        return this.commands.filter((command) => {
            if (command.trigger !== context.splitChar) {
                return false;
            }
            if (command.visible && !command.visible(context, vditor)) {
                return false;
            }
            if (command.canInvoke && !command.canInvoke(context, vditor)) {
                return false;
            }
            if (command.matcher) {
                return command.matcher(context.keyword, vditor, context);
            }

            const normalizedKeyword = context.keyword.toLowerCase();
            const matchPool = [
                command.id,
                ...command.keywords,
                command.description,
                command.detail,
            ].filter(Boolean).join(" ").toLowerCase();
            return normalizedKeyword.length === 0 || matchPool.indexOf(normalizedKeyword) > -1;
        });
    }

    public execute(
        value: string,
        command: IEditorCommand | null | undefined,
        vditor: IVditor,
        context: IEditorCommandContext,
    ) {
        if (!command || typeof command.execute !== "function") {
            return false;
        }
        const executed = command.execute(value, vditor, context) as boolean | undefined;
        if (typeof executed === "boolean") {
            return executed;
        }
        return true;
    }
}

type MemoSlashCommand = {
    id: string;
    category: "基础" | "常用";
    icon: string;
    keywords: string[];
    label: string;
    value: string;
};

const clearSlashInput = (context: IEditorCommandContext): boolean => {
    if (context.key !== "/") {
        return true;
    }

    const range = context.range.cloneRange();
    const startOffset = range.startOffset - context.key.length - context.keyword.length;
    if (startOffset < 0) {
        return false;
    }

    try {
        range.setStart(range.startContainer, startOffset);
        range.deleteContents();
        setSelectionFocus(range);
    } catch {
        return false;
    }
    return true;
};

type MemoSlashCommandExecute = (value: string, vditor: IVditor, context: IEditorCommandContext) => boolean;

const getNormalizedSlashText = (value: string) => value
    .replace(/\u200b/g, "")
    .replace(/\u00a0/g, " ")
    .trim();

const isStandaloneSlashTrigger = (context: IEditorCommandContext) => {
    const blockElement = hasClosestBlock(context.range.startContainer);
    if (!blockElement || context.key !== "/") {
        return false;
    }
    return getNormalizedSlashText(blockElement.textContent || "") === `${context.key}${context.keyword}`;
};

const getMarkdownHTML = (vditor: IVditor, markdown: string) => {
    if (vditor.currentMode === "ir") {
        return vditor.lute.Md2VditorIRDOM(markdown);
    }
    if (vditor.currentMode === "wysiwyg") {
        return vditor.lute.Md2VditorDOM(markdown);
    }
    return "";
};

const insertCodeWbr = (codeElement: HTMLElement) => {
    const codeHTML = codeElement.innerHTML;
    const codeText = codeElement.textContent || "";
    if (codeText === "" || codeText === "\n") {
        codeElement.innerHTML = "\u200b<wbr>\n";
        return;
    }
    if (codeHTML.endsWith("\n")) {
        codeElement.innerHTML = `${codeHTML.slice(0, -1)}<wbr>\n`;
    } else {
        codeElement.insertAdjacentHTML("beforeend", "<wbr>");
    }
};

const markSlashInsertedIRCodeBlock = (pasteElement: HTMLElement, vditor: IVditor) => {
    if (vditor.currentMode !== "ir") {
        return false;
    }
    const codeBlockElement = pasteElement.lastElementChild as HTMLElement | null;
    if (!codeBlockElement || codeBlockElement.getAttribute("data-type") !== "code-block") {
        return false;
    }

    const codeElement = codeBlockElement.querySelector<HTMLElement>(".vditor-ir__marker--pre code");
    if (!codeElement) {
        return false;
    }

    insertCodeWbr(codeElement);
    codeBlockElement.classList.add("vditor-ir__node--expand");
    codeBlockElement.classList.remove("vditor-ir__node--hidden");
    return true;
};

const replaceSlashBlockWithMarkdown = (vditor: IVditor, markdown: string, context: IEditorCommandContext) => {
    const blockElement = hasClosestBlock(context.range.startContainer);
    if (!blockElement || vditor.currentMode === "sv") {
        return false;
    }

    const pasteElement = document.createElement("div");
    pasteElement.innerHTML = getMarkdownHTML(vditor, markdown);
    if (!pasteElement.firstElementChild || pasteElement.firstElementChild.getAttribute("data-block") !== "0") {
        return false;
    }

    if (!markSlashInsertedIRCodeBlock(pasteElement, vditor)) {
        pasteElement.lastElementChild.insertAdjacentHTML("beforeend", "<wbr>");
    }

    const range = context.range.cloneRange();
    blockElement.replaceWith(...Array.from(pasteElement.childNodes));
    setRangeByWbr(vditor[vditor.currentMode].element, range);
    vditor.outline.render(vditor);
    execAfterRender(vditor);
    return true;
};

const insertMarkdown = (vditor: IVditor, markdown: string): boolean => {
    if (!markdown) {
        return true;
    }

    if (vditor.currentMode === "ir") {
        insertHTML(vditor.lute.Md2VditorIRDOM(markdown), vditor);
    } else if (vditor.currentMode === "wysiwyg") {
        insertHTML(vditor.lute.Md2VditorDOM(markdown), vditor);
    } else {
        processPaste(vditor, markdown);
    }
    vditor.outline.render(vditor);
    execAfterRender(vditor);
    return true;
};

const insertSlashCommandMarkdown = (value: string, vditor: IVditor, context: IEditorCommandContext) => {
    const shouldReplaceTriggerBlock = isStandaloneSlashTrigger(context);
    if (shouldReplaceTriggerBlock && replaceSlashBlockWithMarkdown(vditor, value, context)) {
        return true;
    }
    if (!clearSlashInput(context)) {
        return false;
    }
    return insertMarkdown(vditor, value);
};

const getBlockCommandHooks = (commandId: string): Pick<IEditorCommand, "onEnter" | "onBackspace"> => {
    if (commandId === "ordered-list" || commandId === "bullet-list" || commandId === "todo") {
        return {
            onEnter(vditor: IVditor, context: IEditorCommandContext, event: KeyboardEvent) {
                const commandRange = context.range || getEditorRange(vditor);
                return fixList(commandRange, vditor, hasClosestByMatchTag(commandRange.startContainer, "P"), event);
            },
            onBackspace(vditor: IVditor, context: IEditorCommandContext, event: KeyboardEvent) {
                const commandRange = context.range || getEditorRange(vditor);
                return fixList(commandRange, vditor, hasClosestByMatchTag(commandRange.startContainer, "P"), event);
            },
        };
    }

    if (commandId === "quote") {
        return {
            onEnter(vditor: IVditor, context: IEditorCommandContext, event: KeyboardEvent) {
                const commandRange = context.range || getEditorRange(vditor);
                return fixBlockquote(vditor, commandRange, event, hasClosestByMatchTag(commandRange.startContainer, "P"));
            },
            onBackspace(vditor: IVditor, context: IEditorCommandContext, event: KeyboardEvent) {
                const commandRange = context.range || getEditorRange(vditor);
                return fixBlockquote(vditor, commandRange, event, hasClosestByMatchTag(commandRange.startContainer, "P"));
            },
        };
    }

    if (commandId === "table") {
        return {
            onEnter(vditor: IVditor, context: IEditorCommandContext, event: KeyboardEvent) {
                const commandRange = context.range || getEditorRange(vditor);
                return fixTable(vditor, event, commandRange);
            },
            onBackspace(vditor: IVditor, context: IEditorCommandContext, event: KeyboardEvent) {
                const commandRange = context.range || getEditorRange(vditor);
                return fixTable(vditor, event, commandRange);
            },
        };
    }

    return {};
};

const getEditorRange = (vditor: IVditor) => {
    const selection = window.getSelection();
    if (!selection || !selection.rangeCount) {
        return vditor[vditor.currentMode].element.firstElementChild.ownerDocument.createRange();
    }
    return selection.getRangeAt(0).cloneRange();
};

const buildMemoSlashCommandExecutor = (commandId: string): MemoSlashCommandExecute | undefined => {
    if (commandId === "paragraph") {
        return (_value: string, _vditor: IVditor, context: IEditorCommandContext) => {
            return clearSlashInput(context);
        };
    }
    if (commandId === "h1") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "h2") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "h3") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "h4") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "ordered-list") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "bullet-list") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "code-block") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "quote") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "hr") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "todo") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "table") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "link") {
        return insertSlashCommandMarkdown;
    }
    if (commandId === "image") {
        return insertSlashCommandMarkdown;
    }
    return (_value: string, _vditor: IVditor, context: IEditorCommandContext) => {
        if (!clearSlashInput(context)) {
            return false;
        }
        return false;
    };
};

const memoSlashCommands: MemoSlashCommand[] = [
    {
        id: "paragraph",
        category: "基础",
        icon: "T",
        keywords: ["text", "paragraph", "wenben", "duanluo", "文本", "段落", "正文"],
        label: "文本",
        value: "",
    },
    {
        id: "h1",
        category: "基础",
        icon: "H1",
        keywords: ["h1", "heading1", "title", "biaoti", "yiji", "标题", "一级标题", "H1", "标题一"],
        label: "一级标题",
        value: "# 一级标题",
    },
    {
        id: "h2",
        category: "基础",
        icon: "H2",
        keywords: ["h2", "heading2", "title", "biaoti", "erji", "标题", "二级标题", "H2", "标题二"],
        label: "二级标题",
        value: "## 二级标题",
    },
    {
        id: "h3",
        category: "基础",
        icon: "H3",
        keywords: ["h3", "heading3", "title", "biaoti", "sanji", "标题", "三级标题", "H3", "标题三"],
        label: "三级标题",
        value: "### 三级标题",
    },
    {
        id: "h4",
        category: "基础",
        icon: "H4",
        keywords: ["h4", "heading4", "title", "biaoti", "siji", "标题", "四级标题", "H4", "标题四"],
        label: "四级标题",
        value: "#### 四级标题",
    },
    {
        id: "ordered-list",
        category: "基础",
        icon: "1.",
        keywords: ["ordered", "number", "list", "youxu", "liebiao", "有序列表", "列表", "序号列表"],
        label: "有序列表",
        value: "1. 列表项",
    },
    {
        id: "bullet-list",
        category: "基础",
        icon: "-",
        keywords: ["bullet", "unordered", "list", "wuxu", "liebiao", "无序列表", "列表", "要点"],
        label: "无序列表",
        value: "- 列表项",
    },
    {
        id: "code-block",
        category: "基础",
        icon: "{}",
        keywords: ["code", "block", "daima", "代码块", "代码", "块"],
        label: "代码块",
        value: "```\n```",
    },
    {
        id: "quote",
        category: "基础",
        icon: ">",
        keywords: ["quote", "blockquote", "yinyong", "引用", "引用块"],
        label: "引用",
        value: "> 引用",
    },
    {
        id: "hr",
        category: "基础",
        icon: "--",
        keywords: ["line", "divider", "hr", "fengexian", "分割线", "水平线", "横线"],
        label: "分割线",
        value: "---",
    },
    {
        id: "todo",
        category: "常用",
        icon: "[ ]",
        keywords: ["task", "todo", "check", "renwu", "任务", "待办", "任务列表"],
        label: "任务",
        value: "- [ ] 任务",
    },
    {
        id: "link",
        category: "常用",
        icon: "url",
        keywords: ["link", "url", "lianjie", "链接", "超链接"],
        label: "链接",
        value: "[链接文本](https://)",
    },
    {
        id: "image",
        category: "常用",
        icon: "img",
        keywords: ["image", "photo", "tupian", "图片", "图像"],
        label: "图片",
        value: "![图片描述]()",
    },
    {
        id: "table",
        category: "常用",
        icon: "tbl",
        keywords: ["table", "biaoge", "表格", "表"],
        label: "表格",
        value: "| 列 A | 列 B |\n| --- | --- |\n| 内容 | 内容 |",
    },
];

const escapeHTML = (value: string) => value
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

const renderSlashCommand = (command: MemoSlashCommand) => [
    '<span class="memo-slash-command">',
    `<span class="memo-slash-command-icon">${escapeHTML(command.icon)}</span>`,
    '<span class="memo-slash-command-text">',
    `<span class="memo-slash-command-category">${escapeHTML(command.category)}</span>`,
    `<span class="memo-slash-command-label">${escapeHTML(command.label)}</span>`,
    "</span>",
    "</span>",
].join("");

const isInCodeBlock = (context: IEditorCommandContext) => {
    return !!hasClosestByAttribute(context.range.startContainer, "data-type", "code-block") ||
        !!hasClosestByAttribute(context.range.startContainer, "data-type", "code-block-info") ||
        !!hasClosestByAttribute(context.range.startContainer, "data-type", "code-block-open-marker") ||
        !!hasClosestByAttribute(context.range.startContainer, "data-type", "code-block-close-marker");
};

const isInTableContext = (context: IEditorCommandContext) => {
    return !!hasClosestByMatchTag(context.range.startContainer, "TABLE") ||
        !!hasClosestByMatchTag(context.range.startContainer, "TD") ||
        !!hasClosestByMatchTag(context.range.startContainer, "TH");
};

const canInvokeBlockTypeCommand = (context: IEditorCommandContext) => {
    return isInBlockLevelContext(context) && !isInTableContext(context);
};

const isInBlockLevelContext = (context: IEditorCommandContext) => {
    if (isInCodeBlock(context)) {
        return false;
    }
    if (!hasClosestBlock(context.range.startContainer)) {
        return false;
    }
    return true;
};

const isBlockTypeCommand = (commandId: string) => {
    const blockLevelCommandIds = ["ordered-list", "bullet-list", "code-block", "quote", "todo", "table"];
    return blockLevelCommandIds.indexOf(commandId) > -1;
};

const canInvokeSlashCommand = (context: IEditorCommandContext) => !isInCodeBlock(context);

export const memoSlashCommandDefinitions: IEditorCommand[] = memoSlashCommands.map((command) => ({
    id: command.id,
    trigger: "/",
    keywords: command.keywords,
    value: command.value,
    canInvoke: isBlockTypeCommand(command.id) ? canInvokeBlockTypeCommand : canInvokeSlashCommand,
    icon: command.icon,
    description: command.label,
    detail: command.category,
    execute: buildMemoSlashCommandExecutor(command.id) as MemoSlashCommandExecute,
    ...getBlockCommandHooks(command.id),
    hint: {
        html: renderSlashCommand(command),
        value: command.value,
        icon: command.icon,
        description: command.label,
        detail: command.category,
        keywords: command.keywords,
    },
}));
