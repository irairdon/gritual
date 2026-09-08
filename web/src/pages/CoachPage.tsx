import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, readError, type User } from "../api";
import { useAuth } from "../auth";

const CONSENT =
  "Meal photos and chat text are sent to SpaceXAI (xAI) to estimate food and reply. Gritual cannot delete copies xAI may retain. Do not upload photos of other people without their OK.";

type ChatLine = { role: "user" | "assistant" | "tool"; text: string };

export default function CoachPage() {
  const { user, loading, setUser } = useAuth();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [agreed, setAgreed] = useState(false);
  const [message, setMessage] = useState("");
  const [convId, setConvId] = useState<string | null>(null);
  const [lines, setLines] = useState<ChatLine[]>([]);

  useEffect(() => {
    if (loading || !user) return;
    void (async () => {
      const res = await api("/api/v1/ai/conversations");
      if (!res.ok) return;
      const body = (await res.json()) as { items: { id: string }[] };
      if (body.items?.[0]) setConvId(body.items[0].id);
    })();
  }, [loading, user]);

  async function onConsent(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api("/api/v1/me/ai-consent", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setUser((await res.json()) as User);
    } finally {
      setBusy(false);
    }
  }

  async function onSend(e: FormEvent) {
    e.preventDefault();
    const text = message.trim();
    if (!text) return;
    setError("");
    setBusy(true);
    setMessage("");
    setLines((cur) => [...cur, { role: "user", text }]);
    let assistant = "";
    try {
      const res = await fetch("/api/v1/ai/chat", {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json", Accept: "text/event-stream" },
        body: JSON.stringify({ conversation_id: convId, message: text }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      const reader = res.body?.getReader();
      if (!reader) {
        setError("no stream");
        return;
      }
      const dec = new TextDecoder();
      let buf = "";
      setLines((cur) => [...cur, { role: "assistant", text: "" }]);
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += dec.decode(value, { stream: true });
        const parts = buf.split("\n\n");
        buf = parts.pop() ?? "";
        for (const block of parts) {
          let event = "";
          let data = "";
          for (const line of block.split("\n")) {
            if (line.startsWith("event:")) event = line.slice(6).trim();
            if (line.startsWith("data:")) data += line.slice(5).trim();
          }
          if (!event || !data) continue;
          const payload = JSON.parse(data) as {
            text?: string;
            conversation_id?: string;
            name?: string;
            ok?: boolean;
            code?: string;
            message?: string;
          };
          if (event === "token" && payload.text) {
            assistant += payload.text;
            const snap = assistant;
            setLines((cur) => {
              const next = cur.slice();
              next[next.length - 1] = { role: "assistant", text: snap };
              return next;
            });
          }
          if (event === "tool_call" && payload.name) {
            setLines((cur) => [...cur, { role: "tool", text: `Using ${payload.name}…` }]);
          }
          if (event === "done" && payload.conversation_id) {
            setConvId(payload.conversation_id);
          }
          if (event === "error") {
            setError(payload.message || payload.code || "coach error");
          }
        }
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "chat failed");
    } finally {
      setBusy(false);
    }
  }

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) return <p className="text-stone-600">Log in to chat with the coach.</p>;

  if (!user.ai_consent_at) {
    return (
      <div className="max-w-lg space-y-4">
        <h1 className="text-2xl font-semibold">Coach</h1>
        <p className="text-sm text-stone-600">{CONSENT}</p>
        <p className="text-sm text-stone-600">
          Read the <Link to="/privacy" className="underline">privacy policy</Link> first.
        </p>
        <form onSubmit={(e) => void onConsent(e)} className="space-y-3 rounded border border-stone-200 bg-white p-4">
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} />
            I understand and agree.
          </label>
          {error ? <p className="text-sm text-red-700">{error}</p> : null}
          <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={!agreed || busy} type="submit">
            Enable AI
          </button>
        </form>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-lg space-y-4">
      <h1 className="text-2xl font-semibold">Coach</h1>
      <p className="text-sm text-stone-600">Friendly logging help — not medical advice.</p>
      <div className="min-h-64 space-y-2 rounded border border-stone-200 bg-white p-4">
        {lines.length === 0 ? <p className="text-sm text-stone-500">Ask to log a meal, weight, or check today&apos;s macros.</p> : null}
        {lines.map((ln, i) => (
          <p key={i} className={ln.role === "user" ? "text-stone-900" : "text-stone-600"}>
            <span className="font-medium">{ln.role === "user" ? "You" : ln.role === "tool" ? "Tool" : "Coach"}: </span>
            {ln.text}
          </p>
        ))}
      </div>
      {error ? <p className="text-sm text-red-700">{error}</p> : null}
      <form onSubmit={(e) => void onSend(e)} className="flex gap-2">
        <input
          className="flex-1 rounded border border-stone-300 px-3 py-2"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder="Message the coach"
          disabled={busy}
        />
        <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy || !message.trim()} type="submit">
          Send
        </button>
      </form>
    </div>
  );
}
