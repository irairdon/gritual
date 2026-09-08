import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, readError, type Meal, type MealItem, type User } from "../api";
import { useAuth } from "../auth";
import { reencodeJPEG } from "../jpeg";

const CONSENT =
  "Meal photos and chat text are sent to SpaceXAI (xAI) to estimate food and reply. Gritual cannot delete copies xAI may retain. Do not upload photos of other people without their OK.";

type ItemEdit = {
  name: string;
  grams: string;
  kcal: string;
  protein_g: string;
  carbs_g: string;
  fat_g: string;
};

function toEdit(items: MealItem[]): ItemEdit[] {
  return items.map((it) => ({
    name: it.name,
    grams: it.grams == null ? "" : String(it.grams),
    kcal: String(it.kcal),
    protein_g: String(it.protein_g),
    carbs_g: String(it.carbs_g),
    fat_g: String(it.fat_g),
  }));
}

export default function MealsPage() {
  const { user, loading, setUser } = useAuth();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [agreed, setAgreed] = useState(false);
  const [draft, setDraft] = useState<Meal | null>(null);
  const [edits, setEdits] = useState<ItemEdit[]>([]);
  const [notes, setNotes] = useState("");
  const [day, setDay] = useState<{ date: string; kcal: number; protein_g: number; carbs_g: number; fat_g: number; items: Meal[] } | null>(
    null,
  );

  async function loadDay() {
    const res = await api("/api/v1/meals/day");
    if (!res.ok) {
      setError(await readError(res));
      return;
    }
    setDay(await res.json());
  }

  useEffect(() => {
    if (!loading && user) void loadDay();
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

  async function onPhoto(file: File | null) {
    if (!file) return;
    setError("");
    setBusy(true);
    try {
      const blob = await reencodeJPEG(file);
      const fd = new FormData();
      fd.append("file", blob, "meal.jpg");
      const res = await api("/api/v1/meals/photo", { method: "POST", body: fd });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      const meal = (await res.json()) as Meal;
      setDraft(meal);
      setEdits(toEdit(meal.items.length ? meal.items : [{ id: "", name: "", grams: null, kcal: 0, protein_g: 0, carbs_g: 0, fat_g: 0, source: "user" }]));
      setNotes(meal.notes ?? "");
    } catch (err) {
      setError(err instanceof Error ? err.message : "upload failed");
    } finally {
      setBusy(false);
    }
  }

  async function onConfirm(e: FormEvent) {
    e.preventDefault();
    if (!draft) return;
    setError("");
    setBusy(true);
    try {
      const items = edits
        .filter((it) => it.name.trim())
        .map((it) => ({
          name: it.name.trim(),
          grams: it.grams === "" ? null : Number(it.grams),
          kcal: Number(it.kcal || 0),
          protein_g: Number(it.protein_g || 0),
          carbs_g: Number(it.carbs_g || 0),
          fat_g: Number(it.fat_g || 0),
        }));
      const res = await api("/api/v1/meals", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ draft_id: draft.id, items, notes }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setDraft(null);
      setEdits([]);
      setNotes("");
      await loadDay();
    } finally {
      setBusy(false);
    }
  }

  async function onDiscard() {
    if (!draft) return;
    setBusy(true);
    try {
      await api(`/api/v1/logs/${draft.id}`, { method: "DELETE" });
      setDraft(null);
      setEdits([]);
    } finally {
      setBusy(false);
    }
  }

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) {
    return (
      <p className="text-stone-600">
        <Link to="/login" className="underline">
          Log in
        </Link>{" "}
        to log meals.
      </p>
    );
  }

  if (!user.email_verified) {
    return (
      <div className="space-y-3">
        <h1 className="text-2xl font-semibold">Meals</h1>
        <p className="text-sm text-stone-600">Verify your email before sending meal photos to SpaceXAI.</p>
      </div>
    );
  }

  if (!user.ai_consent_at) {
    return (
      <div className="max-w-lg space-y-4">
        <h1 className="text-2xl font-semibold">Meals</h1>
        <p className="text-sm text-stone-700">{CONSENT}</p>
        <p className="text-sm text-stone-600">
          Read the{" "}
          <Link className="underline" to="/privacy">
            privacy policy
          </Link>{" "}
          before consenting.
        </p>
        <form onSubmit={(e) => void onConsent(e)} className="space-y-3">
          <label className="flex items-start gap-2 text-sm">
            <input type="checkbox" className="mt-1" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} required />
            I have read the privacy policy and agree to send meal photos and chat text to SpaceXAI (xAI).
          </label>
          {error ? <p className="text-sm text-red-700">{error}</p> : null}
          <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy || !agreed} type="submit">
            Allow AI
          </button>
        </form>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <div>
        <h1 className="text-2xl font-semibold">Meals</h1>
        <p className="text-sm text-stone-600">Photo a plate. Confirm the draft before it counts.</p>
      </div>
      {error ? <p className="text-sm text-red-700">{error}</p> : null}

      <section className="space-y-3">
        <h2 className="font-medium">Photo</h2>
        <label className="block text-sm">
          <input
            className="mt-1 w-full text-sm"
            type="file"
            accept="image/*"
            capture="environment"
            disabled={busy}
            onChange={(e) => {
              const f = e.target.files?.[0] ?? null;
              e.target.value = "";
              void onPhoto(f);
            }}
          />
        </label>
      </section>

      {draft ? (
        <section className="space-y-3 rounded border border-stone-200 bg-white p-4">
          <h2 className="font-medium">Confirm draft</h2>
          {draft.photo_media_id ? <img src={`/media/${draft.photo_media_id}`} alt="" className="h-32 w-32 rounded object-cover" /> : null}
          <form onSubmit={(e) => void onConfirm(e)} className="space-y-3">
            {edits.map((it, i) => (
              <div key={i} className="grid grid-cols-6 gap-2">
                <input
                  className="col-span-2 rounded border border-stone-300 px-2 py-1 text-sm"
                  maxLength={80}
                  placeholder="food"
                  value={it.name}
                  onChange={(e) => setEdits(edits.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
                />
                <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="g" value={it.grams} onChange={(e) => setEdits(edits.map((x, j) => (j === i ? { ...x, grams: e.target.value } : x)))} />
                <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="kcal" value={it.kcal} onChange={(e) => setEdits(edits.map((x, j) => (j === i ? { ...x, kcal: e.target.value } : x)))} />
                <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="P" value={it.protein_g} onChange={(e) => setEdits(edits.map((x, j) => (j === i ? { ...x, protein_g: e.target.value } : x)))} />
                <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="C" value={it.carbs_g} onChange={(e) => setEdits(edits.map((x, j) => (j === i ? { ...x, carbs_g: e.target.value } : x)))} />
                <input className="rounded border border-stone-300 px-2 py-1 text-sm" placeholder="F" value={it.fat_g} onChange={(e) => setEdits(edits.map((x, j) => (j === i ? { ...x, fat_g: e.target.value } : x)))} />
              </div>
            ))}
            <button
              type="button"
              className="text-sm underline"
              onClick={() => setEdits([...edits, { name: "", grams: "", kcal: "", protein_g: "", carbs_g: "", fat_g: "" }])}
            >
              Add food
            </button>
            <label className="block text-sm">
              Notes
              <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={notes} onChange={(e) => setNotes(e.target.value)} />
            </label>
            <div className="flex gap-3">
              <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
                Confirm
              </button>
              <button type="button" className="text-sm text-red-800" disabled={busy} onClick={() => void onDiscard()}>
                Discard
              </button>
            </div>
          </form>
        </section>
      ) : null}

      <section className="space-y-3">
        <h2 className="font-medium">Today{day ? ` · ${Math.round(day.kcal)} kcal` : ""}</h2>
        {!day || day.items.length === 0 ? (
          <p className="text-sm text-stone-600">No confirmed meals today.</p>
        ) : (
          <ul className="divide-y divide-stone-200 rounded border border-stone-200 bg-white">
            {day.items.map((m) => (
              <li key={m.id} className="px-3 py-2 text-sm">
                <span className="font-medium">{Math.round(m.kcal)} kcal</span>
                <span className="text-stone-500">
                  {" "}
                  · P {Math.round(m.protein_g)} · C {Math.round(m.carbs_g)} · F {Math.round(m.fat_g)} ·{" "}
                  {m.items.map((it) => it.name).join(", ")}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
