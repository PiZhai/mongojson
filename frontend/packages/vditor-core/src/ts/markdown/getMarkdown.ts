import {code160to32} from "../util/code160to32";
import {Constants} from "../constants";

const stripCodeBlockCaretPadding = (html: string) => {
    const tempElement = document.createElement("div");
    tempElement.innerHTML = html;
    tempElement.querySelectorAll<HTMLElement>('[data-type="code-block"] .vditor-ir__marker--pre code')
        .forEach((codeElement) => {
            codeElement.childNodes.forEach((node) => {
                if (node.nodeType === 3) {
                    node.nodeValue = node.nodeValue.replace(new RegExp(Constants.ZWSP, "g"), "");
                }
            });
        });
    return tempElement.innerHTML;
};

export const getMarkdown = (vditor: IVditor) => {
    if (vditor.currentMode === "sv") {
        return code160to32(`${vditor.sv.element.textContent}\n`.replace(/\n\n$/, "\n"));
    } else if (vditor.currentMode === "wysiwyg") {
        return vditor.lute.VditorDOM2Md(vditor.wysiwyg.element.innerHTML);
    } else if (vditor.currentMode === "ir") {
        return vditor.lute.VditorIRDOM2Md(stripCodeBlockCaretPadding(vditor.ir.element.innerHTML));
    }
    return "";
};
