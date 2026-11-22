import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { InstallSteps } from "./components/InstallSteps";

describe("InstallSteps", () => {
  it("marks earlier steps done, the current one running, and shows the source", () => {
    const { container } = render(<InstallSteps busy phase="verify" source="msstore" />);
    expect(screen.getByRole("list", { name: "安装步骤" })).toBeInTheDocument();
    const items = container.querySelectorAll("li");
    expect(items).toHaveLength(4);
    expect(items[0]).toHaveClass("done");
    expect(items[1]).toHaveClass("done");
    expect(items[2]).toHaveClass("running");
    expect(items[3]).not.toHaveClass("running");
    expect(screen.getByText("来源：微软商店")).toBeInTheDocument();
  });
  it("starts at the first step when the phase is unknown", () => {
    const { container } = render(<InstallSteps busy phase="unknown" />);
    expect(container.querySelectorAll("li")[0]).toHaveClass("running");
    expect(screen.queryByText(/来源/)).not.toBeInTheDocument();
  });
});
