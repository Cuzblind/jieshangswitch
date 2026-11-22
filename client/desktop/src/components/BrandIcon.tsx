import { claudeColor, claudePath } from "@/assets/brands/claude";
import { openaiPath } from "@/assets/brands/openai";
import type { Agent } from "@/lib/desktop";

// Each assistant shows its vendor's published mark. OpenAI's mark is
// monochrome, so it follows the text color in light and dark themes.
export function BrandIcon({ agent, size = 23 }: { agent: Agent; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" role="img" aria-hidden="true" focusable="false">
      {agent === "claude" ? (
        <path d={claudePath} fill={claudeColor} />
      ) : (
        <path d={openaiPath} fill="currentColor" fillRule="evenodd" />
      )}
    </svg>
  );
}
