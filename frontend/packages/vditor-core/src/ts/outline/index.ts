import {Constants} from "../constants";
import {outlineRender} from "../markdown/outlineRender";
import {setPadding} from "../ui/initUI";
import {setSelectionFocus} from "../util/selection";

export class Outline {
    public element: HTMLElement;
    private activeTargetId: string | null = null;
    private scrollElement: HTMLElement | null = null;
    private scrollRafId = 0;
    private vditor?: IVditor;

    constructor(outlineLabel: string) {
        this.element = document.createElement("div");
        this.element.className = "vditor-outline";
        this.element.innerHTML = `<div class="vditor-outline__title">${outlineLabel}</div>
<div class="vditor-outline__content"></div>`;
        this.element.addEventListener("click", this.handleClick, true);
    }

    public render(vditor: IVditor) {
        this.vditor = vditor;
        let html: string;
        let contentElement: HTMLElement;
        if (vditor.preview.element.style.display === "block") {
            contentElement = vditor.preview.previewElement;
            html = outlineRender(vditor.preview.previewElement,
                this.element.lastElementChild, vditor);
        } else {
            contentElement = vditor[vditor.currentMode].element;
            html = outlineRender(vditor[vditor.currentMode].element, this.element.lastElementChild, vditor);
        }
        if (vditor.options.outline.enhanced) {
            this.syncEnhancedState(vditor, contentElement);
        }
        return html;
    }

    public destroy() {
        this.element.removeEventListener("click", this.handleClick, true);
        this.scrollElement?.removeEventListener("scroll", this.handleScroll);
        if (this.scrollRafId) {
            window.cancelAnimationFrame(this.scrollRafId);
        }
    }

    public toggle(vditor: IVditor, show = true, focus = true) {
        const btnElement = vditor.toolbar.elements.outline?.firstElementChild;
        if (show && window.innerWidth >= Constants.MOBILE_WIDTH) {
            this.element.style.display = "block";
            this.render(vditor);
            btnElement?.classList.add("vditor-menu--current");
        } else {
            this.element.style.display = "none";
            btnElement?.classList.remove("vditor-menu--current");
        }
        if (focus && getSelection().rangeCount > 0) {
            const range = getSelection().getRangeAt(0);
            if (vditor[vditor.currentMode].element.contains(range.startContainer)) {
                setSelectionFocus(range);
            }
        }
        setPadding(vditor);
    }

    private getActiveClass(vditor: IVditor) {
        return vditor.options.outline.activeClass || "vditor-outline__item--active";
    }

    private getScrollOffset(vditor: IVditor) {
        return vditor.options.outline.scrollOffset ?? 72;
    }

    private getContentElement(vditor: IVditor) {
        if (vditor.preview.element.style.display === "block") {
            return vditor.preview.previewElement;
        }
        return vditor[vditor.currentMode].element;
    }

    private getScrollElement(vditor: IVditor, contentElement: HTMLElement) {
        if (vditor.preview.previewElement.contains(contentElement)) {
            return vditor.preview.element;
        }
        return contentElement;
    }

    private getHeadings(contentElement: HTMLElement) {
        return Array.from(contentElement.querySelectorAll<HTMLElement>("h1,h2,h3,h4,h5,h6"));
    }

    private getOutlineEntries(vditor: IVditor, contentElement = this.getContentElement(vditor)) {
        const headings = this.getHeadings(contentElement);
        return Array.from(this.element.querySelectorAll<HTMLElement>("[data-target-id]"))
            .map((outlineItem) => {
                const targetId = outlineItem.getAttribute("data-target-id");
                if (!targetId) {
                    return null;
                }
                const heading = headings.find((item) => item.id === targetId);
                if (!heading) {
                    return null;
                }
                return {
                    heading,
                    outlineItem,
                    targetId,
                    top: heading.offsetTop,
                };
            })
            .filter((entry): entry is {
                heading: HTMLElement;
                outlineItem: HTMLElement;
                targetId: string;
                top: number;
            } => Boolean(entry));
    }

    private setActiveTarget(vditor: IVditor, targetId: string | null, shouldReveal = true) {
        const activeClass = this.getActiveClass(vditor);
        this.element.querySelectorAll<HTMLElement>(`.${activeClass}`)
            .forEach((item) => item.classList.remove(activeClass));
        this.activeTargetId = targetId;
        if (!targetId) {
            return;
        }

        const activeItem = Array.from(this.element.querySelectorAll<HTMLElement>("[data-target-id]"))
            .find((item) => item.getAttribute("data-target-id") === targetId);
        activeItem?.classList.add(activeClass);
        if (activeItem && shouldReveal) {
            this.ensureVisible(activeItem);
        }
    }

    private ensureVisible(outlineItem: HTMLElement) {
        const stickyTitle = this.element.querySelector<HTMLElement>(".vditor-outline__title");
        const topInset = (stickyTitle?.offsetHeight ?? 0) + 8;
        const itemTop = outlineItem.offsetTop;
        const itemBottom = itemTop + outlineItem.offsetHeight;
        const visibleTop = this.element.scrollTop + topInset;
        const visibleBottom = this.element.scrollTop + this.element.clientHeight;

        if (itemTop < visibleTop) {
            this.element.scrollTop = Math.max(0, itemTop - topInset);
            return;
        }

        if (itemBottom > visibleBottom) {
            this.element.scrollTop = itemBottom - this.element.clientHeight + 12;
        }
    }

    private findActiveEntry(vditor: IVditor) {
        const entries = this.getOutlineEntries(vditor);
        if (entries.length === 0) {
            return null;
        }
        const scrollTop = this.scrollElement?.scrollTop ?? 0;
        const targetTop = scrollTop + this.getScrollOffset(vditor);
        let activeEntry = entries[0];
        for (const entry of entries) {
            if (entry.top > targetTop) {
                break;
            }
            activeEntry = entry;
        }
        return activeEntry;
    }

    private syncEnhancedState(vditor: IVditor, contentElement: HTMLElement) {
        const nextScrollElement = this.getScrollElement(vditor, contentElement);
        if (nextScrollElement !== this.scrollElement) {
            this.scrollElement?.removeEventListener("scroll", this.handleScroll);
            this.scrollElement = nextScrollElement;
            this.scrollElement.addEventListener("scroll", this.handleScroll, {passive: true});
        }

        const entries = this.getOutlineEntries(vditor, contentElement);
        if (entries.length === 0) {
            this.setActiveTarget(vditor, null, false);
            return;
        }

        const activeEntry = this.activeTargetId
            ? entries.find((entry) => entry.targetId === this.activeTargetId)
            : this.findActiveEntry(vditor);
        this.setActiveTarget(vditor, activeEntry?.targetId ?? entries[0].targetId, false);
    }

    private handleScroll = () => {
        const vditor = this.vditor;
        if (!vditor?.options.outline.enhanced || this.scrollRafId) {
            return;
        }
        this.scrollRafId = window.requestAnimationFrame(() => {
            this.scrollRafId = 0;
            const activeEntry = this.findActiveEntry(vditor);
            this.setActiveTarget(vditor, activeEntry?.targetId ?? null);
        });
    };

    private handleClick = (event: MouseEvent) => {
        const vditor = this.vditor;
        if (!vditor?.options.outline.enhanced) {
            return;
        }
        const target = event.target;
        if (!(target instanceof HTMLElement)) {
            return;
        }
        const outlineTarget = target.closest<HTMLElement>("[data-target-id]");
        const targetId = outlineTarget?.getAttribute("data-target-id");
        if (targetId) {
            this.setActiveTarget(vditor, targetId);
        }
    };
}
