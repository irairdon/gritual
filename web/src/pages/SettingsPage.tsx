import { FormEvent, useEffect, useState } from "react";
import { api, readError, type User } from "../api";
import { useAuth } from "../auth";
import { reencodeJPEG } from "../jpeg";

type AgentToken = {
  id: string;
  name: string;
  prefix: string;
  created_at: string;
  last_used_at: string | null;
};

export default function SettingsPage() {
  const { user, loading, setUser } = useAuth();
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [avatarFile, setAvatarFile] = useState<File | null>(null);
  const [tokens, setTokens] = useState<AgentToken[]>([]);
  const [tokenName, setTokenName] = useState("");
  const [shownToken, setShownToken] = useState("");

  async function loadTokens() {
    const res = await api("/api/v1/me/tokens");
    if (!res.ok) return;
    const body = (await res.json()) as { items: AgentToken[] };
    setTokens(body.items ?? []);
  }

  useEffect(() => {
    if (!loading && user) void loadTokens();
  }, [loading, user]);

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) return <p className="text-stone-600">Log in to manage your account.</p>;

  async function onSave(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (!user) return;
    setError("");
    setSaved("");
    setBusy(true);
    const form = e.currentTarget;
    const current = user;
    try {
      let avatarId = current.avatar_media_id ?? undefined;
      if (avatarFile) {
        const blob = await reencodeJPEG(avatarFile);
        const fd = new FormData();
        fd.append("file", blob, "avatar.jpg");
        const up = await api("/api/v1/media", { method: "POST", body: fd });
        if (!up.ok) {
          setError(await readError(up));
          return;
        }
        const obj = (await up.json()) as { id: string };
        avatarId = obj.id;
      }
      const data = new FormData(form);
      const num = (key: string) => {
        const v = String(data.get(key) ?? "").trim();
        if (!v) return undefined;
        const n = Number(v);
        return Number.isFinite(n) ? n : undefined;
      };
      const body: Record<string, unknown> = {
        display_name: String(data.get("display_name") ?? "").trim(),
        units: String(data.get("units") ?? "imperial"),
        tz: String(data.get("tz") ?? "").trim(),
        calorie_goal: num("calorie_goal"),
        protein_goal_g: num("protein_goal_g"),
        bio: String(data.get("bio") ?? ""),
        height_cm: num("height_cm"),
      };
      if (avatarId) body.avatar_media_id = avatarId;
      const res = await api("/api/v1/me", {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setUser((await res.json()) as User);
      setAvatarFile(null);
      setSaved("Saved.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "save failed");
    } finally {
      setBusy(false);
    }
  }

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

      <form onSubmit={(e) => void onSave(e)} className="space-y-3 rounded border border-stone-200 bg-white p-4">
        <h2 className="font-medium">Profile</h2>
        {user.avatar_media_id ? (
          <img
            src={`/media/${user.avatar_media_id}`}
            alt=""
            className="h-20 w-20 rounded-full object-cover"
          />
        ) : null}
        <label className="block text-sm">
          Display name
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            name="display_name"
            defaultValue={user.display_name}
            required
          />
        </label>
        <label className="block text-sm">
          Units
          <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" name="units" defaultValue={user.units ?? "imperial"}>
            <option value="imperial">Imperial</option>
            <option value="metric">Metric</option>
          </select>
        </label>
        <label className="block text-sm">
          Time zone
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            name="tz"
            defaultValue={user.tz ?? "America/Denver"}
            required
          />
        </label>
        <label className="block text-sm">
          Daily calorie goal
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            name="calorie_goal"
            type="number"
            min={0}
            defaultValue={user.calorie_goal ?? ""}
          />
        </label>
        <label className="block text-sm">
          Daily protein goal (g)
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            name="protein_goal_g"
            type="number"
            min={0}
            defaultValue={user.protein_goal_g ?? ""}
          />
        </label>
        <label className="block text-sm">
          Bio
          <textarea
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            name="bio"
            rows={3}
            defaultValue={user.bio ?? ""}
          />
        </label>
        <label className="block text-sm">
          Height (cm)
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            name="height_cm"
            type="number"
            min={0}
            step="0.1"
            defaultValue={user.height_cm ?? ""}
          />
        </label>
        <label className="block text-sm">
          Avatar
          <input
            className="mt-1 w-full text-sm"
            type="file"
            accept="image/jpeg,image/png"
            onChange={(e) => setAvatarFile(e.target.files?.[0] ?? null)}
          />
        </label>
        {error ? <p className="text-sm text-red-700">{error}</p> : null}
        {saved ? <p className="text-sm text-stone-600">{saved}</p> : null}
        <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
          Save profile
        </button>
      </form>

      <section className="space-y-3 rounded border border-stone-200 bg-white p-4">
        <h2 className="font-medium">Agents</h2>
        <p className="text-sm text-stone-600">
          Personal access tokens for MCP at <code>/mcp</code>. Shown once. Claude and Cursor use{" "}
          <code>Authorization: Bearer</code>.
        </p>
        <ul className="space-y-2 text-sm">
          {tokens.map((t) => (
            <li key={t.id} className="flex items-center justify-between gap-2">
              <span>
                {t.name} <span className="text-stone-500">{t.prefix}…</span>
              </span>
              <button
                type="button"
                className="text-red-700"
                onClick={() => {
                  void (async () => {
                    const res = await api(`/api/v1/me/tokens/${t.id}`, { method: "DELETE" });
                    if (!res.ok) {
                      setError(await readError(res));
                      return;
                    }
                    await loadTokens();
                  })();
                }}
              >
                Revoke
              </button>
            </li>
          ))}
        </ul>
        {shownToken ? (
          <p className="break-all rounded bg-stone-100 p-2 text-sm">
            Copy now: <code>{shownToken}</code>
          </p>
        ) : null}
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            void (async () => {
              setError("");
              setShownToken("");
              const res = await api("/api/v1/me/tokens", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ name: tokenName || "mcp" }),
              });
              if (!res.ok) {
                setError(await readError(res));
                return;
              }
              const created = (await res.json()) as AgentToken & { token: string };
              setShownToken(created.token);
              setTokenName("");
              await loadTokens();
            })();
          }}
        >
          <input
            className="flex-1 rounded border border-stone-300 px-3 py-2"
            placeholder="Token name"
            value={tokenName}
            onChange={(e) => setTokenName(e.target.value)}
          />
          <button className="rounded bg-stone-900 px-4 py-2 text-white" type="submit">
            Create
          </button>
        </form>
      </section>

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
          <button className="rounded bg-red-700 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
            Delete my account
          </button>
        </form>
      </section>
    </div>
  );
}
