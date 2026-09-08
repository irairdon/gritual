async function getBearer(): Promise<string | null> {
  // v1 web: always null. v1.1+: Keychain.
  return null;
}

export async function api(path: string, init: RequestInit = {}): Promise<Response> {
  const bearer = await getBearer();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (bearer) headers.set("Authorization", `Bearer ${bearer}`);
  return fetch(path, {
    ...init,
    headers,
    credentials: bearer ? "omit" : "include",
  });
}
