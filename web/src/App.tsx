import { useEffect, useState } from "react";

export default function App() {
  const [health, setHealth] = useState("checking…");

  useEffect(() => {
    fetch("/healthz")
      .then((r) => {
        if (!r.ok) throw new Error(String(r.status));
        return r.text();
      })
      .then((t) => setHealth(t.trim() || "ok"))
      .catch(() => setHealth("down"));
  }, []);

  return (
    <main className="min-h-screen p-8">
      <h1 className="text-2xl font-semibold">Gritual</h1>
      <p>health: {health}</p>
    </main>
  );
}
