import { describe, expect, it } from "vitest";
import { Model } from "@/gotypes";
import { modelGroup } from "./ModelPicker";

describe("modelGroup", () => {
  it("separates local, router, and remote provider models", () => {
    expect(modelGroup(new Model({ model: "qwen3:8b", kind: "local" }))).toBe(
      "Modelos locais",
    );
    expect(
      modelGroup(new Model({ model: "auto/coding", kind: "router" })),
    ).toBe("Roteamento inteligente");
    expect(
      modelGroup(
        new Model({
          model: "groq/llama-3.3-70b-versatile",
          kind: "remote",
          provider: "groq",
        }),
      ),
    ).toBe("groq");
  });
});
