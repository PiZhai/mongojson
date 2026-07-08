import "./assets/less/index.less";
import "./js/i18n/zh_CN.js";
import VditorMethod from "./method";
import {Constants, VDITOR_VERSION} from "./ts/constants";
import {DevTools} from "./ts/devtools/index";
import {Hint} from "./ts/hint/index";
import {IR} from "./ts/ir/index";
import {input as irInput} from "./ts/ir/input";
import {getHTML} from "./ts/markdown/getHTML";
import {getMarkdown} from "./ts/markdown/getMarkdown";
import {setLute} from "./ts/markdown/setLute";
import {Outline} from "./ts/outline/index";
import {Preview} from "./ts/preview/index";
import {Resize} from "./ts/resize/index";
import {Editor} from "./ts/sv/index";
import {inputEvent} from "./ts/sv/inputEvent";
import {processPaste} from "./ts/sv/process";
import {Tip} from "./ts/tip/index";
import {setEditMode} from "./ts/toolbar/EditMode";
import {Toolbar} from "./ts/toolbar/index";
import {disableToolbar, hidePanel} from "./ts/toolbar/setToolbar";
import {enableToolbar} from "./ts/toolbar/setToolbar";
import {CommandBus} from "./ts/command";
import {initUI, UIUnbindListener} from "./ts/ui/initUI";
import {setCodeTheme} from "./ts/ui/setCodeTheme";
import {setContentTheme} from "./ts/ui/setContentTheme";
import {setPreviewMode} from "./ts/ui/setPreviewMode";
import {setTheme} from "./ts/ui/setTheme";
import {Undo} from "./ts/undo/index";
import {Upload} from "./ts/upload/index";
import {addScript, addScriptSync} from "./ts/util/addScript";
import {getSelectText} from "./ts/util/getSelectText";
import {Options} from "./ts/util/Options";
import {getCursorPosition, getEditorRange, insertHTML} from "./ts/util/selection";
import {afterRenderEvent} from "./ts/wysiwyg/afterRenderEvent";
import {WYSIWYG} from "./ts/wysiwyg/index";
import {input} from "./ts/wysiwyg/input";
import {execAfterRender, insertEmptyBlock} from "./ts/util/fixBrowserBehavior";
import {accessLocalStorage} from "./ts/util/compatibility";
import {setMarkdown} from "./ts/util/setMarkdown";

export type VditorMode = "wysiwyg" | "sv" | "ir";

export interface VditorOutlineEntry {
    id: string;
    level: number;
    line: number;
    text: string;
}

export interface VditorTransaction {
    commandId?: string;
    markdown: string;
    mode: VditorMode;
    source: "input" | "mode" | "set-document" | "insert-value" | "command";
}

export interface VditorDocumentSnapshot {
    html: string;
    markdown: string;
    mode: VditorMode;
    outline: VditorOutlineEntry[];
    selection: string;
}

export interface VditorSetDocumentOptions {
    clearStack?: boolean;
    mode?: VditorMode;
}

type VditorTransactionListener = (transaction: VditorTransaction) => void;

const normalizeOutlineText = (text: string) => text.replace(/\s+/g, " ").trim();

const makeOutlineId = (text: string, line: number, usedIds: Set<string>) => {
    const base = normalizeOutlineText(text)
        .toLowerCase()
        .replace(/[^\p{L}\p{N}]+/gu, "-")
        .replace(/^-+|-+$/g, "") || `heading-${line + 1}`;
    let id = base;
    let index = 1;
    while (usedIds.has(id)) {
        id = `${base}-${index}`;
        index += 1;
    }
    usedIds.add(id);
    return id;
};

const buildOutlineModelFromMarkdown = (markdown: string): VditorOutlineEntry[] => {
    const entries: VditorOutlineEntry[] = [];
    const usedIds = new Set<string>();
    const lines = markdown.split(/\r?\n/);
    let inFence = false;
    let fenceMarker = "";
    let previousParagraph: { line: number; text: string } | null = null;

    lines.forEach((line, lineIndex) => {
        const fenceMatch = /^ {0,3}(`{3,}|~{3,})/.exec(line);
        if (fenceMatch) {
            const marker = fenceMatch[1][0];
            if (!inFence) {
                inFence = true;
                fenceMarker = marker;
            } else if (marker === fenceMarker) {
                inFence = false;
                fenceMarker = "";
            }
            previousParagraph = null;
            return;
        }

        if (inFence) return;

        const atxMatch = /^ {0,3}(#{1,6})\s+(.+?)\s*#*\s*$/.exec(line);
        if (atxMatch) {
            const text = normalizeOutlineText(atxMatch[2]);
            entries.push({
                id: makeOutlineId(text, lineIndex, usedIds),
                level: atxMatch[1].length,
                line: lineIndex,
                text,
            });
            previousParagraph = null;
            return;
        }

        const setextMatch = /^ {0,3}(=+|-+)\s*$/.exec(line);
        if (setextMatch && previousParagraph) {
            const text = normalizeOutlineText(previousParagraph.text);
            entries.push({
                id: makeOutlineId(text, previousParagraph.line, usedIds),
                level: setextMatch[1][0] === "=" ? 1 : 2,
                line: previousParagraph.line,
                text,
            });
            previousParagraph = null;
            return;
        }

        const trimmedLine = line.trim();
        previousParagraph = trimmedLine ? {line: lineIndex, text: trimmedLine} : null;
    });

    return entries;
};

class Vditor extends VditorMethod {
    public readonly version: string;
    public vditor: IVditor;
    private isDestroyed = false;
    private outlineRefreshFrame = 0;
    private outlineSignature = "";
    private transactionListeners = new Set<VditorTransactionListener>();

    /**
     * @param id 要挂载 Vditor 的元素或者元素 ID。
     * @param options Vditor 参数
     */
    constructor(id: string | HTMLElement, options?: IOptions) {
        super();
        this.version = VDITOR_VERSION;

        if (typeof id === "string") {
            if (!options) {
                options = {
                    cache: {
                        id: `vditor${id}`,
                    },
                };
            } else if (!options.cache) {
                options.cache = { id: `vditor${id}` };
            } else if (!options.cache.id) {
                options.cache.id = `vditor${id}`;
            }
            if (!document.getElementById(id)) {
                this.showErrorTip(`Failed to get element by id: ${id}`);
                return;
            }
            id = document.getElementById(id);
        }

        const getOptions = new Options(options);
        const mergedOptions = getOptions.merge();
        const originalInput = mergedOptions.input;
        mergedOptions.input = (value: string) => {
            if (typeof originalInput === "function") {
                originalInput(value);
            }
            this.emitTransaction("input", value);
        };

        // 支持自定义国际化
        if (mergedOptions.i18n) {
            window.VditorI18n = mergedOptions.i18n;
            this.init(id, mergedOptions);
        } else if (mergedOptions.lang === "zh_CN" && window.VditorI18n) {
            this.init(id, mergedOptions);
        } else {
            if (!["de_DE", "en_US", "es_ES", "fr_FR", "ja_JP", "ko_KR", "pt_BR", "ru_RU", "sv_SE", "vi_VN", "zh_CN", "zh_TW"].includes(mergedOptions.lang)) {
                throw new Error(
                    "options.lang error, see https://ld246.com/article/1549638745630#options",
                );
            } else {
                const i18nScriptPrefix = "vditorI18nScript";
                const i18nScriptID = i18nScriptPrefix + mergedOptions.lang;
                document.querySelectorAll(`head script[id^="${i18nScriptPrefix}"]`).forEach((el) => {
                    if (el.id !== i18nScriptID) {
                        document.head.removeChild(el);
                    }
                });
                addScript(`${mergedOptions.cdn}/dist/js/i18n/${mergedOptions.lang}.js`, i18nScriptID).then(() => {
                    this.init(id as HTMLElement, mergedOptions);
                }).catch(() => {
                    this.showErrorTip(`GET ${mergedOptions.cdn}/dist/js/i18n/${mergedOptions.lang}.js net::ERR_ABORTED 404 (Not Found)`);
                });
            }
        }
    }

    private showErrorTip(error: string) {
        const tip = new Tip();
        document.body.appendChild(tip.element);
        tip.show(error, 0);
    }

    public updateToolbarConfig(options: IToolbarConfig) {
        this.vditor.toolbar.updateConfig(this.vditor, options);
    }

    /** 设置主题 */
    public setTheme(
        theme: "dark" | "classic",
        contentTheme?: string,
        codeTheme?: string,
        contentThemePath?: string,
    ) {
        this.vditor.options.theme = theme;
        setTheme(this.vditor);
        if (contentTheme) {
            this.vditor.options.preview.theme.current = contentTheme;
            setContentTheme(contentTheme, contentThemePath || this.vditor.options.preview.theme.path);
        }
        if (codeTheme) {
            this.vditor.options.preview.hljs.style = codeTheme;
            setCodeTheme(codeTheme, this.vditor.options.cdn);
        }
    }

    /** 获取 Markdown 内容 */
    public getValue() {
        return getMarkdown(this.vditor);
    }

    /** 获取编辑器当前编辑模式 */
    public getCurrentMode() {
        return this.vditor.currentMode;
    }

    public setMode(mode: VditorMode) {
        if (this.vditor.currentMode === mode) {
            return;
        }
        setEditMode(this.vditor, mode, getMarkdown(this.vditor));
        this.emitTransaction("mode");
    }

    public onTransaction(listener: VditorTransactionListener) {
        this.transactionListeners.add(listener);
        return () => {
            this.transactionListeners.delete(listener);
        };
    }

    public registerCommand(command: IEditorCommand | IEditorCommand[]) {
        if (!this.vditor?.commandBus) {
            return;
        }
        this.vditor.commandBus.register(command);
    }

    public unregisterCommand(commandId: string) {
        if (!this.vditor?.commandBus) {
            return;
        }
        this.vditor.commandBus.unregister(commandId);
    }

    public resetCommands(commands: IEditorCommand[]) {
        if (!this.vditor?.commandBus) {
            return;
        }
        this.vditor.commandBus.reset(commands);
    }

    public getCommand(commandId: string) {
        if (!this.vditor?.commandBus) {
            return;
        }
        return this.vditor.commandBus.getById(commandId);
    }

    public getAllCommands() {
        if (!this.vditor?.commandBus) {
            return [];
        }
        return this.vditor.commandBus.getAll();
    }

    public executeCommand(
        value: string,
        commandId: string,
        context?: IEditorCommandContext,
    ) {
        if (!this.vditor?.commandBus) {
            return false;
        }

        const command = this.vditor.commandBus.getById(commandId);
        if (!command) {
            return false;
        }
        const executionContext = context || this.getDefaultCommandContext(command);
        if (!executionContext) {
            return false;
        }

        if (typeof this.vditor.options.onEditorCommandExecuted === "function") {
            this.vditor.options.onEditorCommandExecuted(command || null, {
                ...executionContext,
                phase: "before",
                value,
                command: command || null,
            });
        }

        const executed = this.vditor.commandBus.execute(value, command, this.vditor, executionContext);

        if (executed && typeof this.vditor.options.onEditorCommandExecuted === "function") {
            this.vditor.options.onEditorCommandExecuted(command || null, {
                ...executionContext,
                phase: "after",
                value,
                command: command || null,
            });
        }
        if (executed) {
            this.emitTransaction("command", getMarkdown(this.vditor), command.id);
        }

        return executed;
    }

    public getOutlineModel() {
        return buildOutlineModelFromMarkdown(getMarkdown(this.vditor));
    }

    public getSnapshot(): VditorDocumentSnapshot {
        return {
            html: getHTML(this.vditor),
            markdown: getMarkdown(this.vditor),
            mode: this.vditor.currentMode,
            outline: this.getOutlineModel(),
            selection: this.getSelection() || "",
        };
    }

    /** 聚焦到编辑器 */
    public focus() {
        if (this.vditor.currentMode === "sv") {
            this.vditor.sv.element.focus();
        } else if (this.vditor.currentMode === "wysiwyg") {
            this.vditor.wysiwyg.element.focus();
        } else if (this.vditor.currentMode === "ir") {
            this.vditor.ir.element.focus();
        }
    }

    /** 让编辑器失焦 */
    public blur() {
        if (this.vditor.currentMode === "sv") {
            this.vditor.sv.element.blur();
        } else if (this.vditor.currentMode === "wysiwyg") {
            this.vditor.wysiwyg.element.blur();
        } else if (this.vditor.currentMode === "ir") {
            this.vditor.ir.element.blur();
        }
    }

    /** 禁用编辑器 */
    public disabled() {
        hidePanel(this.vditor, ["subToolbar", "hint", "popover"]);
        disableToolbar(
            this.vditor.toolbar.elements,
            Constants.EDIT_TOOLBARS.concat(["undo", "redo", "fullscreen", "edit-mode"]),
        );
        this.vditor[this.vditor.currentMode].element.setAttribute(
            "contenteditable",
            "false",
        );
    }

    /** 解除编辑器禁用 */
    public enable() {
        enableToolbar(
            this.vditor.toolbar.elements,
            Constants.EDIT_TOOLBARS.concat(["undo", "redo", "fullscreen", "edit-mode"]),
        );
        this.vditor.undo.resetIcon(this.vditor);
        this.vditor[this.vditor.currentMode].element.setAttribute("contenteditable", "true");
    }

    /** 返回选中的字符串 */
    public getSelection() {
        if (this.vditor.currentMode === "wysiwyg") {
            return getSelectText(this.vditor.wysiwyg.element);
        } else if (this.vditor.currentMode === "sv") {
            return getSelectText(this.vditor.sv.element);
        } else if (this.vditor.currentMode === "ir") {
            return getSelectText(this.vditor.ir.element);
        }
    }

    /** 设置预览区域内容 */
    public renderPreview(value?: string) {
        this.vditor.preview.render(this.vditor, value);
    }

    public renderPreviewFromMarkdown(markdown: string) {
        this.vditor.preview.renderMarkdown(this.vditor, markdown);
    }

    /** 获取焦点位置 */
    public getCursorPosition() {
        return getCursorPosition(this.vditor[this.vditor.currentMode].element);
    }

    /** 上传是否还在进行中 */
    public isUploading() {
        return this.vditor.upload.isUploading;
    }

    /** 清除缓存 */
    public clearCache() {
        if (this.vditor.options.cache.enable && accessLocalStorage()) {
            localStorage.removeItem(this.vditor.options.cache.id);
        }
    }

    /** 禁用缓存 */
    public disabledCache() {
        this.vditor.options.cache.enable = false;
    }

    /** 启用缓存 */
    public enableCache() {
        if (!this.vditor.options.cache.id) {
            throw new Error(
                "need options.cache.id, see https://ld246.com/article/1549638745630#options",
            );
        }
        this.vditor.options.cache.enable = true;
    }

    /** HTML 转 md */
    public html2md(value: string) {
        return this.vditor.lute.HTML2Md(value);
    }

    /** markdown 转 JSON 输出 */
    public exportJSON(value: string) {
        return this.vditor.lute.RenderJSON(value);
    }

    /** 获取 HTML */
    public getHTML() {
        return getHTML(this.vditor);
    }

    /** 消息提示。time 为 0 将一直显示 */
    public tip(text: string, time?: number) {
        this.vditor.tip.show(text, time);
    }

    /** 设置预览模式 */
    public setPreviewMode(mode: "both" | "editor") {
        setPreviewMode(mode, this.vditor);
    }

    /** 删除选中内容 */
    public deleteValue() {
        if (window.getSelection().isCollapsed) {
            return;
        }
        document.execCommand("delete", false);
    }

    /** 更新选中内容 */
    public updateValue(value: string) {
        document.execCommand("insertHTML", false, value);
    }

    /** 在焦点处插入内容，并默认进行 Markdown 渲染 */
    public insertValue(value: string, render = true) {
        const range = getEditorRange(this.vditor);
        range.collapse(true);
        // https://github.com/Vanessa219/vditor/issues/716
        // https://github.com/Vanessa219/vditor/issues/917
        const tmpElement = document.createElement("template");
        tmpElement.innerHTML = value;
        range.insertNode(tmpElement.content.cloneNode(true));
        range.collapse(false);
        if (this.vditor.currentMode === "sv") {
            this.vditor.sv.preventInput = true;
            if (render) {
                inputEvent(this.vditor);
            }
        } else if (this.vditor.currentMode === "wysiwyg") {
            // 由于 https://github.com/Vanessa219/vditor/issues/1566 不能使用 this.vditor.wysiwyg.preventInput = true;
            if (render) {
                input(this.vditor, getSelection().getRangeAt(0));
            }
        } else if (this.vditor.currentMode === "ir") {
            this.vditor.ir.preventInput = true;
            if (render) {
                irInput(this.vditor, getSelection().getRangeAt(0), true);
            }
        }
        this.emitTransaction("insert-value");
    }

    /** 在焦点处插入 Markdown */
    public insertMD(md: string) {
        // https://github.com/Vanessa219/vditor/issues/1640
        if (this.vditor.currentMode === "ir") {
            insertHTML(this.vditor.lute.Md2VditorIRDOM(md), this.vditor);
        } else if (this.vditor.currentMode === "wysiwyg") {
            insertHTML(this.vditor.lute.Md2VditorDOM(md), this.vditor);
        } else {
            processPaste(this.vditor, md);
        }
        this.vditor.outline.render(this.vditor);
        execAfterRender(this.vditor);
    }

    /** 设置编辑器内容 */
    public setValue(markdown: string, clearStack = false) {
        setMarkdown(this.vditor, markdown, {
            enableAddUndoStack: true,
            enableHint: false,
            enableInput: false,
        });
        this.outlineSignature = this.getOutlineSignature(markdown);

        if (!markdown) {
            hidePanel(this.vditor, ["emoji", "headings", "submenu", "hint"]);
            if (this.vditor.wysiwyg.popover) {
                this.vditor.wysiwyg.popover.style.display = "none";
            }
            this.clearCache();
        }
        if (clearStack) {
            this.clearStack();
        }
        this.emitTransaction("set-document", markdown);
    }

    public setDocument(markdown: string, options: VditorSetDocumentOptions = {}) {
        this.setValue(markdown, options.clearStack ?? true);
        if (options.mode) {
            this.setMode(options.mode);
        }
    }

    /** 空块 */
    public insertEmptyBlock(position: InsertPosition) {
        insertEmptyBlock(this.vditor, position);
    }

    /** 清空 undo & redo 栈 */
    public clearStack() {
        this.vditor.undo.clearStack(this.vditor);
        this.vditor.undo.addToUndoStack(this.vditor);
    }

    /** 销毁编辑器 */
    public destroy() {
        this.vditor.element.innerHTML = this.vditor.originalInnerHTML;
        this.vditor.element.classList.remove("vditor");
        this.vditor.element.removeAttribute("style");
        const iconScript = document.getElementById("vditorIconScript");
        if (iconScript) {
            iconScript.remove();
        }
        if (this.vditor.selectionToolbar) {
            this.vditor.selectionToolbar.destroy();
            this.vditor.selectionToolbar = undefined;
        }
        if (this.vditor.editorTail) {
            this.vditor.editorTail.destroy();
            this.vditor.editorTail = undefined;
        }
        this.vditor.outline?.destroy();
        if (this.outlineRefreshFrame) {
            window.cancelAnimationFrame(this.outlineRefreshFrame);
            this.outlineRefreshFrame = 0;
        }
        this.clearCache();

        UIUnbindListener();
        this.vditor.wysiwyg.unbindListener();
        this.vditor.options.after = undefined;
        this.isDestroyed = true;
    }

    /** 获取评论 ID */
    public getCommentIds() {
        if (this.vditor.currentMode !== "wysiwyg") {
            return [];
        }
        return this.vditor.wysiwyg.getComments(this.vditor, true);
    }

    /** 高亮评论 */
    public hlCommentIds(ids: string[]) {
        if (this.vditor.currentMode !== "wysiwyg") {
            return;
        }
        const hlItem = (item: Element) => {
            item.classList.remove("vditor-comment--hover");
            ids.forEach((id) => {
                if (item.getAttribute("data-cmtids").indexOf(id) > -1) {
                    item.classList.add("vditor-comment--hover");
                }
            });
        };
        this.vditor.wysiwyg.element
            .querySelectorAll(".vditor-comment")
            .forEach((item) => {
                hlItem(item);
            });
        if (this.vditor.preview.element.style.display !== "none") {
            this.vditor.preview.element
                .querySelectorAll(".vditor-comment")
                .forEach((item) => {
                    hlItem(item);
                });
        }
    }

    /** 取消评论高亮 */
    public unHlCommentIds(ids: string[]) {
        if (this.vditor.currentMode !== "wysiwyg") {
            return;
        }
        const unHlItem = (item: Element) => {
            ids.forEach((id) => {
                if (item.getAttribute("data-cmtids").indexOf(id) > -1) {
                    item.classList.remove("vditor-comment--hover");
                }
            });
        };
        this.vditor.wysiwyg.element
            .querySelectorAll(".vditor-comment")
            .forEach((item) => {
                unHlItem(item);
            });
        if (this.vditor.preview.element.style.display !== "none") {
            this.vditor.preview.element
                .querySelectorAll(".vditor-comment")
                .forEach((item) => {
                    unHlItem(item);
                });
        }
    }

    /** 删除评论 */
    public removeCommentIds(removeIds: string[]) {
        if (this.vditor.currentMode !== "wysiwyg") {
            return;
        }

        const removeItem = (item: Element, removeId: string) => {
            const ids = item.getAttribute("data-cmtids").split(" ");
            ids.find((id, index) => {
                if (id === removeId) {
                    ids.splice(index, 1);
                    return true;
                }
            });
            if (ids.length === 0) {
                item.outerHTML = item.innerHTML;
                getEditorRange(this.vditor).collapse(true);
            } else {
                item.setAttribute("data-cmtids", ids.join(" "));
            }
        };
        removeIds.forEach((removeId) => {
            this.vditor.wysiwyg.element
                .querySelectorAll(".vditor-comment")
                .forEach((item) => {
                    removeItem(item, removeId);
                });
            if (this.vditor.preview.element.style.display !== "none") {
                this.vditor.preview.element
                    .querySelectorAll(".vditor-comment")
                    .forEach((item) => {
                        removeItem(item, removeId);
                    });
            }
        });
        afterRenderEvent(this.vditor, {
            enableAddUndoStack: true,
            enableHint: false,
            enableInput: false,
        });
    }

    private getDefaultCommandContext(command: IEditorCommand | undefined | null): IEditorCommandContext | null {
        const selection = window.getSelection();
        if (!selection || !selection.rangeCount) {
            return null;
        }

        const range = selection.getRangeAt(0);
        const lineValue = range.startContainer.textContent.substring(0, range.startOffset) || "";
        const splitChar = command?.trigger || "";
        const lastIndex = splitChar ? lineValue.lastIndexOf(splitChar) : -1;
        const keyword = lastIndex > -1
            ? lineValue.substring(Math.max(0, lastIndex + splitChar.length))
            : "";

        return {
            key: splitChar,
            splitChar,
            keyword,
            lineValue,
            range,
        };
    }

    private init(id: HTMLElement, mergedOptions: IOptions) {
        if (this.isDestroyed) {
            return;
        }
        const commandBus = new CommandBus(mergedOptions.command);
        this.vditor = {
            currentMode: mergedOptions.mode,
            element: id,
            commandBus,
            emitTransaction: (source, options = {}) => {
                this.emitTransaction(source, options.markdown, options.commandId);
            },
            hint: new Hint(mergedOptions.hint.extend, commandBus),
            lute: undefined,
            options: mergedOptions,
            originalInnerHTML: id.innerHTML,
            outline: new Outline(window.VditorI18n.outline),
            tip: new Tip(),
        };

        this.vditor.sv = new Editor(this.vditor);
        this.vditor.undo = new Undo();
        this.vditor.wysiwyg = new WYSIWYG(this.vditor);
        this.vditor.ir = new IR(this.vditor);
        this.vditor.toolbar = new Toolbar(this.vditor);

        if (mergedOptions.resize.enable) {
            this.vditor.resize = new Resize(this.vditor);
        }

        if (this.vditor.toolbar.elements.devtools) {
            this.vditor.devtools = new DevTools();
        }

        if (mergedOptions.upload.url || mergedOptions.upload.handler) {
            this.vditor.upload = new Upload();
        }

        addScript(
            mergedOptions._lutePath ||
            `${mergedOptions.cdn}/dist/js/lute/lute.min.js`,
            "vditorLuteScript",
        ).then(() => {
            this.vditor.lute = setLute({
                autoSpace: this.vditor.options.preview.markdown.autoSpace,
                gfmAutoLink: this.vditor.options.preview.markdown.gfmAutoLink,
                codeBlockPreview: this.vditor.options.preview.markdown
                    .codeBlockPreview,
                emojiSite: this.vditor.options.hint.emojiPath,
                emojis: this.vditor.options.hint.emoji,
                fixTermTypo: this.vditor.options.preview.markdown.fixTermTypo,
                footnotes: this.vditor.options.preview.markdown.footnotes,
                headingAnchor: false,
                inlineMathDigit: this.vditor.options.preview.math.inlineDigit,
                linkBase: this.vditor.options.preview.markdown.linkBase,
                linkPrefix: this.vditor.options.preview.markdown.linkPrefix,
                listStyle: this.vditor.options.preview.markdown.listStyle,
                mark: this.vditor.options.preview.markdown.mark,
                mathBlockPreview: this.vditor.options.preview.markdown
                    .mathBlockPreview,
                paragraphBeginningSpace: this.vditor.options.preview.markdown
                    .paragraphBeginningSpace,
                sanitize: this.vditor.options.preview.markdown.sanitize,
                sub: this.vditor.options.preview.markdown.sub,
                sup: this.vditor.options.preview.markdown.sup,
                toc: this.vditor.options.preview.markdown.toc,
            });

            this.vditor.preview = new Preview(this.vditor);

            initUI(this.vditor);
            this.outlineSignature = this.getOutlineSignature(getMarkdown(this.vditor));

            if (mergedOptions.after) {
                mergedOptions.after();
            }
            if (mergedOptions.icon) {
                // 防止初始化 2 个编辑器时加载 2 次
                addScriptSync(`${mergedOptions.cdn}/dist/js/icons/${mergedOptions.icon}.js`, "vditorIconScript");
            }
        });
    }

    private refreshOutline() {
        if (!this.vditor?.outline) {
            return;
        }
        this.vditor.outline.render(this.vditor);
    }

    private getOutlineSignature(markdown: string) {
        return buildOutlineModelFromMarkdown(markdown)
            .map((entry) => `${entry.line}:${entry.level}:${entry.text}`)
            .join("\n");
    }

    private scheduleOutlineRefresh(markdown: string, force = false) {
        if (!this.vditor?.outline || !this.vditor.options.outline.enable) {
            return;
        }

        const nextSignature = this.getOutlineSignature(markdown);
        if (!force && nextSignature === this.outlineSignature) {
            return;
        }
        this.outlineSignature = nextSignature;

        if (this.outlineRefreshFrame) {
            window.cancelAnimationFrame(this.outlineRefreshFrame);
        }
        this.outlineRefreshFrame = window.requestAnimationFrame(() => {
            this.outlineRefreshFrame = 0;
            this.refreshOutline();
        });
    }

    private emitTransaction(
        source: VditorTransaction["source"],
        markdown?: string,
        commandId?: string,
    ) {
        if (!this.vditor) {
            return;
        }
        const currentMarkdown = markdown ?? getMarkdown(this.vditor);
        this.scheduleOutlineRefresh(currentMarkdown, source === "mode");
        if (this.transactionListeners.size === 0) {
            return;
        }
        const transaction: VditorTransaction = {
            commandId,
            markdown: currentMarkdown,
            mode: this.vditor.currentMode,
            source,
        };
        this.transactionListeners.forEach((listener) => listener(transaction));
    }
}

export default Vditor;
export {markdownSlashCommandDefinitions} from "./ts/command";
