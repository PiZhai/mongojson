import {describe, expect, it} from "vitest";
import {ensureTrailingNewlines, getTailClickLine} from ".";

describe("editor tail helpers", () => {
    it("adds one trailing newline so typing can continue after a closing code fence", () => {
        expect(ensureTrailingNewlines("```ts\nconst value = 1\n```", 1, 3)).toBe("```ts\nconst value = 1\n```\n");
    });

    it("does not add extra newlines when the requested tail line already exists", () => {
        expect(ensureTrailingNewlines("done\n\n", 2, 3)).toBe("done\n\n");
    });

    it("pads to the requested double-click tail line", () => {
        expect(ensureTrailingNewlines("done", 2, 3)).toBe("done\n\n");
        expect(ensureTrailingNewlines("done\n", 3, 3)).toBe("done\n\n\n");
    });

    it("keeps an empty document empty on first-line single click", () => {
        expect(ensureTrailingNewlines("", 1, 3)).toBe("");
    });

    it("pads an empty document to the requested blank line for double click", () => {
        expect(ensureTrailingNewlines("", 3, 3)).toBe("\n\n");
    });

    it("returns the tail line based on the click distance after the last content block", () => {
        expect(getTailClickLine(101, 100, 20, 3)).toBe(1);
        expect(getTailClickLine(125, 100, 20, 3)).toBe(2);
        expect(getTailClickLine(200, 100, 20, 3)).toBe(3);
        expect(getTailClickLine(100, 100, 20, 3)).toBeNull();
    });
});
