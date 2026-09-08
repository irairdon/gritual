import { FormEvent, useState } from "react";
import { api, readError } from "../api";
import { useAuth } from "../auth";

export default function SettingsPage() {
  const { user, loading, setUser } = useAuth();
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) return <p className="text-stone-600">Log in to manage your account.</p>;

  async function onDelete(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api("/api/v1/me", {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ confirm_email: confirm }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setUser(null);
      location.assign("/login");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="max-w-md space-y-6">
      <h1 className="text-2xl font-semibold">Settings</h1>
      <p className="text-sm text-stone-600">
        Signed in as <span className="font-medium">{user.email}</span>
      </p>
      <section className="space-y-3 rounded border border-red-200 bg-red-50 p-4">
        <h2 className="font-medium text-red-900">Delete account</h2>
        <p className="text-sm text-red-800">This permanently removes your logs, sessions, and profile. Type your email to confirm.</p>
        <form onSubmit={onDelete} className="space-y-3">
          <input
            className="w-full rounded border border-red-300 bg-white px-3 py-2"
            type="email"
            placeholder={user.email}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            required
          />
          {error ? <p className="text-sm text-red-700">{error}</p> : null}
          <button className="rounded bg-red-700 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
            Delete my account
          </button>
        </form>
      </section>
    </div>
  );
}
