import {Constants} from "../constants";
import {getMarkdown} from "../markdown/getMarkdown";
import {execAfterRender, insertEmptyBlock} from "../util/fixBrowserBehavior";
import {setMarkdown} from "../util/setMarkdown";

const DEFAULT_TAIL_LINES = 3;
const DEFAULT_SINGLE_CLICK_DELAY = 260;

const getActiveEditorElement = (vditor: IVditor) => vditor[vditor.currentMode]?.element ?? null;

const getEditorLineHeight = (editorElement: HTMLElement) => {
    const styles = window.getComputedStyle(editorElement);
    const lineHeight = Number.parseFloat(styles.lineHeight);
    if (Number.isFinite(lineHeight) && lineHeight > 0) {
        return lineHeight;
    }

    const fontSize = Number.parseFloat(styles.fontSize);
    return Number.isFinite(fontSize) && fontSize > 0 ? fontSize * 1.65 : 22;
};

const getLastEditorContentBlock = (editorElement: HTMLElement) => {
    const blocks = Array.from(editorElement.querySelectorAll<HTMLElement>("[data-block='0']"))
        .filter((block) => editorElement.contains(block));
    return blocks.length > 0 ? blocks[blocks.length - 1] : null;
};

const isEmptyEditorBlock = (block: HTMLElement) => {
    return (block.textContent ?? "").replaceAll(Constants.ZWSP, "").trim().length === 0;
};

const getEditorTailBlocks = (editorElement: HTMLElement) => {
    const blocks = Array.from(editorElement.children)
        .filter((child): child is HTMLElement => {
            return child instanceof HTMLElement && child.dataset.block === "0";
        });
    let firstTailIndex = blocks.length;

    while (firstTailIndex > 0 && isEmptyEditorBlock(blocks[firstTailIndex - 1])) {
        firstTailIndex -= 1;
    }

    return {
        blocks,
        tailBlocks: blocks.slice(firstTailIndex),
        tailStartBlock: firstTailIndex > 0 ? blocks[firstTailIndex - 1] : null,
    };
};

export const getTailClickLine = (clientY: number, lastContentBottom: number, lineHeight: number, maxLines: number) => {
    if (clientY <= lastContentBottom || lineHeight <= 0) {
        return null;
    }

    return Math.max(1, Math.min(maxLines, Math.floor((clientY - lastContentBottom) / lineHeight) + 1));
};

const getEditorTailClickLine = (vditor: IVditor, event: MouseEvent, maxLines: number) => {
    if (event.button !== 0) {
        return null;
    }

    const editorElement = getActiveEditorElement(vditor);
    if (!(event.target instanceof Node) || !editorElement) {
        return null;
    }

    const contentElement = vditor.element.querySelector<HTMLElement>(".vditor-content");
    if (!editorElement.contains(event.target) && !contentElement?.contains(event.target)) {
        return null;
    }

    const editorRect = editorElement.getBoundingClientRect();
    if (
        event.clientX < editorRect.left ||
        event.clientX > editorRect.right ||
        event.clientY < editorRect.top ||
        event.clientY > editorRect.bottom
    ) {
        return null;
    }

    const lastBlock = getLastEditorContentBlock(editorElement);
    const contentBottom = lastBlock?.getBoundingClientRect().bottom ?? editorElement.getBoundingClientRect().top;
    return getTailClickLine(event.clientY, contentBottom, getEditorLineHeight(editorElement), maxLines);
};

const collapseSelectionToElementEnd = (editorElement: HTMLElement) => {
    const selection = window.getSelection();
    if (!selection) {
        return;
    }

    const range = document.createRange();
    const lastBlock = getLastEditorContentBlock(editorElement);
    const target = lastBlock ?? editorElement;
    range.selectNodeContents(target);
    range.collapse(false);
    selection.removeAllRanges();
    selection.addRange(range);
};

const collapseSelectionToBlockStart = (block: HTMLElement) => {
    const selection = window.getSelection();
    if (!selection) {
        return;
    }

    const range = document.createRange();
    const textNode = Array.from(block.childNodes).find((node) => node.nodeType === Node.TEXT_NODE);
    if (textNode) {
        range.setStart(textNode, 0);
    } else {
        range.setStart(block, 0);
    }
    range.collapse(true);
    selection.removeAllRanges();
    selection.addRange(range);
};

const countTrailingNewlines = (markdown: string) => {
    const match = /\n*$/.exec(markdown);
    return match?.[0].length ?? 0;
};

export const ensureTrailingNewlines = (markdown: string, targetLine: number, maxLines: number) => {
    if (markdown.length === 0) {
        return targetLine <= 1 ? markdown : "\n".repeat(Math.min(maxLines, targetLine) - 1);
    }

    const normalizedTarget = Math.max(1, Math.min(maxLines, Math.floor(targetLine)));
    const currentTrailingNewlines = countTrailingNewlines(markdown);
    if (currentTrailingNewlines >= normalizedTarget) {
        return markdown;
    }

    return `${markdown}${"\n".repeat(normalizedTarget - currentTrailingNewlines)}`;
};

export class EditorTail {
    private clickTimerId = 0;
    private readonly maxLines: number;
    private readonly singleClickDelay: number;
    private readonly ignoreSelector?: string;

    constructor(private readonly vditor: IVditor) {
        const options = this.vditor.options.editorTail;
        this.maxLines = Math.max(1, Math.floor(options?.lines ?? DEFAULT_TAIL_LINES));
        this.singleClickDelay = Math.max(0, Math.floor(options?.singleClickDelay ?? DEFAULT_SINGLE_CLICK_DELAY));
        this.ignoreSelector = options?.ignoreSelector;
        this.applyTailSpacing();
        this.vditor.element.addEventListener("click", this.handleClick, true);
        this.vditor.element.addEventListener("dblclick", this.handleDoubleClick, true);
    }

    public destroy() {
        this.clearClickTimer();
        this.vditor.element.classList.remove("vditor--editor-tail");
        this.vditor.element.style.removeProperty("--vditor-editor-tail-height");
        this.vditor.element.removeEventListener("click", this.handleClick, true);
        this.vditor.element.removeEventListener("dblclick", this.handleDoubleClick, true);
    }

    private applyTailSpacing() {
        const editorElement = getActiveEditorElement(this.vditor);
        const lineHeight = editorElement ? getEditorLineHeight(editorElement) : 22;
        this.vditor.element.classList.add("vditor--editor-tail");
        this.vditor.element.style.setProperty(
            "--vditor-editor-tail-height",
            `${Math.ceil(lineHeight * this.maxLines)}px`,
        );
    }

    private clearClickTimer() {
        if (!this.clickTimerId) {
            return;
        }
        window.clearTimeout(this.clickTimerId);
        this.clickTimerId = 0;
    }

    private shouldIgnoreTarget(target: EventTarget | null) {
        if (!(target instanceof HTMLElement)) {
            return false;
        }
        return Boolean(target.closest(".vditor-code-language-menu") ||
            (this.ignoreSelector && target.closest(this.ignoreSelector)));
    }

    private handleClick = (event: MouseEvent) => {
        if (this.shouldIgnoreTarget(event.target)) {
            return;
        }
        if (event.detail > 1) {
            this.clearClickTimer();
            return;
        }

        const targetLine = getEditorTailClickLine(this.vditor, event, this.maxLines);
        if (!targetLine) {
            return;
        }

        event.preventDefault();
        event.stopPropagation();
        event.stopImmediatePropagation();
        this.clearClickTimer();
        this.clickTimerId = window.setTimeout(() => {
            this.clickTimerId = 0;
            this.continueFromTail(1);
        }, this.singleClickDelay);
    };

    private handleDoubleClick = (event: MouseEvent) => {
        if (this.shouldIgnoreTarget(event.target)) {
            return;
        }

        const targetLine = getEditorTailClickLine(this.vditor, event, this.maxLines);
        if (!targetLine) {
            return;
        }

        event.preventDefault();
        event.stopPropagation();
        event.stopImmediatePropagation();
        this.clearClickTimer();
        this.continueFromTail(targetLine);
    };

    private continueFromTail(targetLine: number) {
        if (this.vditor.currentMode === "sv") {
            this.continueFromSourceTail(targetLine);
            return;
        }
        this.continueFromBlockTail(targetLine);
    }

    private continueFromSourceTail(targetLine: number) {
        const currentMarkdown = getMarkdown(this.vditor);
        const nextMarkdown = ensureTrailingNewlines(currentMarkdown, targetLine, this.maxLines);
        if (nextMarkdown !== currentMarkdown) {
            setMarkdown(this.vditor, nextMarkdown, {
                enableAddUndoStack: true,
                enableAfterRender: true,
                enableHint: false,
                enableInput: true,
            });
        }

        window.requestAnimationFrame(() => {
            this.vditor.sv.element.focus();
            collapseSelectionToElementEnd(this.vditor.sv.element);
        });
    }

    private continueFromBlockTail(targetLine: number) {
        const editorElement = getActiveEditorElement(this.vditor);
        if (!editorElement) {
            return;
        }
        const normalizedLine = Math.max(1, Math.floor(targetLine));
        let {tailBlocks} = getEditorTailBlocks(editorElement);

        if (tailBlocks.length < normalizedLine) {
            collapseSelectionToElementEnd(editorElement);
        }
        while (tailBlocks.length < normalizedLine) {
            insertEmptyBlock(this.vditor, "afterend");
            tailBlocks = getEditorTailBlocks(editorElement).tailBlocks;
        }

        const targetBlock = tailBlocks[normalizedLine - 1] ?? tailBlocks[tailBlocks.length - 1];
        editorElement.focus();
        if (targetBlock) {
            collapseSelectionToBlockStart(targetBlock);
        } else {
            collapseSelectionToElementEnd(editorElement);
        }

        const markdown = getMarkdown(this.vditor);
        execAfterRender(this.vditor);
        if (typeof this.vditor.options.input === "function") {
            this.vditor.options.input(markdown);
        }
    }
}
