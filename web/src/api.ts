declare global {
  interface Window {
    __gritualNative?: {
      getAccessToken?: () => Promise<string | null>;
      getRefreshToken?: () => Promise<string | null>;
      setTokens?: (access: string, refresh: string) => Promise<void>;
      clearTokens?: () => Promise<void>;
    };
  }
}

let refreshInFlight: Promise<boolean> | null = null;

async function getBearer(): Promise<string | null> {
  // v1 web: always null. v1.1+: read access token from Keychain plugin.
  return window.__gritualNative?.getAccessToken?.() ?? null;
}

function isPublicAuthPath(): boolean {
  const p = location.pathname;
  return p === "/login" || p === "/register" || p === "/auth/magic" || p === "/privacy" || p.startsWith("/join/");
}

export async function api(path: string, init: RequestInit = {}, isRetry = false): Promise<Response> {
  const bearer = await getBearer();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (bearer) headers.set("Authorization", `Bearer ${bearer}`);
  const res = await fetch(path, {
    ...init,
    headers,
    credentials: bearer ? "omit" : "include",
  });
  if (res.status !== 401 || isRetry) {
    if (res.status === 401 && !isPublicAuthPath()) location.assign("/login");
    return res;
  }
  if (bearer) {
    const ok = await singleFlightRefresh();
    if (ok) return api(path, init, true);
  }
  if (!isPublicAuthPath()) location.assign("/login");
  return res;
}

function singleFlightRefresh(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = (async () => {
      const rt = await window.__gritualNative?.getRefreshToken?.();
      if (!rt) return false;
      const r = await fetch("/api/v1/auth/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: rt }),
      });
      if (!r.ok) {
        await window.__gritualNative?.clearTokens?.();
        return false;
      }
      const body = await r.json();
      await window.__gritualNative?.setTokens?.(body.access_token, body.refresh_token);
      return true;
    })().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

export type User = {
  id: string;
  email: string;
  display_name: string;
  email_verified: boolean;
  units?: string;
  tz?: string;
  calorie_goal?: number | null;
  protein_goal_g?: number | null;
  bio?: string | null;
  height_cm?: number | null;
  avatar_media_id?: string | null;
  is_admin?: boolean;
  ai_consent_at?: string | null;
};

export type LoginJSON = {
  user: User;
  access_token: string;
  refresh_token: string;
  expires_in: number;
};

export async function readError(res: Response): Promise<string> {
  try {
    const body = (await res.json()) as { error?: { message?: string } };
    return body.error?.message || res.statusText;
  } catch {
    return res.statusText;
  }
}

export type Circle = {
  id: string;
  name: string;
  emoji: string | null;
  tz: string;
  role: "owner" | "admin" | "member";
  member_count: number;
};

export type CircleMember = {
  user_id: string;
  display_name: string;
  role: "owner" | "admin" | "member";
  joined_at: string;
};

export type Invite = {
  id: string;
  token: string;
  url: string;
  expires_at: string;
  max_uses: number | null;
};

export type RitualType = "weight" | "workout" | "habit" | "fishing" | "meal" | "custom";

export type Ritual = {
  id: string;
  owner_user_id: string | null;
  circle_id: string | null;
  type: RitualType;
  title: string;
  target_value: number | null;
  target_unit: string | null;
  direction: "at_least" | "at_most" | "hit";
  period: "none" | "daily" | "weekly" | "season" | "date_range";
  scoring_key: string;
  created_at: string;
};

export type ChallengeType = "weight" | "workout" | "habit" | "fishing" | "custom";

export type Challenge = {
  id: string;
  circle_id: string;
  ritual_id: string | null;
  type: ChallengeType;
  scoring_key: string;
  direction: "at_most" | "at_least" | null;
  name: string;
  starts_at: string;
  ends_at: string;
  require_photo: boolean;
  join_policy: "opt_in";
  created_at: string;
  joined: boolean;
  participant_count: number;
};

export type StandingEntry = {
  user_id: string;
  display_name: string;
  points: number;
  last_event_at: string | null;
  detail?: { baseline_kg?: number; current_kg?: number };
};

export type LogItem = {
  id: string;
  type: RitualType;
  logged_at: string;
  visibility: string;
  notes: string | null;
  ritual_id: string | null;
  challenge_id?: string | null;
  weight?: { kg: number; lb: number };
  workout?: {
    title: string;
    sets: { exercise: string; reps: number | null; weight_kg: number | null; rpe: number | null; ordinal: number }[];
  };
  habit?: { status: "done" | "skip" };
  fishing?: {
    water_body: string | null;
    lat: number | null;
    lng: number | null;
    catches: { species: string | null; count: number }[];
  };
  custom?: { value: number; unit: string };
  meal?: { status: string; kcal: number; protein_g: number; carbs_g: number; fat_g: number };
};

export type MealItem = {
  id: string;
  name: string;
  grams: number | null;
  kcal: number;
  protein_g: number;
  carbs_g: number;
  fat_g: number;
  source: string;
};

export type Meal = {
  id: string;
  status: "draft" | "confirmed";
  logged_at: string;
  visibility: string;
  notes: string | null;
  kcal: number;
  protein_g: number;
  carbs_g: number;
  fat_g: number;
  confidence: number | null;
  photo_media_id: string | null;
  items: MealItem[];
};

export function safeNext(raw: string | null): string {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/";
  return raw;
}
