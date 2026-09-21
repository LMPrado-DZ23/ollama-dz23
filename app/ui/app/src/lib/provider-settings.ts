import { API_BASE } from "./config";

export interface APIProvider {
  name: string;
  base_url: string;
  models: string[];
  configured: boolean;
  enabled: boolean;
  managed_externally: boolean;
}

export async function providerRequest(
  method = "GET",
  provider?: string,
  key?: string,
): Promise<APIProvider[]> {
  const response = await fetch(`${API_BASE}/api/v1/dz23/providers`, {
    method,
    headers:
      method === "GET" ? undefined : { "Content-Type": "application/json" },
    body: method === "GET" ? undefined : JSON.stringify({ provider, key }),
    credentials: "same-origin",
    cache: "no-store",
  });
  if (!response.ok) {
    // Never echo a response body that might contain a credential.
    if (response.status === 409)
      throw new Error(
        "Esta chave é gerenciada pelo ambiente. Remova a configuração externa antes de editar aqui.",
      );
    if (response.status === 403)
      throw new Error(
        "Sessão expirada. Reabra o Ollama DZ23 para configurar as APIs.",
      );
    throw new Error(
      `Não foi possível ${method === "GET" ? "carregar os provedores" : "salvar a alteração"} (HTTP ${response.status}).`,
    );
  }
  const data = await response.json();
  if (!Array.isArray(data.providers))
    throw new Error("Resposta inválida ao carregar os provedores.");
  return data.providers;
}
