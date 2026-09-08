import { Link } from "react-router-dom";
import { useAuth } from "../auth";

export default function HomePage() {
  const { user, loading } = useAuth();
  if (loading) return <p className="text-stone-500">Loading…</p>;
  if (!user) {
    return (
      <div className="space-y-4">
        <h1 className="text-2xl font-semibold">Life, together.</h1>
        <p className="text-stone-600">Small circles. Shared rituals. Kind competitions.</p>
        <div className="flex gap-3">
          <Link to="/login" className="rounded border border-stone-300 px-4 py-2">
            Log in
          </Link>
          <Link to="/register" className="rounded bg-stone-900 px-4 py-2 text-white">
            Create account
          </Link>
        </div>
      </div>
    );
  }
  return (
    <div className="space-y-2">
      <h1 className="text-2xl font-semibold">Hello, {user.display_name}</h1>
      <p className="text-stone-600">{user.email_verified ? "Email verified." : "Check your inbox to verify email."}</p>
    </div>
  );
}
