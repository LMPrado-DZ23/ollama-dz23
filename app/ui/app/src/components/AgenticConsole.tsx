import { useEffect, useMemo, useState } from "react";

type Mission = {
  id: string;
  objective: string;
  state: string;
  plan?: Array<{ id: string; title: string; kind: string; state: string; requires_approval: boolean }>;
  approvals?: Array<{ id: string; step_id: string; status: string; reason?: string }>;
  artifacts?: Array<{ id: string; name: string; sha256: string; size: number }>;
  last_error?: string;
};
type Event = { id: string; type: string; step_id?: string; created_at: string; payload?: unknown };
type Metrics = Record<string, number>;
type OrchestrationTask = { id: string; role: string; state: string; output?: string; error?: string; evidence?: Array<{ url: string; title?: string; excerpt?: string }> };
type OrchestrationJob = { id: string; objective: string; state: string; summary?: string; conflicts?: string[]; tasks: OrchestrationTask[] };
type ResearchReport = { query: string; summary: string; citations: Array<{ url: string; title?: string; excerpt?: string }>; sources: Array<{ url: string; title?: string; error?: string }> };

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, { ...init, headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error ?? response.statusText);
  return body as T;
}

export default function AgenticConsole() {
  const [objective, setObjective] = useState("");
  const [mission, setMission] = useState<Mission | null>(null);
  const [events, setEvents] = useState<Event[]>([]);
  const [metrics, setMetrics] = useState<Metrics>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [orchestrationObjective, setOrchestrationObjective] = useState("");
  const [orchestration, setOrchestration] = useState<OrchestrationJob | null>(null);
  const [researchQuery, setResearchQuery] = useState("");
  const [researchURLs, setResearchURLs] = useState("");
  const [research, setResearch] = useState<ResearchReport | null>(null);

  const load = async (missionId?: string, orchestrationId?: string) => {
    try {
      const [nextMetrics, nextMission, nextEvents, nextOrchestration] = await Promise.all([
        api<Metrics>("/api/agent/v1/metrics"),
        missionId ? api<Mission>(`/api/agent/v1/missions/${encodeURIComponent(missionId)}`) : Promise.resolve(null),
        missionId ? api<{ events: Event[] }>(`/api/agent/v1/missions/${encodeURIComponent(missionId)}/events`) : Promise.resolve({ events: [] }),
        orchestrationId ? api<OrchestrationJob>(`/api/agent/v1/orchestration/jobs/${encodeURIComponent(orchestrationId)}`) : Promise.resolve(null),
      ]);
      setMetrics(nextMetrics);
      if (nextMission) setMission(nextMission);
      setEvents(nextEvents.events);
      if (nextOrchestration) setOrchestration(nextOrchestration);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Não foi possível carregar o runtime agentic");
    }
  };

  useEffect(() => {
    void load(mission?.id, orchestration?.id);
    const timer = window.setInterval(() => void load(mission?.id, orchestration?.id), 3000);
    return () => window.clearInterval(timer);
  }, [mission?.id, orchestration?.id]);

  const pendingApprovals = useMemo(() => mission?.approvals?.filter((approval) => approval.status === "PENDING") ?? [], [mission]);

  const createMission = async () => {
    if (!objective.trim()) return;
    setBusy(true); setError("");
    try {
      const created = await api<Mission>("/api/agent/v1/missions", { method: "POST", body: JSON.stringify({ objective, auto_run: false }) });
      setMission(created); setObjective(""); await load(created.id, orchestration?.id);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Falha ao criar missão"); } finally { setBusy(false); }
  };

  const decide = async (approvalId: string, approved: boolean) => {
    if (!mission) return;
    setBusy(true);
    try {
      await api(`/api/agent/v1/missions/${encodeURIComponent(mission.id)}/approvals/${encodeURIComponent(approvalId)}`, { method: "POST", body: JSON.stringify({ approved, reason: approved ? "Aprovado no Agentic Console" : "Rejeitado no Agentic Console" }) });
      await load(mission.id, orchestration?.id);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Falha ao decidir aprovação"); } finally { setBusy(false); }
  };

  const run = async () => {
    if (!mission) return;
    setBusy(true);
    try { await api(`/api/agent/v1/missions/${encodeURIComponent(mission.id)}/run`, { method: "POST", body: "{}" }); await load(mission.id, orchestration?.id); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "Falha ao iniciar missão"); } finally { setBusy(false); }
  };

  const createOrchestration = async () => {
    if (!orchestrationObjective.trim()) return;
    setBusy(true); setError("");
    try {
      const created = await api<OrchestrationJob>("/api/agent/v1/orchestration/jobs", { method: "POST", body: JSON.stringify({ objective: orchestrationObjective, roles: ["research", "programming", "testing", "security", "review"], budget: { max_agents: 3, max_seconds: 600, max_retries: 1 }, auto_run: true }) });
      setOrchestration(created); setOrchestrationObjective(""); await load(mission?.id, created.id);
    } catch (cause) { setError(cause instanceof Error ? cause.message : "Falha ao iniciar orquestração"); } finally { setBusy(false); }
  };

  const runResearch = async () => {
    const urls = researchURLs.split(/\r?\n/).map((url) => url.trim()).filter(Boolean);
    if (!researchQuery.trim() || urls.length === 0) return;
    setBusy(true); setError("");
    try { setResearch(await api<ResearchReport>("/api/agent/v1/research", { method: "POST", body: JSON.stringify({ query: researchQuery, urls, max_sources: 8, respect_robots: true }) })); }
    catch (cause) { setError(cause instanceof Error ? cause.message : "Falha na pesquisa profunda"); } finally { setBusy(false); }
  };

  return (
    <main className="flex h-full min-h-0 flex-col gap-5 overflow-y-auto p-6">
      <header><p className="text-xs font-medium uppercase tracking-[0.18em] text-neutral-500">DZ23 Agentic Runtime</p><h1 className="mt-1 text-2xl font-semibold text-neutral-900 dark:text-neutral-100">Mission Console</h1><p className="mt-2 max-w-3xl text-sm text-neutral-600 dark:text-neutral-400">Planeje, orquestre, pesquise, aprove, execute e observe operações com a mesma trilha persistente usada pela API local.</p></header>

      <section className="rounded-2xl border border-neutral-200 bg-white p-4 shadow-sm dark:border-neutral-800 dark:bg-neutral-950"><label className="text-sm font-medium text-neutral-800 dark:text-neutral-200" htmlFor="agent-objective">Novo objetivo</label><div className="mt-3 flex flex-col gap-3 sm:flex-row"><textarea id="agent-objective" value={objective} onChange={(event) => setObjective(event.target.value)} placeholder="Ex.: inspecionar o projeto e listar possíveis erros" className="min-h-20 flex-1 resize-y rounded-xl border border-neutral-300 bg-transparent px-3 py-2 text-sm outline-none focus:border-neutral-500 dark:border-neutral-700" /><button type="button" disabled={busy || !objective.trim()} onClick={() => void createMission()} className="h-10 rounded-xl bg-neutral-900 px-4 text-sm font-medium text-white disabled:cursor-not-allowed disabled:opacity-40 dark:bg-neutral-100 dark:text-neutral-900">Criar missão</button></div>{error && <p className="mt-3 rounded-lg bg-red-50 px-3 py-2 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300">{error}</p>}</section>

      <section className="grid grid-cols-2 gap-3 sm:grid-cols-4">{[["Criadas", metrics.missions_created], ["Concluídas", metrics.missions_completed], ["Retries", metrics.retries], ["Tools", metrics.tool_calls]].map(([label, value]) => <div key={label} className="rounded-xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-950"><p className="text-xs text-neutral-500">{label}</p><p className="mt-1 text-2xl font-semibold text-neutral-900 dark:text-neutral-100">{value ?? 0}</p></div>)}</section>

      <section className="grid gap-4 xl:grid-cols-2">
        <div className="rounded-2xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-950"><div className="flex items-start justify-between gap-3"><div><p className="text-xs font-medium uppercase tracking-[0.14em] text-neutral-500">Multiagent</p><h2 className="mt-1 font-medium text-neutral-900 dark:text-neutral-100">Orquestrar especialistas</h2></div>{orchestration && <span className="rounded-full bg-neutral-100 px-3 py-1 text-xs font-medium dark:bg-neutral-800">{orchestration.state}</span>}</div><textarea value={orchestrationObjective} onChange={(event) => setOrchestrationObjective(event.target.value)} placeholder="Ex.: pesquisar concorrentes, revisar segurança e propor implementação" className="mt-3 min-h-20 w-full resize-y rounded-xl border border-neutral-300 bg-transparent px-3 py-2 text-sm outline-none dark:border-neutral-700" /><button type="button" disabled={busy || !orchestrationObjective.trim()} onClick={() => void createOrchestration()} className="mt-3 rounded-lg bg-neutral-900 px-3 py-2 text-sm text-white disabled:opacity-40 dark:bg-neutral-100 dark:text-neutral-900">Iniciar orquestração</button>{orchestration && <div className="mt-4 space-y-2">{orchestration.tasks.map((task) => <div key={task.id} className="rounded-lg border border-neutral-200 p-3 text-sm dark:border-neutral-800"><div className="flex justify-between"><span className="font-medium">{task.role}</span><span className="text-xs text-neutral-500">{task.state}</span></div>{task.error && <p className="mt-1 text-xs text-red-600">{task.error}</p>}</div>)}{orchestration.summary && <details className="rounded-lg bg-neutral-50 p-3 text-xs dark:bg-neutral-900"><summary className="cursor-pointer font-medium">Ver síntese</summary><pre className="mt-2 whitespace-pre-wrap font-sans">{orchestration.summary}</pre></details>}</div>}</div>
        <div className="rounded-2xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-950"><p className="text-xs font-medium uppercase tracking-[0.14em] text-neutral-500">Pesquisa profunda</p><h2 className="mt-1 font-medium text-neutral-900 dark:text-neutral-100">Fontes e citações</h2><input value={researchQuery} onChange={(event) => setResearchQuery(event.target.value)} placeholder="Pergunta de pesquisa" className="mt-3 h-10 w-full rounded-xl border border-neutral-300 bg-transparent px-3 text-sm outline-none dark:border-neutral-700" /><textarea value={researchURLs} onChange={(event) => setResearchURLs(event.target.value)} placeholder="Uma URL HTTPS pública por linha" className="mt-2 min-h-20 w-full resize-y rounded-xl border border-neutral-300 bg-transparent px-3 py-2 text-sm outline-none dark:border-neutral-700" /><button type="button" disabled={busy || !researchQuery.trim() || !researchURLs.trim()} onClick={() => void runResearch()} className="mt-3 rounded-lg border border-neutral-300 px-3 py-2 text-sm dark:border-neutral-700">Pesquisar</button>{research && <div className="mt-4 space-y-2 text-sm"><p className="whitespace-pre-wrap text-neutral-700 dark:text-neutral-300">{research.summary}</p>{research.citations.map((citation) => <a key={citation.url} href={citation.url} target="_blank" rel="noreferrer" className="block rounded-lg border border-neutral-200 p-2 text-xs underline dark:border-neutral-800">{citation.title || citation.url}</a>)}</div>}</div>
      </section>

      {mission && <section className="grid min-h-0 gap-4 xl:grid-cols-[1.2fr_0.8fr]"><div className="rounded-2xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-950"><div className="flex items-start justify-between gap-3"><div><p className="text-xs text-neutral-500">{mission.id}</p><h2 className="mt-1 font-medium text-neutral-900 dark:text-neutral-100">{mission.objective}</h2></div><span className="rounded-full bg-neutral-100 px-3 py-1 text-xs font-medium dark:bg-neutral-800">{mission.state}</span></div><div className="mt-5 space-y-3">{events.map((event) => <div key={event.id} className="flex gap-3 text-sm"><div className="mt-1.5 h-2 w-2 shrink-0 rounded-full bg-neutral-500" /><div><p className="font-medium text-neutral-800 dark:text-neutral-200">{event.type}</p><p className="text-xs text-neutral-500">{event.step_id ?? "mission"} · {new Date(event.created_at).toLocaleString()}</p></div></div>)}</div><div className="mt-5 flex gap-2"><button type="button" disabled={busy || pendingApprovals.length > 0 || mission.state === "COMPLETED"} onClick={() => void run()} className="rounded-lg bg-neutral-900 px-3 py-2 text-sm text-white disabled:opacity-40 dark:bg-neutral-100 dark:text-neutral-900">Executar</button>{mission.artifacts?.map((artifact) => <a key={artifact.id} href={`/api/agent/v1/missions/${mission.id}/artifacts/${artifact.id}`} className="rounded-lg border border-neutral-300 px-3 py-2 text-sm dark:border-neutral-700">Baixar {artifact.name}</a>)}</div></div><div className="rounded-2xl border border-neutral-200 bg-white p-4 dark:border-neutral-800 dark:bg-neutral-950"><h2 className="font-medium text-neutral-900 dark:text-neutral-100">Approvals</h2>{pendingApprovals.length === 0 ? <p className="mt-3 text-sm text-neutral-500">Nenhuma aprovação pendente.</p> : pendingApprovals.map((approval) => <div key={approval.id} className="mt-3 rounded-xl border border-amber-200 bg-amber-50 p-3 dark:border-amber-900 dark:bg-amber-950/30"><p className="text-xs text-amber-800 dark:text-amber-200">{approval.step_id}</p><div className="mt-3 flex gap-2"><button type="button" disabled={busy} onClick={() => void decide(approval.id, true)} className="rounded-lg bg-emerald-600 px-3 py-2 text-xs font-medium text-white">Aprovar</button><button type="button" disabled={busy} onClick={() => void decide(approval.id, false)} className="rounded-lg bg-red-600 px-3 py-2 text-xs font-medium text-white">Rejeitar</button></div></div>)}</div></section>}
    </main>
  );
}
