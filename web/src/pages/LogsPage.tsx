import { FormEvent, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api, readError, type LogItem, type Ritual } from "../api";
import { useAuth } from "../auth";

type LogKind = "weight" | "workout" | "habit" | "fishing" | "custom";

type SetRow = { exercise: string; reps: string; weight_kg: string; rpe: string };
type CatchRow = { species: string; count: string };

export default function LogsPage() {
  const { user, loading } = useAuth();
  const [kind, setKind] = useState<LogKind>("weight");
  const [logs, setLogs] = useState<LogItem[]>([]);
  const [rituals, setRituals] = useState<Ritual[]>([]);
  const [ritualId, setRitualId] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const [weight, setWeight] = useState("");
  const [weightUnit, setWeightUnit] = useState<"kg" | "lb">("kg");
  const [workoutTitle, setWorkoutTitle] = useState("");
  const [sets, setSets] = useState<SetRow[]>([{ exercise: "", reps: "", weight_kg: "", rpe: "" }]);
  const [habitStatus, setHabitStatus] = useState<"done" | "skip">("done");
  const [waterBody, setWaterBody] = useState("");
  const [lat, setLat] = useState("");
  const [lng, setLng] = useState("");
  const [catches, setCatches] = useState<CatchRow[]>([{ species: "", count: "1" }]);
  const [customValue, setCustomValue] = useState("");
  const [customUnit, setCustomUnit] = useState("");

  const matchingRituals = useMemo(() => rituals.filter((r) => r.type === kind), [rituals, kind]);

  async function load() {
    const [lRes, rRes] = await Promise.all([api("/api/v1/logs"), api("/api/v1/rituals")]);
    if (!lRes.ok) {
      setError(await readError(lRes));
      return;
    }
    setLogs(((await lRes.json()) as { items: LogItem[] }).items);
    if (rRes.ok) {
      setRituals(((await rRes.json()) as { items: Ritual[] }).items);
    }
  }

  useEffect(() => {
    if (!loading && user) {
      setWeightUnit(user.units === "imperial" ? "lb" : "kg");
      void load();
    }
  }, [loading, user]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const ritual_id = ritualId || null;
      let path = "";
      let body: Record<string, unknown> = { ritual_id };
      if (kind === "weight") {
        path = "/api/v1/weights";
        const n = Number(weight);
        body = { ...body, [weightUnit]: n };
      } else if (kind === "workout") {
        path = "/api/v1/workouts";
        body = {
          ...body,
          title: workoutTitle,
          sets: sets
            .filter((s) => s.exercise.trim())
            .map((s, i) => ({
              exercise: s.exercise,
              reps: s.reps === "" ? null : Number(s.reps),
              weight_kg: s.weight_kg === "" ? null : Number(s.weight_kg),
              rpe: s.rpe === "" ? null : Number(s.rpe),
              ordinal: i,
            })),
        };
      } else if (kind === "habit") {
        path = "/api/v1/habits";
        body = { ...body, status: habitStatus };
      } else if (kind === "fishing") {
        path = "/api/v1/fishing";
        body = {
          ...body,
          water_body: waterBody || null,
          lat: lat === "" ? null : Number(lat),
          lng: lng === "" ? null : Number(lng),
          catches: catches
            .filter((c) => c.species.trim() || c.count)
            .map((c) => ({ species: c.species || null, count: Number(c.count || "1") })),
        };
      } else {
        path = "/api/v1/customs";
        body = { ...body, value: Number(customValue), unit: customUnit };
      }
      const res = await api(path, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setWeight("");
      setWorkoutTitle("");
      setSets([{ exercise: "", reps: "", weight_kg: "", rpe: "" }]);
      setWaterBody("");
      setLat("");
      setLng("");
      setCatches([{ species: "", count: "1" }]);
      setCustomValue("");
      setCustomUnit("");
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function onDelete(id: string) {
    const res = await api(`/api/v1/logs/${id}`, { method: "DELETE" });
    if (!res.ok && res.status !== 204) {
      setError(await readError(res));
      return;
    }
    await load();
  }

  function summary(log: LogItem): string {
    if (log.weight) return `${log.weight.kg.toFixed(1)} kg (${log.weight.lb.toFixed(1)} lb)`;
    if (log.workout) return `${log.workout.title} · ${log.workout.sets.length} sets`;
    if (log.habit) return log.habit.status;
    if (log.fishing) return log.fishing.water_body || "trip";
    if (log.custom) return `${log.custom.value} ${log.custom.unit}`;
    return "";
  }

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) {
    return (
      <p className="text-stone-600">
        <Link to="/login" className="underline">
          Log in
        </Link>{" "}
        to log.
      </p>
    );
  }

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-semibold">Logs</h1>
        <p className="text-sm text-stone-600">Weight is stored in kg. Workouts are type workout.</p>
      </div>

      {error ? <p className="text-sm text-red-700">{error}</p> : null}

      <section className="space-y-3">
        <h2 className="font-medium">New log</h2>
        <form onSubmit={onSubmit} className="max-w-md space-y-3">
          <label className="block text-sm">
            Type
            <select
              className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
              value={kind}
              onChange={(e) => {
                setKind(e.target.value as LogKind);
                setRitualId("");
              }}
            >
              <option value="weight">weight</option>
              <option value="workout">workout</option>
              <option value="habit">habit</option>
              <option value="fishing">fishing</option>
              <option value="custom">custom</option>
            </select>
          </label>
          <label className="block text-sm">
            Ritual (optional)
            <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={ritualId} onChange={(e) => setRitualId(e.target.value)}>
              <option value="">None</option>
              {matchingRituals.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.title}
                </option>
              ))}
            </select>
          </label>

          {kind === "weight" ? (
            <div className="flex gap-2">
              <label className="block flex-1 text-sm">
                Weight
                <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" type="number" step="0.1" value={weight} onChange={(e) => setWeight(e.target.value)} required />
              </label>
              <label className="block text-sm">
                Unit
                <select className="mt-1 rounded border border-stone-300 px-3 py-2" value={weightUnit} onChange={(e) => setWeightUnit(e.target.value as "kg" | "lb")}>
                  <option value="kg">kg</option>
                  <option value="lb">lb</option>
                </select>
              </label>
            </div>
          ) : null}

          {kind === "workout" ? (
            <>
              <label className="block text-sm">
                Title
                <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={workoutTitle} onChange={(e) => setWorkoutTitle(e.target.value)} required />
              </label>
              {sets.map((s, i) => (
                <div key={i} className="grid grid-cols-4 gap-2">
                  <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="exercise" value={s.exercise} onChange={(e) => setSets(sets.map((x, j) => (j === i ? { ...x, exercise: e.target.value } : x)))} />
                  <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="reps" value={s.reps} onChange={(e) => setSets(sets.map((x, j) => (j === i ? { ...x, reps: e.target.value } : x)))} />
                  <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="kg" value={s.weight_kg} onChange={(e) => setSets(sets.map((x, j) => (j === i ? { ...x, weight_kg: e.target.value } : x)))} />
                  <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="rpe" value={s.rpe} onChange={(e) => setSets(sets.map((x, j) => (j === i ? { ...x, rpe: e.target.value } : x)))} />
                </div>
              ))}
              <button type="button" className="text-sm underline" onClick={() => setSets([...sets, { exercise: "", reps: "", weight_kg: "", rpe: "" }])}>
                Add set
              </button>
            </>
          ) : null}

          {kind === "habit" ? (
            <label className="block text-sm">
              Status
              <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={habitStatus} onChange={(e) => setHabitStatus(e.target.value as "done" | "skip")}>
                <option value="done">done</option>
                <option value="skip">skip</option>
              </select>
            </label>
          ) : null}

          {kind === "fishing" ? (
            <>
              <label className="block text-sm">
                Water body
                <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={waterBody} onChange={(e) => setWaterBody(e.target.value)} />
              </label>
              <div className="flex gap-2">
                <label className="block flex-1 text-sm">
                  Lat
                  <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={lat} onChange={(e) => setLat(e.target.value)} />
                </label>
                <label className="block flex-1 text-sm">
                  Lng
                  <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={lng} onChange={(e) => setLng(e.target.value)} />
                </label>
              </div>
              {catches.map((c, i) => (
                <div key={i} className="flex gap-2">
                  <input className="flex-1 rounded border border-stone-300 px-2 py-1 text-sm" placeholder="species" value={c.species} onChange={(e) => setCatches(catches.map((x, j) => (j === i ? { ...x, species: e.target.value } : x)))} />
                  <input className="w-20 rounded border border-stone-300 px-2 py-1 text-sm" placeholder="count" value={c.count} onChange={(e) => setCatches(catches.map((x, j) => (j === i ? { ...x, count: e.target.value } : x)))} />
                </div>
              ))}
              <button type="button" className="text-sm underline" onClick={() => setCatches([...catches, { species: "", count: "1" }])}>
                Add catch
              </button>
            </>
          ) : null}

          {kind === "custom" ? (
            <div className="flex gap-2">
              <label className="block flex-1 text-sm">
                Value
                <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" type="number" step="any" value={customValue} onChange={(e) => setCustomValue(e.target.value)} required />
              </label>
              <label className="block flex-1 text-sm">
                Unit
                <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={customUnit} onChange={(e) => setCustomUnit(e.target.value)} />
              </label>
            </div>
          ) : null}

          <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
            Save
          </button>
        </form>
      </section>

      <section className="space-y-3">
        <h2 className="font-medium">Recent</h2>
        {logs.length === 0 ? (
          <p className="text-sm text-stone-600">No logs yet.</p>
        ) : (
          <ul className="divide-y divide-stone-200 rounded border border-stone-200 bg-white">
            {logs.map((log) => (
              <li key={log.id} className="flex items-center justify-between px-3 py-2 text-sm">
                <span>
                  <span className="font-medium">{log.type}</span>
                  <span className="text-stone-500">
                    {" "}
                    · {new Date(log.logged_at).toLocaleString()} · {summary(log)}
                  </span>
                </span>
                <button type="button" className="text-red-800" onClick={() => void onDelete(log.id)}>
                  Delete
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
