import { useEffect, useState } from "react";
import { Link, Outlet } from "react-router-dom";
import { useAuth } from "../auth";

function OfflineBanner() {
  const [offline, setOffline] = useState(() => (typeof navigator !== "undefined" ? !navigator.onLine : false));

  useEffect(() => {
    const goOnline = () => setOffline(false);
    const goOffline = () => setOffline(true);
    window.addEventListener("online", goOnline);
    window.addEventListener("offline", goOffline);
    return () => {
      window.removeEventListener("online", goOnline);
      window.removeEventListener("offline", goOffline);
    };
  }, []);

  if (!offline) {
    return null;
  }
  return (
    <div role="status" className="border-b border-amber-200 bg-amber-50 px-4 py-2 text-center text-sm text-amber-950">
      You’re offline
    </div>
  );
}

export default function Layout() {
  const { user, logout } = useAuth();

  return (
    <div className="min-h-screen bg-stone-50 text-stone-900">
      <header className="border-b border-stone-200 bg-white">
        <div className="mx-auto flex max-w-3xl items-center justify-between px-4 py-3">
          <Link to="/" className="text-xl font-semibold tracking-tight">
            Gritual
          </Link>
          <nav className="flex items-center gap-4 text-sm">
            <Link to="/privacy" className="text-stone-600 hover:text-stone-900">
              Privacy
            </Link>
            {user ? (
              <>
                <Link to="/" className="text-stone-600 hover:text-stone-900">
                  Circles
                </Link>
                <Link to="/rituals" className="text-stone-600 hover:text-stone-900">
                  Rituals
                </Link>
                <Link to="/logs" className="text-stone-600 hover:text-stone-900">
                  Logs
                </Link>
                <Link to="/meals" className="text-stone-600 hover:text-stone-900">
                  Meals
                </Link>
                <Link to="/coach" className="text-stone-600 hover:text-stone-900">
                  Coach
                </Link>
                <Link to="/settings" className="text-stone-600 hover:text-stone-900">
                  Settings
                </Link>
                <button type="button" onClick={() => void logout()} className="text-stone-600 hover:text-stone-900">
                  Log out
                </button>
              </>
            ) : (
              <>
                <Link to="/login" className="text-stone-600 hover:text-stone-900">
                  Log in
                </Link>
                <Link to="/register" className="rounded bg-stone-900 px-3 py-1.5 text-white">
                  Register
                </Link>
              </>
            )}
          </nav>
        </div>
      </header>
      <OfflineBanner />
      <main className="mx-auto max-w-3xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
}
