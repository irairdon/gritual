import { Link, Outlet } from "react-router-dom";
import { useAuth } from "../auth";

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
      <main className="mx-auto max-w-3xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
}
