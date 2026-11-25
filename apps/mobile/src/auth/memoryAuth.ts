import { AuthPort, cloudSignInNotConfigured, emailPasswordRequired } from "./authPort";
import { AuthSession, guestSession, signedInSession } from "./session";

export type MemoryAuthOptions = {
  cloudSignIn: boolean;
};

export type MemoryAuth = {
  auth: AuthPort;
  networkCallCount: () => number;
};

export function createMemoryAuth(options: MemoryAuthOptions): MemoryAuth {
  let session: AuthSession | null = null;
  let networkCalls = 0;

  const auth: AuthPort = {
    restoreSession: async () => session,
    continueAsGuest: async () => {
      session = guestSession();
      return { ok: true, session };
    },
    signInWithGoogle: async () => {
      if (!options.cloudSignIn) {
        return { ok: false, message: cloudSignInNotConfigured };
      }
      networkCalls += 1;
      session = signedInSession("google-user", "user@gmail.com");
      return { ok: true, session };
    },
    signInWithEmail: async (email: string, password: string) => {
      if (!options.cloudSignIn) {
        return { ok: false, message: cloudSignInNotConfigured };
      }
      if (email.length === 0 || password.length === 0) {
        return { ok: false, message: emailPasswordRequired };
      }
      networkCalls += 1;
      session = signedInSession("email-user", email);
      return { ok: true, session };
    },
  };

  return {
    auth,
    networkCallCount: () => networkCalls,
  };
}
