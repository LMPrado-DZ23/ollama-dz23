import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { providerRequest, type APIProvider } from "@/lib/provider-settings";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function ProviderSettings() {
  const queryClient = useQueryClient();
  const [providers, setProviders] = useState<APIProvider[]>([]);
  const [selected, setSelected] = useState("");
  const [key, setKey] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    let active = true;
    setLoading(true);
    setError("");
    providerRequest()
      .then((items) => {
        if (active) {
          setProviders(items);
          setSelected(items[0]?.name || "");
        }
      })
      .catch((e: Error) => {
        if (active) setError(e.message);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [attempt]);
  const provider = providers.find((p) => p.name === selected);
  async function save(remove = false) {
    if (!provider || busy) return;
    if (remove && !window.confirm(`Remover a chave de ${provider.name}?`))
      return;
    setBusy(true);
    setError("");
    setMessage("");
    const submittedKey = key;
    setKey("");
    try {
      const items = await providerRequest(
        remove ? "DELETE" : "PUT",
        provider.name,
        remove ? undefined : submittedKey,
      );
      setProviders(items);
      setMessage(
        remove
          ? "Chave removida."
          : "Chave salva. Os modelos estão disponíveis no seletor; a validade será confirmada ao usar o provedor.",
      );
      await queryClient.invalidateQueries({ queryKey: ["models"] });
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "Não foi possível salvar a alteração.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section
      aria-labelledby="provider-heading"
      className="rounded-xl bg-white p-4 dark:bg-neutral-800 space-y-4"
    >
      <div>
        <h2 id="provider-heading" className="font-medium">
          Provedores e chaves de API
        </h2>
        <p className="mt-1 text-sm text-neutral-500">
          Conecte suas contas de IA ao Ollama DZ23. O uso segue os limites e a
          cobrança de cada provedor.
        </p>
      </div>
      {loading ? (
        <p role="status">Carregando provedores…</p>
      ) : (
        <>
          {providers.length === 0 && !error && (
            <p className="text-sm">
              Nenhum provedor configurado. Instale a versão DZ23 com o catálogo
              de provedores ou configure OLLAMA_DZ23_CONFIG.
            </p>
          )}
          {provider && (
            <div className="space-y-3">
              <label className="block text-sm" htmlFor="api-provider">
                Provedor
              </label>
              <select
                id="api-provider"
                value={selected}
                disabled={busy}
                onChange={(e) => {
                  setSelected(e.target.value);
                  setKey("");
                  setError("");
                  setMessage("");
                }}
                className="w-full rounded-md border border-neutral-300 bg-transparent p-2 text-sm dark:border-neutral-600"
              >
                {providers.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                    {p.configured ? " — chave cadastrada" : " — sem chave"}
                  </option>
                ))}
              </select>
              <p className="text-xs text-neutral-500 break-all">
                {provider.base_url}
              </p>
              <p className="text-sm">
                {!provider.enabled
                  ? "Provedor desativado no catálogo."
                  : provider.configured
                    ? "Chave cadastrada · validade não verificada"
                    : "Cadastre uma chave para habilitar os modelos."}
              </p>
              {provider.managed_externally ? (
                <p className="text-sm">
                  Credencial gerenciada por variável de ambiente ou pelo
                  configurador externo.
                </p>
              ) : (
                <>
                  <label className="block text-sm" htmlFor="provider-api-key">
                    {provider.configured
                      ? "Substituir chave de API"
                      : "Chave de API"}
                  </label>
                  <Input
                    id="provider-api-key"
                    type="password"
                    autoComplete="new-password"
                    spellCheck={false}
                    value={key}
                    disabled={busy}
                    onChange={(e) => setKey(e.target.value)}
                    placeholder="Cole sua chave de API"
                  />
                  <p className="text-xs text-neutral-500">
                    No Windows, a chave é criptografada para seu usuário. Ela
                    não será exibida novamente.
                  </p>
                  <div className="flex flex-wrap gap-2">
                    <Button
                      type="button"
                      disabled={busy || !key.trim()}
                      onClick={() => void save()}
                    >
                      {busy ? "Salvando…" : "Salvar chave"}
                    </Button>
                    {provider.configured && (
                      <Button
                        type="button"
                        outline
                        disabled={busy}
                        onClick={() => void save(true)}
                      >
                        Remover chave
                      </Button>
                    )}
                  </div>
                </>
              )}
              <details className="text-sm">
                <summary className="cursor-pointer">
                  Modelos do catálogo ({provider.models.length})
                </summary>
                <ul className="mt-2 list-disc pl-5 break-all">
                  {provider.models.map((m) => (
                    <li key={m}>{m}</li>
                  ))}
                </ul>
              </details>
            </div>
          )}
        </>
      )}
      {error && (
        <div role="alert" className="text-sm text-red-600">
          <p>{error}</p>
          {providers.length === 0 && (
            <Button
              type="button"
              outline
              onClick={() => setAttempt((n) => n + 1)}
            >
              Tentar novamente
            </Button>
          )}
        </div>
      )}
      {message && (
        <p role="status" className="text-sm text-green-700 dark:text-green-400">
          {message}
        </p>
      )}
    </section>
  );
}
