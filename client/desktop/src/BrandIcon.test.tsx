import { render } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";
import { BrandIcon } from "./components/BrandIcon";

// The Codex card shows OpenAI's official mark, taken verbatim from upstream CC Switch.
it("draws the OpenAI mark for Codex", () => {
  const svg = readFileSync(resolve(process.cwd(), "src/icons/extracted/openai.svg"), "utf8");
  const official = svg.match(/ d="([^"]+)"/)?.[1];
  const { container } = render(<BrandIcon agent="codex" size={24} />);
  const path = container.querySelector("path");
  expect(official).toBeTruthy();
  expect(path?.getAttribute("d")).toBe(official);
  expect(path?.getAttribute("fill-rule")).toBe("evenodd");
  expect(path?.getAttribute("fill")).toBe("currentColor");
});
