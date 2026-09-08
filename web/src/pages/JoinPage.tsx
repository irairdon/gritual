import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, readError, type Circle } from "../api";
import { useAuth } from "../auth";

export default function JoinPage() {
  const { token } = useParams();
  const { user, loading } = useAuth();
  const navigate = useNavigate();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const next = `/join/${token ?? ""}`;

  async function accept() {
    if (!token) return;
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/invites/${encodeURIComponent(token)}/accept`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      const circle = (await res.json()) as Circle;
      navigate(`/circles/${circle.id}`);
    } finally {
      setBusy(false);
    }
  }

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) {
    return (
      <div className="space-y-4">
        <h1 className="text-2xl font-semibold">Join a circle</h1>
        <p className="text-stone-600">Log in or create an account (18+) to accept this invite.</p>
        <div className="flex gap-3">
          <Link to={`/login?next=${encodeURIComponent(next)}`} className="rounded border border-stone-300 px-4 py-2">
            Log in
          </Link>
          <Link to={`/register?next=${encodeURIComponent(next)}`} className="rounded bg-stone-900 px-4 py-2 text-white">
            Create account
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <h1 className="text-2xl font-semibold">Join a circle</h1>
      <p className="text-stone-600">You were invited to a Gritual circle. Invites are not emailed; this page is the join path.</p>
      {error ? <p className="text-sm text-red-700">{error}</p> : null}
      <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy || !token} onClick={() => void accept()}>
        Accept invite
      </button>
    </div>
  );
}
