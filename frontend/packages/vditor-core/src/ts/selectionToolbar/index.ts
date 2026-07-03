import {getEventName} from "../util/compatibility";

interface ISelectionToolbarRuntimeContext {
    action?: ISelectionToolbarAction;
    value: string;
}

const DEFAULT_SELECTION_TOOLBAR_OFFSET = 12;

export class SelectionToolbar {
    public element: HTMLDivElement;
    private readonly actions: ISelectionToolbarAction[];
    private readonly offset: number;
    private readonly canInvoke?: ISelectionToolbarConfig["canInvoke"];
    private readonly onAction?: ISelectionToolbarConfig["onAction"];
    private readonly isEnabled: boolean;
    private updateRafId = 0;
    private destroyFlag = false;
    private readonly editorModeScrollListeners: Array<{
        target: HTMLElement;
        listener: () => void;
    }> = [];

    constructor(private readonly vditor: IVditor) {
        const options = this.vditor.options.selectionToolbar;
        this.actions = options?.actions ? options.actions.slice(0) : [];
        this.offset = options?.offset ?? DEFAULT_SELECTION_TOOLBAR_OFFSET;
        this.canInvoke = options?.canInvoke;
        this.onAction = options?.onAction;
        this.isEnabled = options?.enable !== false && this.actions.length > 0;

        this.element = document.createElement("div");
        this.element.className = "vditor-selection-toolbar";
        this.element.setAttribute("role", "toolbar");
        this.element.setAttribute("aria-label", "文本选择工具栏");
        this.element.style.visibility = "hidden";
        this.element.style.display = "none";
        document.body.appendChild(this.element);

        this.bindListeners();
    }

    public update = () => {
        if (this.destroyFlag || !this.isEnabled) {
            return;
        }
        if (this.updateRafId) {
            return;
        }
        this.updateRafId = window.requestAnimationFrame(() => {
            this.updateRafId = 0;
            this.render();
        });
    };

    public hide = () => {
        this.element.classList.remove("vditor-selection-toolbar--visible");
        this.element.style.visibility = "hidden";
        this.element.style.display = "none";
        this.element.replaceChildren();
    };

    public destroy = () => {
        if (this.destroyFlag) {
            return;
        }
        this.destroyFlag = true;
        if (this.updateRafId) {
            window.cancelAnimationFrame(this.updateRafId);
            this.updateRafId = 0;
        }
        document.removeEventListener("selectionchange", this.update);
        document.removeEventListener("mousedown", this.handleDocumentMouseDown, true);
        document.removeEventListener("keydown", this.handleKeydown);
        window.removeEventListener("scroll", this.update, true);
        window.removeEventListener("resize", this.update);
        this.editorModeScrollListeners.forEach(({target, listener}) => {
            target.removeEventListener("scroll", listener);
        });
        this.editorModeScrollListeners.length = 0;
        this.hide();
        this.element.remove();
    };

    private bindListeners() {
        document.addEventListener("selectionchange", this.update);
        document.addEventListener("mousedown", this.handleDocumentMouseDown, true);
        document.addEventListener("keydown", this.handleKeydown);
        window.addEventListener("scroll", this.update, true);
        window.addEventListener("resize", this.update);

        this.bindModeScroll(this.vditor.wysiwyg.element, () => this.update());
        this.bindModeScroll(this.vditor.sv.element, () => this.update());
        this.bindModeScroll(this.vditor.ir.element, () => this.update());
    }

    private bindModeScroll(element: HTMLElement, listener: () => void) {
        element.addEventListener("scroll", listener, {
            passive: true,
        });
        this.editorModeScrollListeners.push({
            target: element,
            listener,
        });
    }

    private handleDocumentMouseDown = (event: MouseEvent) => {
        if (this.element.style.display === "none") {
            return;
        }
        if (this.element.contains(event.target as Node)) {
            return;
        }
        if (this.vditor.element.contains(event.target as Node)) {
            this.update();
            return;
        }
        this.hide();
    };

    private handleKeydown = (event: KeyboardEvent) => {
        if (event.key === "Escape" && !event.isComposing) {
            this.hide();
        }
    };

    private createContext(range: Range): ISelectionToolbarContext {
        const value = range.toString();
        return {
            mode: this.vditor.currentMode,
            editorElement: this.vditor[this.vditor.currentMode].element,
            selection: value,
            range: range.cloneRange(),
        };
    }

    private render() {
        if (this.vditor.options.selectionToolbar?.enable === false) {
            this.hide();
            return;
        }
        const selection = window.getSelection();
        if (!selection || selection.rangeCount === 0) {
            this.hide();
            return;
        }

        const range = selection.getRangeAt(0);
        if (!range || range.collapsed) {
            this.hide();
            return;
        }

        const editorElement = this.vditor[this.vditor.currentMode].element;
        if (!editorElement.contains(range.startContainer) ||
            !editorElement.contains(range.endContainer)) {
            this.hide();
            return;
        }

        const context = this.createContext(range);
        const candidateActions = this.actions.filter((action) => {
            if (typeof action.command !== "string" || !action.command) {
                return false;
            }
            if (this.canInvoke && !this.canInvoke({
                ...context,
                action,
            }, this.vditor, action)) {
                return false;
            }
            return true;
        });

        if (candidateActions.length === 0 || context.selection.trim() === "") {
            this.hide();
            return;
        }

        this.renderButtons(candidateActions, context);
        this.position(context);
    }

    private renderButtons(actions: ISelectionToolbarAction[], context: ISelectionToolbarContext) {
        this.element.replaceChildren();
        actions.forEach((action) => {
            const button = document.createElement("button");
            button.type = "button";
            button.className = "vditor-selection-toolbar__button";
            button.setAttribute("aria-label", action.title || action.label);
            button.textContent = action.icon || action.label;
            button.setAttribute("title", action.title || action.label);

            button.addEventListener("mousedown", (event) => {
                event.preventDefault();
            });
            button.addEventListener("click", (event) => {
                event.preventDefault();
                event.stopPropagation();
                if (!this.isSelectionInCurrentMode()) {
                    this.hide();
                    return;
                }
                if (!context.selection.trim()) {
                    this.hide();
                    return;
                }
                this.handleAction(action, context);
            });
            this.element.appendChild(button);
        });

        this.element.style.display = "inline-flex";
        this.element.style.visibility = "visible";
        this.element.classList.add("vditor-selection-toolbar--visible");
    }

    private position(context: ISelectionToolbarContext) {
        const rangeRect = this.getRangeRect(context.range);
        if (!rangeRect) {
            this.hide();
            return;
        }

        const toolbarWidth = this.element.offsetWidth || 0;
        const toolbarHeight = this.element.offsetHeight || 0;
        const maxLeft = Math.max(8, window.innerWidth - toolbarWidth - 8);
        const left = Math.max(8, Math.min(maxLeft, rangeRect.left + (rangeRect.width / 2) - (toolbarWidth / 2)));

        let top = rangeRect.top - toolbarHeight - this.offset;
        if (top < 8 || top + toolbarHeight > window.innerHeight - 8) {
            top = rangeRect.bottom + this.offset;
        }
        if (top + toolbarHeight > window.innerHeight - 8) {
            top = Math.max(8, window.innerHeight - toolbarHeight - 8);
        }

        this.element.style.left = `${left}px`;
        this.element.style.top = `${top}px`;
    }

    private getRangeRect(range: Range) {
        const rects = Array.from(range.getClientRects()).filter((rect) => rect.width > 0 || rect.height > 0);
        if (rects.length > 0) {
            return rects[0];
        }

        const fallback = range.getBoundingClientRect();
        if (fallback.width > 0 || fallback.height > 0) {
            return fallback;
        }

        return null;
    }

    private isSelectionInCurrentMode() {
        const selection = window.getSelection();
        if (!selection || selection.rangeCount === 0) {
            return false;
        }
        const range = selection.getRangeAt(0);
        const editorElement = this.vditor[this.vditor.currentMode].element;
        return editorElement.contains(range.startContainer) && editorElement.contains(range.endContainer);
    }

    private handleAction(action: ISelectionToolbarAction, context: ISelectionToolbarContext) {
        const command = this.vditor.toolbar.elements[action.command];
        if (!command || !command.children.length) {
            this.onAction?.(action, context, this.vditor);
            return;
        }
        const toolbarButton = command.children[0] as HTMLElement;
        const runtimeContext: ISelectionToolbarRuntimeContext = {
            action,
            value: context.selection,
        };

        this.dispatchCommandExecution(context, runtimeContext, "before");
        this.onAction?.(action, context, this.vditor);
        this.restoreRange(context.range);
        toolbarButton.dispatchEvent(new CustomEvent(getEventName(), {
            bubbles: true,
            cancelable: true,
        }));
        this.dispatchCommandExecution(context, runtimeContext, "after");
        this.vditor.emitTransaction?.("command", {commandId: action.command});
        this.hide();
        this.update();
    }

    private dispatchCommandExecution(
        context: ISelectionToolbarContext,
        runtimeContext: ISelectionToolbarRuntimeContext,
        phase: "before" | "after",
    ) {
        if (typeof this.vditor.options.onEditorCommandExecuted !== "function") {
            return;
        }

        const {action, value} = runtimeContext;
        const command: IEditorCommand = {
            id: action?.command ?? "",
            trigger: "",
            keywords: [],
            description: action?.title || action?.label,
        };
        this.vditor.options.onEditorCommandExecuted(command, {
            ...context,
            command,
            phase,
            splitChar: "",
            key: "",
            keyword: "",
            lineValue: context.selection,
            value,
        });
    }

    private restoreRange(range: Range) {
        const selection = window.getSelection();
        if (!selection) {
            return;
        }
        selection.removeAllRanges();
        selection.addRange(range);
    }
}
