import { AuthPort, AuthResult, emailPasswordRequired } from "./authPort";
import { AuthSession, guestSession, signedInSession } from "./session";

export type AuthUser = {
  id: string;
  email: string | undefined;
};

export type PasswordSignInResult = {
  user: AuthUser | null;
  error: string | null;
};

export type AuthClient = {
  restoreUser(): Promise<AuthUser | null>;
  signInWithPassword(email: string, password: string): Promise<PasswordSignInResult>;
  signInWithGoogle(): Promise<PasswordSignInResult>;
};

function sessionFromUser(user: AuthUser | null, error: string | null): AuthResult {
  if (error !== null) {
    return { ok: false, message: error };
  }
  if (user === null || user.email === undefined) {
    return { ok: false, message: "Sign-in did not return a user" };
  }
  return { ok: true, session: signedInSession(user.id, user.email) };
}

export function createSupabaseAuth(client: AuthClient): AuthPort {
  let guest: AuthSession | null = null;

  return {
    restoreSession: async () => {
      if (guest !== null) {
        return guest;
      }
      const user = await client.restoreUser();
      if (user === null || user.email === undefined) {
        return null;
      }
      return signedInSession(user.id, user.email);
    },
    continueAsGuest: async () => {
      guest = guestSession();
      return { ok: true, session: guest };
    },
    signInWithGoogle: async () => {
      guest = null;
      const result = await client.signInWithGoogle();
      return sessionFromUser(result.user, result.error);
    },
    signInWithEmail: async (email: string, password: string) => {
      if (email.length === 0 || password.length === 0) {
        return { ok: false, message: emailPasswordRequired };
      }
      guest = null;
      const result = await client.signInWithPassword(email, password);
      return sessionFromUser(result.user, result.error);
    },
  };
}
