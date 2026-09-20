import { afterEach, describe, expect, it, vi } from "vitest";
const { listModels } = vi.hoisted(() => ({ listModels: vi.fn() }));
vi.mock("./lib/ollama-client", () => ({
  ollamaClient: { list: listModels },
}));

import {
  fetchConnectUrl,
  getModels,
  getClaudeDesktopAvailableModels,
  getIntegrationStatuses,
} from "./api";

describe("getModels", () => {
  afterEach(() => listModels.mockReset());

  it("preserves DZ23 provider metadata for remote and routed models", async () => {
    listModels.mockResolvedValue({
      models: [
        {
          name: "qwen3:8b",
          digest: "sha256:local",
          details: { format: "gguf", family: "qwen" },
        },
        {
          name: "groq/llama",
          digest: "remote:groq",
          details: { format: "remote", family: "groq" },
          capabilities: ["chat", "coding"],
        },
        {
          name: "deepseek/chat",
          digest: "remote:deepseek",
          details: { format: "remote", family: "deepseek-unavailable" },
        },
        {
          name: "auto/coding",
          digest: "virtual:dz23",
          details: { format: "virtual", family: "dz23-router" },
        },
      ],
    });

    const models = await getModels();
    expect(
      models.map((model) => ({
        model: model.model,
        kind: model.kind,
        provider: model.provider,
        available: model.available,
        capabilities: model.capabilities,
      })),
    ).toEqual([
      {
        model: "qwen3:8b",
        kind: "local",
        provider: "Ollama",
        available: true,
        capabilities: [],
      },
      {
        model: "groq/llama",
        kind: "remote",
        provider: "groq",
        available: true,
        capabilities: ["chat", "coding"],
      },
      {
        model: "deepseek/chat",
        kind: "remote",
        provider: "deepseek",
        available: false,
        capabilities: [],
      },
      {
        model: "auto/coding",
        kind: "router",
        provider: "DZ23 Router",
        available: true,
        capabilities: [],
      },
    ]);
  });
});

describe("fetchConnectUrl", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("requests a desktop handoff after account creation", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            signin_url:
              "https://ollama.com/connect?name=MacBook&key=public-key",
          }),
          { status: 401 },
        ),
      ),
    );

    await expect(fetchConnectUrl()).resolves.toBe(
      "https://ollama.com/connect?name=MacBook&key=public-key&launch=true",
    );
  });
});

describe("getIntegrationStatuses", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns desktop and launcher integration metadata", async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify([
          {
            id: "claude-desktop",
            name: "Claude",
            description: "Use Ollama models in Claude Desktop",
            installed: true,
          },
          {
            id: "opencode",
            name: "OpenCode",
            description: "Open-source coding agent",
            command: "ollama launch opencode",
          },
        ]),
        { status: 200 },
      ),
    );
    vi.stubGlobal("fetch", fetch);

    await expect(getIntegrationStatuses()).resolves.toEqual([
      {
        id: "claude-desktop",
        name: "Claude",
        description: "Use Ollama models in Claude Desktop",
        installed: true,
      },
      {
        id: "opencode",
        name: "OpenCode",
        description: "Open-source coding agent",
        command: "ollama launch opencode",
      },
    ]);
    expect(fetch).toHaveBeenCalledWith(
      "http://127.0.0.1:3001/api/v1/integrations",
    );
  });
});

describe("getClaudeDesktopAvailableModels", () => {
  afterEach(() => {
    listModels.mockReset();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("returns installed local models while pruning remote entries", async () => {
    listModels.mockResolvedValue({
      models: [
        { name: "llama3.2:latest", digest: "local" },
        {
          name: "remote-placeholder",
          digest: "remote",
          remote_host: "https://ollama.com",
        },
      ],
    });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);

    const models = await getClaudeDesktopAvailableModels();

    expect(models.map((model) => model.model)).toEqual(["llama3.2"]);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("does not request cloud models when they are unavailable to the user", async () => {
    listModels.mockResolvedValue({
      models: [
        { name: "qwen3:8b", digest: "local" },
        { name: "deepseek-v4-flash:cloud", digest: "cached-cloud" },
        { name: "gemma4:31b-cloud", digest: "legacy-cached-cloud" },
      ],
    });
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);

    const models = await getClaudeDesktopAvailableModels();

    expect(models.map((model) => model.model)).toEqual(["qwen3:8b"]);
    expect(fetch).not.toHaveBeenCalled();
  });

  it("loads the account cloud list in parallel when Cloud is available", async () => {
    listModels.mockResolvedValue({
      models: [{ name: "qwen3:8b", digest: "local" }],
    });
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          models: [
            { name: "glm-5.2", digest: "cloud" },
            { name: "gemma4:31b-cloud", digest: "legacy-cloud" },
            { name: "qwen3:8b", digest: "cloud-duplicate" },
          ],
        }),
      ),
    );
    vi.stubGlobal("fetch", fetch);

    const models = await getClaudeDesktopAvailableModels(true);

    expect(models.map((model) => model.model)).toEqual([
      "qwen3:8b",
      "glm-5.2:cloud",
      "gemma4:31b-cloud",
    ]);
    expect(fetch).toHaveBeenCalledWith(
      "http://127.0.0.1:3001/api/v1/models/cloud",
    );
  });

  it("keeps local models when the account cloud list fails", async () => {
    listModels.mockResolvedValue({
      models: [{ name: "qwen3:8b", digest: "local" }],
    });
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));

    const models = await getClaudeDesktopAvailableModels(true);

    expect(models.map((model) => model.model)).toEqual(["qwen3:8b"]);
  });
});
