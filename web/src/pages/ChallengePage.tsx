import { FormEvent, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api, readError, type Challenge, type StandingEntry } from "../api";
import { useAuth } from "../auth";

export default function ChallengePage() {
  const { id } = useParams();
  const { user, loading } = useAuth();
  const [challenge, setChallenge] = useState<Challenge | null>(null);
  const [standings, setStandings] = useState<StandingEntry[]>([]);
  const [share, setShare] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    if (!id) return;
    const [cRes, sRes] = await Promise.all([api(`/api/v1/challenges/${id}`), api(`/api/v1/challenges/${id}/standings`)]);
    if (!cRes.ok) {
      setError(await readError(cRes));
      setChallenge(null);
      return;
    }
    setChallenge((await cRes.json()) as Challenge);
    if (sRes.ok) {
      const body = (await sRes.json()) as { entries: StandingEntry[] };
      setStandings(body.entries);
    }
  }

  useEffect(() => {
    if (!loading && user && id) void load();
  }, [loading, user, id]);

  async function onJoin(e: FormEvent) {
    e.preventDefault();
    if (!challenge) return;
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/challenges/${challenge.id}/join`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ share_matching_logs: share }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setShare(false);
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function onLeave() {
    if (!challenge) return;
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/challenges/${challenge.id}/leave`, { method: "POST" });
      if (!res.ok && res.status !== 204) {
        setError(await readError(res));
        return;
      }
      await load();
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
        to view this challenge.
      </p>
    );
  }
  if (!challenge) {
    return (
      <div className="space-y-2">
        <p className="text-stone-600">{error || "Challenge not found."}</p>
        <Link to="/" className="text-sm underline">
          Back
        </Link>
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <div>
        <Link to={`/circles/${challenge.circle_id}`} className="text-sm text-stone-600 underline">
          Circle
        </Link>
        <h1 className="mt-2 text-2xl font-semibold">{challenge.name}</h1>
        <p className="text-sm text-stone-600">
          {challenge.type} · {challenge.scoring_key}
          {challenge.direction ? ` · ${challenge.direction}` : ""} · {challenge.participant_count} joined · {challenge.join_policy}
        </p>
        <p className="text-sm text-stone-500">
          {new Date(challenge.starts_at).toLocaleString()} → {new Date(challenge.ends_at).toLocaleString()}
        </p>
      </div>

      {error ? <p className="text-sm text-red-700">{error}</p> : null}

      {challenge.joined ? (
        <section className="space-y-2">
          <p className="text-sm text-stone-600">You joined. Matching logs in this window are shared with the challenge.</p>
          <button type="button" className="rounded border border-red-300 px-4 py-2 text-red-800 disabled:opacity-50" disabled={busy} onClick={() => void onLeave()}>
            Leave
          </button>
        </section>
      ) : (
        <section className="space-y-3 rounded border border-stone-200 bg-white p-4">
          <h2 className="font-medium">Join</h2>
          <p className="text-sm text-stone-600">
            Participants will see your <strong>{challenge.type}</strong> logs during this challenge (not your other private health data). New matching logs are
            shared with the challenge automatically. You can stop sharing by leaving.
          </p>
          <form onSubmit={onJoin} className="space-y-3">
            <label className="flex items-start gap-2 text-sm">
              <input className="mt-1" type="checkbox" checked={share} onChange={(e) => setShare(e.target.checked)} required />
              <span>Share matching logs (visibility becomes challenge). Required to participate.</span>
            </label>
            <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy || !share} type="submit">
              Join
            </button>
          </form>
        </section>
      )}

      <section className="space-y-3">
        <h2 className="font-medium">Standings</h2>
        {standings.length === 0 ? (
          <p className="text-sm text-stone-600">No participants yet.</p>
        ) : (
          <ol className="divide-y divide-stone-200 rounded border border-stone-200 bg-white">
            {standings.map((e, i) => (
              <li key={e.user_id} className="flex items-center justify-between px-3 py-2 text-sm">
                <span>
                  <span className="mr-2 text-stone-400">{i + 1}</span>
                  {e.display_name}
                  {e.user_id === user.id ? " (you)" : ""}
                </span>
                <span className="tabular-nums">
                  {e.points}
                  {e.detail?.baseline_kg != null && e.detail?.current_kg != null ? (
                    <span className="ml-2 text-stone-500">
                      {e.detail.baseline_kg}→{e.detail.current_kg} kg
                    </span>
                  ) : null}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
    </div>
  );
}
