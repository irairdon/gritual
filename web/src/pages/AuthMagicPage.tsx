import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, readError, type LoginJSON } from "../api";
import { useAuth } from "../auth";

export default function AuthMagicPage() {
  const { setUser } = useAuth();
  const navigate = useNavigate();
  const [message, setMessage] = useState("Signing you in…");

  useEffect(() => {
    const params = new URLSearchParams(location.hash.replace(/^#/, ""));
    const token = params.get("token") || "";
    if (!token) {
      setMessage("Missing sign-in token.");
      return;
    }
    let cancelled = false;
    (async () => {
      const res = await api("/api/v1/auth/magic-link/consume", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token }),
      });
      if (cancelled) return;
      if (!res.ok) {
        setMessage(await readError(res));
        return;
      }
      const body = (await res.json()) as LoginJSON;
      setUser(body.user);
      navigate("/");
    })();
    return () => {
      cancelled = true;
    };
  }, [navigate, setUser]);

  return <p className="text-stone-700">{message}</p>;
}
