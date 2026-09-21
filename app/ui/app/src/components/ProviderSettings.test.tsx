import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Button } from "./ui/button";
import { ProviderSettings } from "./ProviderSettings";
import { providerRequest } from "@/lib/provider-settings";

const provider = {
  name: "deepseek",
  base_url: "https://api.deepseek.com/v1",
  models: ["deepseek-chat"],
  configured: false,
  enabled: true,
  managed_externally: false,
};
let renderer: ReactTestRenderer | undefined;
const request = vi.fn();
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", request);
  request.mockReset();
  request.mockResolvedValue({
    ok: true,
    json: async () => ({ providers: [provider] }),
  });
});
afterEach(async () => {
  if (renderer) await act(async () => renderer?.unmount());
  renderer = undefined;
  vi.unstubAllGlobals();
});
async function mount() {
  await act(async () => {
    renderer = create(
      <QueryClientProvider client={new QueryClient()}>
        <ProviderSettings />
      </QueryClientProvider>,
    );
  });
  return renderer!.root;
}
describe("Provider settings", () => {
  it("saves from a masked input, clears it, and shows the returned status", async () => {
    const root = await mount();
    const input = root.findByType("input");
    expect(input.props.type).toBe("password");
    await act(async () =>
      input.props.onChange({ target: { value: "test-secret" } }),
    );
    request.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ providers: [{ ...provider, configured: true }] }),
    });
    const save = root
      .findAllByType(Button)
      .find((b) => b.props.children === "Salvar chave")!;
    await act(async () => save.props.onClick());
    expect(request).toHaveBeenLastCalledWith(
      expect.stringContaining("/api/v1/dz23/providers"),
      expect.objectContaining({
        method: "PUT",
        body: JSON.stringify({ provider: "deepseek", key: "test-secret" }),
      }),
    );
    expect(root.findByType("input").props.value).toBe("");
    expect(JSON.stringify(renderer!.toJSON())).toContain("Chave salva");
    expect(JSON.stringify(renderer!.toJSON())).not.toContain("test-secret");
  });
  it("shows a useful error and never echoes a failed response body", async () => {
    request.mockResolvedValue({
      ok: false,
      status: 500,
      json: async () => ({ error: "secret-that-must-not-appear" }),
    });
    const root = await mount();
    expect(
      root.findByProps({ role: "alert" }).findByType("p").props.children,
    ).toContain("HTTP 500");
    expect(JSON.stringify(renderer!.toJSON())).not.toContain(
      "secret-that-must-not-appear",
    );
  });
  it("does not expose an edit input for environment-managed credentials", async () => {
    request.mockResolvedValue({
      ok: true,
      json: async () => ({
        providers: [{ ...provider, managed_externally: true }],
      }),
    });
    const root = await mount();
    expect(root.findAllByType("input")).toHaveLength(0);
  });
  it("reports an expired desktop session", async () => {
    request.mockResolvedValue({ ok: false, status: 403 });
    await expect(providerRequest()).rejects.toThrow("Sessão expirada");
  });
});
