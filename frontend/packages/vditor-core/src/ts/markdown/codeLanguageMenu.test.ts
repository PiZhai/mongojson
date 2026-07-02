import {describe, expect, it} from "vitest";
import {replaceCodeFenceLanguage} from "./codeLanguageMenu";

describe("code language menu markdown updates", () => {
    it("updates only the selected fenced code block language", () => {
        const markdown = [
            "```ts",
            "const value = 1",
            "```",
            "",
            "```json",
            "{\"ok\":true}",
            "```",
        ].join("\n");

        expect(replaceCodeFenceLanguage(markdown, 1, "python")).toBe([
            "```ts",
            "const value = 1",
            "```",
            "",
            "```python",
            "{\"ok\":true}",
            "```",
        ].join("\n"));
    });

    it("clears the language when plaintext is selected", () => {
        expect(replaceCodeFenceLanguage("```json\n{}\n```", 0, "plaintext")).toBe("```\n{}\n```");
    });

    it("keeps markdown unchanged when the target block is missing", () => {
        expect(replaceCodeFenceLanguage("plain text", 0, "ts")).toBe("plain text");
    });
});
