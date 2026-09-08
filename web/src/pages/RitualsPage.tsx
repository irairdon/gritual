import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, readError, type Circle, type Ritual, type RitualType } from "../api";
import { useAuth } from "../auth";

const types: RitualType[] = ["weight", "workout", "habit", "fishing", "meal", "custom"];

export default function RitualsPage() {
  const { user, loading } = useAuth();
  const [rituals, setRituals] = useState<Ritual[]>([]);
  const [circles, setCircles] = useState<Circle[]>([]);
  const [type, setType] = useState<RitualType>("habit");
  const [title, setTitle] = useState("");
  const [direction, setDirection] = useState("at_least");
  const [period, setPeriod] = useState("none");
  const [circleId, setCircleId] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    const [rRes, cRes] = await Promise.all([api("/api/v1/rituals"), api("/api/v1/circles")]);
    if (!rRes.ok) {
      setError(await readError(rRes));
      return;
    }
    const body = (await rRes.json()) as { items: Ritual[] };
    setRituals(body.items);
    if (cRes.ok) {
      const cBody = (await cRes.json()) as { items: Circle[] };
      setCircles(cBody.items);
    }
  }

  useEffect(() => {
    if (!loading && user) void load();
  }, [loading, user]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api("/api/v1/rituals", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          type,
          title,
          direction,
          period,
          circle_id: circleId || null,
        }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setTitle("");
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: string) {
    setError("");
    const res = await api(`/api/v1/rituals/${id}`, { method: "DELETE" });
    if (!res.ok && res.status !== 204) {
      setError(await readError(res));
      return;
    }
    await load();
  }

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) {
    return (
      <p className="text-stone-600">
        <Link to="/login" className="underline">
          Log in
        </Link>{" "}
        to manage rituals.
      </p>
    );
  }

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-semibold">Rituals</h1>
        <p className="text-sm text-stone-600">Personal or circle goals. Canonical type is workout, not strength.</p>
      </div>

      {error ? <p className="text-sm text-red-700">{error}</p> : null}

      <section className="space-y-3">
        <h2 className="font-medium">Yours</h2>
        {rituals.length === 0 ? (
          <p className="text-sm text-stone-600">No rituals yet.</p>
        ) : (
          <ul className="divide-y divide-stone-200 rounded border border-stone-200 bg-white">
            {rituals.map((r) => (
              <li key={r.id} className="flex items-center justify-between gap-3 px-3 py-2 text-sm">
                <span>
                  <span className="font-medium">{r.title}</span>
                  <span className="text-stone-500">
                    {" "}
                    · {r.type} · {r.scoring_key || "unscored"} · {r.direction}
                  </span>
                </span>
                <button type="button" className="text-red-800" onClick={() => void onDelete(r.id)}>
                  Delete
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="space-y-3">
        <h2 className="font-medium">Create</h2>
        <form onSubmit={onCreate} className="max-w-sm space-y-3">
          <label className="block text-sm">
            Type
            <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={type} onChange={(e) => setType(e.target.value as RitualType)}>
              {types.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            Title
            <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={title} onChange={(e) => setTitle(e.target.value)} required />
          </label>
          <label className="block text-sm">
            Direction
            <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={direction} onChange={(e) => setDirection(e.target.value)}>
              <option value="at_least">at_least</option>
              <option value="at_most">at_most</option>
              <option value="hit">hit</option>
            </select>
          </label>
          <label className="block text-sm">
            Period
            <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={period} onChange={(e) => setPeriod(e.target.value)}>
              <option value="none">none</option>
              <option value="daily">daily</option>
              <option value="weekly">weekly</option>
              <option value="season">season</option>
              <option value="date_range">date_range</option>
            </select>
          </label>
          <label className="block text-sm">
            Circle (optional)
            <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={circleId} onChange={(e) => setCircleId(e.target.value)}>
              <option value="">Personal</option>
              {circles.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.emoji ? `${c.emoji} ` : ""}
                  {c.name}
                </option>
              ))}
            </select>
          </label>
          <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
            Create
          </button>
        </form>
      </section>
    </div>
  );
}
