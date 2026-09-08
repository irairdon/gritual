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
  return p === "/login" || p === "/register" || p === "/auth/magic" || p === "/privacy";
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
  is_admin?: boolean;
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
