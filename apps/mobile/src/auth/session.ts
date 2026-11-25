export type SessionKind = "guest" | "signedIn";

export type AuthSession = {
  kind: SessionKind;
  userId: string | null;
  email: string | null;
};

export function guestSession(): AuthSession {
  return { kind: "guest", userId: null, email: null };
}

export function signedInSession(userId: string, email: string): AuthSession {
  return { kind: "signedIn", userId, email };
}

export function sessionSkipsAuth(session: AuthSession | null): boolean {
  return session !== null;
}
