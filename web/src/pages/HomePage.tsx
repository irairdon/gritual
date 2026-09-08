import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, readError, type Circle } from "../api";
import { useAuth } from "../auth";

export default function HomePage() {
  const { user, loading } = useAuth();
  const [circles, setCircles] = useState<Circle[]>([]);
  const [name, setName] = useState("");
  const [emoji, setEmoji] = useState("");
  const [tz, setTz] = useState("America/Denver");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    const res = await api("/api/v1/circles");
    if (!res.ok) {
      setError(await readError(res));
      return;
    }
    const body = (await res.json()) as { items: Circle[] };
    setCircles(body.items);
  }

  useEffect(() => {
    if (!loading && user) void load();
  }, [loading, user]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api("/api/v1/circles", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, emoji: emoji || null, tz }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setName("");
      setEmoji("");
      await load();
    } finally {
      setBusy(false);
    }
  }

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) {
    return (
      <div className="space-y-4">
        <h1 className="text-2xl font-semibold">Life, together.</h1>
        <p className="text-stone-600">Small circles. Shared rituals. Kind competitions.</p>
        <div className="flex gap-3">
          <Link to="/login" className="rounded border border-stone-300 px-4 py-2">
            Log in
          </Link>
          <Link to="/register" className="rounded bg-stone-900 px-4 py-2 text-white">
            Create account
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <div className="space-y-2">
        <h1 className="text-2xl font-semibold">Hello, {user.display_name}</h1>
        <p className="text-stone-600">{user.email_verified ? "Email verified." : "Check your inbox to verify email."}</p>
      </div>

      <section className="space-y-3">
        <h2 className="font-medium">Your circles</h2>
        {circles.length === 0 ? (
          <p className="text-sm text-stone-600">No circles yet. Create one and share an invite link.</p>
        ) : (
          <ul className="divide-y divide-stone-200 rounded border border-stone-200 bg-white">
            {circles.map((c) => (
              <li key={c.id}>
                <Link to={`/circles/${c.id}`} className="flex items-center justify-between px-3 py-2 hover:bg-stone-50">
                  <span>
                    {c.emoji ? `${c.emoji} ` : ""}
                    {c.name}
                  </span>
                  <span className="text-sm text-stone-500">
                    {c.member_count} · {c.role}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="space-y-3">
        <h2 className="font-medium">Create a circle</h2>
        <form onSubmit={onCreate} className="max-w-sm space-y-3">
          <label className="block text-sm">
            Name
            <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={name} onChange={(e) => setName(e.target.value)} required />
          </label>
          <label className="block text-sm">
            Emoji (optional)
            <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={emoji} onChange={(e) => setEmoji(e.target.value)} />
          </label>
          <label className="block text-sm">
            Timezone
            <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={tz} onChange={(e) => setTz(e.target.value)} required />
          </label>
          {error ? <p className="text-sm text-red-700">{error}</p> : null}
          <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
            Create
          </button>
        </form>
      </section>
    </div>
  );
}
