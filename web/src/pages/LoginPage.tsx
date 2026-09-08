import { FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api, readError, type LoginJSON } from "../api";
import { useAuth } from "../auth";

export default function LoginPage() {
  const { setUser } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [magicSent, setMagicSent] = useState(false);
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api("/api/v1/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      const body = (await res.json()) as LoginJSON;
      setUser(body.user);
      navigate("/");
    } finally {
      setBusy(false);
    }
  }

  async function sendMagic() {
    setError("");
    setBusy(true);
    try {
      const res = await api("/api/v1/auth/magic-link", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setMagicSent(true);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mx-auto max-w-sm space-y-4">
      <h1 className="text-2xl font-semibold">Log in</h1>
      <form onSubmit={onSubmit} className="space-y-3">
        <label className="block text-sm">
          Email
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoComplete="email"
          />
        </label>
        <label className="block text-sm">
          Password
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            autoComplete="current-password"
          />
        </label>
        {error ? <p className="text-sm text-red-700">{error}</p> : null}
        {magicSent ? <p className="text-sm text-stone-600">If that email has an account, we sent a sign-in link.</p> : null}
        <button className="w-full rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
          Log in
        </button>
      </form>
      <button className="w-full text-sm text-stone-600 underline" type="button" disabled={busy || !email} onClick={() => void sendMagic()}>
        Email me a magic link
      </button>
      <p className="text-sm text-stone-600">
        No account? <Link to="/register" className="underline">Register</Link>
      </p>
    </div>
  );
}
