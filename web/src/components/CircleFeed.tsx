import { FormEvent, useEffect, useState } from "react";
import { api, readError, type FeedPost, type FeedReaction } from "../api";

const EMOJIS: { key: FeedReaction; label: string }[] = [
  { key: "like", label: "👍" },
  { key: "fire", label: "🔥" },
  { key: "fish", label: "🐟" },
  { key: "strong", label: "💪" },
  { key: "heart", label: "❤️" },
];

type Props = {
  circleId: string;
  userId: string;
  role: "owner" | "admin" | "member";
};

export default function CircleFeed({ circleId, userId, role }: Props) {
  const [posts, setPosts] = useState<FeedPost[]>([]);
  const [body, setBody] = useState("");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    const res = await api(`/api/v1/circles/${circleId}/feed`);
    if (!res.ok) {
      setError(await readError(res));
      return;
    }
    const data = (await res.json()) as { items: FeedPost[] };
    setPosts(data.items);
  }

  useEffect(() => {
    void load();
  }, [circleId]);

  async function onPost(e: FormEvent) {
    e.preventDefault();
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/circles/${circleId}/feed`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ body }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setBody("");
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function onComment(postId: string) {
    const text = (drafts[postId] || "").trim();
    if (!text) return;
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/posts/${postId}/comments`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ body: text }),
      });
      if (!res.ok) {
        setError(await readError(res));
        return;
      }
      setDrafts((d) => ({ ...d, [postId]: "" }));
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function onDeleteComment(id: string) {
    setError("");
    setBusy(true);
    try {
      const res = await api(`/api/v1/comments/${id}`, { method: "DELETE" });
      if (!res.ok && res.status !== 204) {
        setError(await readError(res));
        return;
      }
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function onReact(post: FeedPost, emoji: FeedReaction) {
    setError("");
    const mine = post.my_reaction === emoji;
    const res = await api(`/api/v1/posts/${post.id}/reactions`, {
      method: mine ? "DELETE" : "PUT",
      headers: mine ? undefined : { "Content-Type": "application/json" },
      body: mine ? undefined : JSON.stringify({ emoji }),
    });
    if (!res.ok && res.status !== 204) {
      setError(await readError(res));
      return;
    }
    await load();
  }

  const canModerate = role === "owner" || role === "admin";

  return (
    <section className="space-y-3">
      <h2 className="font-medium">Feed</h2>
      {error ? <p className="text-sm text-red-700">{error}</p> : null}
      <form onSubmit={onPost} className="space-y-2 rounded border border-stone-200 bg-white p-3">
        <label className="block text-sm">
          Share with the circle
          <textarea
            className="mt-1 w-full rounded border border-stone-300 px-3 py-2"
            rows={2}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            required
          />
        </label>
        <button className="rounded bg-stone-900 px-4 py-2 text-white disabled:opacity-50" disabled={busy} type="submit">
          Post
        </button>
      </form>
      {posts.length === 0 ? (
        <p className="text-sm text-stone-600">No posts yet.</p>
      ) : (
        <ul className="space-y-3">
          {posts.map((p) => (
            <li key={p.id} className="space-y-2 rounded border border-stone-200 bg-white p-3">
              <div className="flex items-baseline justify-between gap-2">
                <span className="text-sm font-medium">{p.display_name || "Someone"}</span>
                <span className="text-xs text-stone-500">{new Date(p.created_at).toLocaleString()}</span>
              </div>
              {p.body ? <p className="text-sm whitespace-pre-wrap">{p.body}</p> : null}
              {p.log ? (
                <p className="text-xs text-stone-600">
                  Logged {p.log.type}
                  {p.log.notes ? ` — ${p.log.notes}` : ""}
                </p>
              ) : null}
              <div className="flex flex-wrap gap-1">
                {EMOJIS.map((e) => {
                  const active = p.my_reaction === e.key;
                  const n = p.reactions[e.key] || 0;
                  return (
                    <button
                      key={e.key}
                      type="button"
                      className={`rounded border px-2 py-0.5 text-sm ${active ? "border-stone-900 bg-stone-100" : "border-stone-200"}`}
                      onClick={() => void onReact(p, e.key)}
                    >
                      {e.label}
                      {n ? ` ${n}` : ""}
                    </button>
                  );
                })}
              </div>
              {p.comments.length > 0 ? (
                <ul className="space-y-1 border-t border-stone-100 pt-2">
                  {p.comments.map((c) => (
                    <li key={c.id} className="flex items-start justify-between gap-2 text-sm">
                      <span>
                        <span className="font-medium">{c.display_name || "Someone"}</span> {c.body}
                      </span>
                      {c.user_id === userId || canModerate ? (
                        <button type="button" className="text-xs text-stone-500 underline" disabled={busy} onClick={() => void onDeleteComment(c.id)}>
                          Delete
                        </button>
                      ) : null}
                    </li>
                  ))}
                </ul>
              ) : null}
              <form
                className="flex gap-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  void onComment(p.id);
                }}
              >
                <input
                  className="flex-1 rounded border border-stone-300 px-2 py-1 text-sm"
                  placeholder="Comment"
                  value={drafts[p.id] || ""}
                  onChange={(e) => setDrafts((d) => ({ ...d, [p.id]: e.target.value }))}
                />
                <button className="rounded border border-stone-300 px-2 py-1 text-sm disabled:opacity-50" disabled={busy} type="submit">
                  Reply
                </button>
              </form>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
