import {processAfterRender as processIRAfterRender} from "../ir/process";
import {getMarkdown} from "../markdown/getMarkdown";
import {processAfterRender as processSVAfterRender} from "../sv/process";
import {execAfterRender} from "./fixBrowserBehavior";
import {processCodeRender} from "./processCode";
import {renderDomByMd} from "../wysiwyg/renderDomByMd";

export interface ISetMarkdownOptions {
    enableAddUndoStack?: boolean;
    enableAfterRender?: boolean;
    enableHint?: boolean;
    enableInput?: boolean;
}

export const setMarkdown = (vditor: IVditor, markdown: string, options: ISetMarkdownOptions = {}) => {
    const {
        enableAddUndoStack = true,
        enableAfterRender = false,
        enableHint = false,
        enableInput = false,
    } = options;

    if (vditor.currentMode === "sv") {
        vditor.sv.element.innerHTML = `<div data-block='0'>${vditor.lute.SpinVditorSVDOM(markdown)}</div>`;
        processSVAfterRender(vditor, {
            enableAddUndoStack,
            enableHint,
            enableInput,
        });
    } else if (vditor.currentMode === "wysiwyg") {
        renderDomByMd(vditor, markdown, {
            enableAddUndoStack,
            enableHint,
            enableInput,
        });
    } else {
        vditor.ir.element.innerHTML = vditor.lute.Md2VditorIRDOM(markdown);
        vditor.ir.element
            .querySelectorAll(".vditor-ir__preview[data-render='2']")
            .forEach((item: HTMLElement) => {
                processCodeRender(item, vditor);
            });
        processIRAfterRender(vditor, {
            enableAddUndoStack,
            enableHint,
            enableInput,
        });
    }

    vditor.outline.render(vditor);

    if (enableAfterRender) {
        execAfterRender(vditor);
    }

    return getMarkdown(vditor);
};
