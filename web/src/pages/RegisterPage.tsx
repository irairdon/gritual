import { FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { api, readError, type LoginJSON } from "../api";
import { useAuth } from "../auth";

export default function RegisterPage() {
  const { setUser } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [dob, setDob] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api("/api/v1/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password, display_name: displayName, dob }),
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

  return (
    <div className="mx-auto max-w-sm space-y-4">
      <h1 className="text-2xl font-semibold">Create account</h1>
      <p className="text-sm text-stone-600">You must be 18 or older. See our <Link to="/privacy" className="underline">privacy</Link> page.</p>
      <form onSubmit={onSubmit} className="space-y-3">
        <label className="block text-sm">
          Display name
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            autoComplete="nickname"
          />
        </label>
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
            minLength={8}
            autoComplete="new-password"
          />
        </label>
        <label className="block text-sm">
          Date of birth
          <input
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            type="date"
            value={dob}
            onChange={(e) => setDob(e.target.value)}
            required
          />
        </label>
        {error ? <p className="text-sm text-red-700">{error}</p> : null}
        <button className="w-full rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
          Register
        </button>
      </form>
      <p className="text-sm text-stone-600">
        Already have an account? <Link to="/login" className="underline">Log in</Link>
      </p>
    </div>
  );
}
