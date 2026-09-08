import { FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, readError, type Challenge, type ChallengeType, type Circle, type CircleMember, type Invite } from "../api";
import { useAuth } from "../auth";
import CircleFeed from "../components/CircleFeed";
import InviteQR from "../components/InviteQR";

export default function CirclePage() {
  const { id } = useParams();
  const { user, loading } = useAuth();
  const navigate = useNavigate();
  const [circle, setCircle] = useState<Circle | null>(null);
  const [members, setMembers] = useState<CircleMember[]>([]);
  const [invite, setInvite] = useState<Invite | null>(null);
  const [challenges, setChallenges] = useState<Challenge[]>([]);
  const [chalName, setChalName] = useState("");
  const [chalType, setChalType] = useState<ChallengeType>("weight");
  const [chalScoring, setChalScoring] = useState("weight.progress");
  const [chalDirection, setChalDirection] = useState("at_most");
  const [chalStart, setChalStart] = useState("");
  const [chalEnd, setChalEnd] = useState("");
  const [chalShare, setChalShare] = useState(false);
  const [transferTo, setTransferTo] = useState("");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);

  const scoringOptions: Record<ChallengeType, string[]> = {
    weight: ["weight.progress"],
    workout: ["workout.volume"],
    habit: ["habit.completion"],
    fishing: ["fishing.days", "fishing.catches"],
    custom: ["custom.sum", "custom.average"],
  };

  async function load() {
    if (!id) return;
    const [cRes, mRes, chRes] = await Promise.all([
      api(`/api/v1/circles/${id}`),
      api(`/api/v1/circles/${id}/members`),
      api(`/api/v1/circles/${id}/challenges`),
    ]);
    if (!cRes.ok) {
      setError(await readError(cRes));
      setCircle(null);
      return;
    }
    setCircle((await cRes.json()) as Circle);
    if (mRes.ok) {
      const body = (await mRes.json()) as { items: CircleMember[] };
      setMembers(body.items);
    }
    if (chRes.ok) {
      const body = (await chRes.json()) as { items: Challenge[] };
      setChallenges(body.items);
    }
  }

  useEffect(() => {
    if (!loading && user && id) void load();
  }, [loading, user, id]);

  const others = useMemo(() => members.filter((m) => m.user_id !== user?.id), [members, user]);

  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) {
    return (
      <p className="text-stone-600">
        <Link to="/login" className="underline">
          Log in
        </Link>{" "}
        to view this circle.
      </p>
    );
  }
  if (!circle) {
    return (
      <div className="space-y-2">
        <p className="text-stone-600">{error || "Circle not found."}</p>
        <Link to="/" className="text-sm underline">
          Back
        </Link>
      </div>
    );
  }

  async function createInvite() {
    if (!circle) return;
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/circles/${circle.id}/invites`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: "{}",
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setInvite((await res.json()) as Invite);
      setCopied(false);
    } finally {
      setBusy(false);
    }
  }

  async function copyInvite() {
    if (!invite) return;
    await navigator.clipboard.writeText(invite.url);
    setCopied(true);
  }

  async function shareInvite() {
    if (!invite || !circle || !navigator.share) return;
    await navigator.share({ title: `Join ${circle.name}`, url: invite.url });
  }

  async function revokeInvite() {
    if (!invite || !circle) return;
    setBusy(true);
    setError("");
    try {
      const res = await api(`/api/v1/circles/${circle.id}/invites/${invite.id}`, { method: "DELETE" });
      if (!res.ok && res.status !== 204) {
        setError(await readError(res));
        return;
      }
      setInvite(null);
      setCopied(false);
    } finally {
      setBusy(false);
    }
  }

  async function onTransfer(e: FormEvent) {
    e.preventDefault();
    if (!circle) return;
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/circles/${circle.id}/transfer`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ user_id: transferTo }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function leave() {
    if (!circle || !user) return;
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/circles/${circle.id}/members/${user.id}`, { method: "DELETE" });
      if (!res.ok && res.status !== 204) {
        setError(await readError(res));
        return;
      }
      navigate("/");
    } finally {
      setBusy(false);
    }
  }

  const c = circle;
  const canInvite = c.role === "owner" || c.role === "admin";
  const ownerWithOthers = c.role === "owner" && others.length > 0;

  return (
    <div className="space-y-8">
      <div>
        <Link to="/" className="text-sm text-stone-600 underline">
          All circles
        </Link>
        <h1 className="mt-2 text-2xl font-semibold">
          {circle.emoji ? `${circle.emoji} ` : ""}
          {circle.name}
        </h1>
        <p className="text-sm text-stone-600">
          {circle.member_count} members · {circle.tz} · you are {circle.role}
        </p>
      </div>

      {error ? <p className="text-sm text-red-700">{error}</p> : null}

      <CircleFeed circleId={circle.id} userId={user.id} role={circle.role} />

      <section className="space-y-3">
        <h2 className="font-medium">Challenges</h2>
        {challenges.length === 0 ? (
          <p className="text-sm text-stone-600">No challenges yet. Creating one never enrolls the circle — each person joins.</p>
        ) : (
          <ul className="divide-y divide-stone-200 rounded border border-stone-200 bg-white">
            {challenges.map((ch) => (
              <li key={ch.id}>
                <Link to={`/challenges/${ch.id}`} className="flex items-center justify-between px-3 py-2 text-sm hover:bg-stone-50">
                  <span>
                    <span className="font-medium">{ch.name}</span>
                    <span className="text-stone-500">
                      {" "}
                      · {ch.type} · {ch.scoring_key}
                    </span>
                  </span>
                  <span className="text-stone-500">{ch.joined ? "joined" : "opt in"}</span>
                </Link>
              </li>
            ))}
          </ul>
        )}
        <form
          className="max-w-sm space-y-3 rounded border border-stone-200 bg-white p-4"
          onSubmit={(e) => {
            e.preventDefault();
            void (async () => {
              if (!circle) return;
              setError("");
              setBusy(true);
              try {
                const body: Record<string, unknown> = {
                  name: chalName,
                  type: chalType,
                  scoring_key: chalScoring,
                  starts_at: new Date(chalStart).toISOString(),
                  ends_at: new Date(chalEnd).toISOString(),
                };
                if (chalScoring === "weight.progress") body.direction = chalDirection;
                if (chalShare) body.share_matching_logs = true;
                const res = await api(`/api/v1/circles/${circle.id}/challenges`, {
                  method: "POST",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify(body),
                });
                if (!res.ok) {
                  setError(await readError(res));
                  return;
                }
                setChalName("");
                setChalShare(false);
                await load();
              } finally {
                setBusy(false);
              }
            })();
          }}
        >
          <h3 className="font-medium">Create challenge</h3>
          <label className="block text-sm">
            Name
            <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={chalName} onChange={(e) => setChalName(e.target.value)} required />
          </label>
          <label className="block text-sm">
            Type
            <select
              className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
              value={chalType}
              onChange={(e) => {
                const t = e.target.value as ChallengeType;
                setChalType(t);
                setChalScoring(scoringOptions[t][0]);
              }}
            >
              {(Object.keys(scoringOptions) as ChallengeType[]).map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </label>
          <label className="block text-sm">
            Scoring
            <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={chalScoring} onChange={(e) => setChalScoring(e.target.value)}>
              {scoringOptions[chalType].map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          {chalScoring === "weight.progress" ? (
            <label className="block text-sm">
              Direction
              <select className="mt-1 w-full rounded border border-stone-300 px-3 py-2" value={chalDirection} onChange={(e) => setChalDirection(e.target.value)}>
                <option value="at_most">at_most (loss)</option>
                <option value="at_least">at_least (gain)</option>
              </select>
            </label>
          ) : null}
          <label className="block text-sm">
            Starts
            <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" type="datetime-local" value={chalStart} onChange={(e) => setChalStart(e.target.value)} required />
          </label>
          <label className="block text-sm">
            Ends
            <input className="mt-1 w-full rounded border border-stone-300 px-3 py-2" type="datetime-local" value={chalEnd} onChange={(e) => setChalEnd(e.target.value)} required />
          </label>
          <label className="flex items-start gap-2 text-sm">
            <input className="mt-1" type="checkbox" checked={chalShare} onChange={(e) => setChalShare(e.target.checked)} />
            <span>Join now and share matching logs (unchecked: challenge exists with zero participants)</span>
          </label>
          <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
            Create
          </button>
        </form>
      </section>

      <section className="space-y-3">
        <h2 className="font-medium">Members</h2>
        <ul className="divide-y divide-stone-200 rounded border border-stone-200 bg-white">
          {members.map((m) => (
            <li key={m.user_id} className="flex items-center justify-between px-3 py-2 text-sm">
              <span>
                {m.display_name}
                {m.user_id === user.id ? " (you)" : ""}
              </span>
              <span className="text-stone-500">{m.role}</span>
            </li>
          ))}
        </ul>
      </section>

      {canInvite ? (
        <section className="space-y-3">
          <h2 className="font-medium">Invite</h2>
          <p className="text-sm text-stone-600">Link and QR only — invites are not emailed.</p>
          {invite ? (
            <div className="space-y-3 rounded border border-stone-200 bg-white p-4">
              <p className="break-all text-sm">{invite.url}</p>
              <div className="rounded bg-white p-2">
                <InviteQR url={invite.url} />
              </div>
              <div className="flex flex-wrap gap-2">
                <button type="button" className="rounded bg-stone-900 px-3 py-1.5 text-sm text-white" onClick={() => void copyInvite()}>
                  {copied ? "Copied" : "Copy link"}
                </button>
                {typeof navigator.share === "function" ? (
                  <button type="button" className="rounded border border-stone-300 px-3 py-1.5 text-sm" onClick={() => void shareInvite()}>
                    Share
                  </button>
                ) : null}
                <button type="button" className="rounded border border-stone-300 px-3 py-1.5 text-sm" disabled={busy} onClick={() => void revokeInvite()}>
                  Revoke
                </button>
              </div>
            </div>
          ) : (
            <button
              type="button"
              className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50"
              disabled={busy}
              onClick={() => void createInvite()}
            >
              Create invite link
            </button>
          )}
        </section>
      ) : null}

      {circle.role === "owner" && others.length > 0 ? (
        <section className="space-y-3">
          <h2 className="font-medium">Transfer ownership</h2>
          <form onSubmit={onTransfer} className="flex flex-wrap items-end gap-2">
            <label className="block text-sm">
              New owner
              <select
                className="mt-1 block rounded border border-stone-300 px-3 py-2"
                value={transferTo}
                onChange={(e) => setTransferTo(e.target.value)}
                required
              >
                <option value="">Select member</option>
                {others.map((m) => (
                  <option key={m.user_id} value={m.user_id}>
                    {m.display_name}
                  </option>
                ))}
              </select>
            </label>
            <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
              Transfer
            </button>
          </form>
        </section>
      ) : null}

      <section className="space-y-2">
        <h2 className="font-medium">Leave</h2>
        {ownerWithOthers ? (
          <p className="text-sm text-stone-600">Transfer ownership before leaving.</p>
        ) : (
          <button type="button" className="rounded border border-red-300 px-4 py-2 text-red-800 disabled:opacity-50" disabled={busy} onClick={() => void leave()}>
            Leave circle
          </button>
        )}
      </section>
    </div>
  );
}
