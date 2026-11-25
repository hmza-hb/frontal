import { AuthSession } from "./session";

export type AuthSuccess = {
  ok: true;
  session: AuthSession;
};

export type AuthFailure = {
  ok: false;
  message: string;
};

export type AuthResult = AuthSuccess | AuthFailure;

export type AuthPort = {
  restoreSession(): Promise<AuthSession | null>;
  continueAsGuest(): Promise<AuthResult>;
  signInWithGoogle(): Promise<AuthResult>;
  signInWithEmail(email: string, password: string): Promise<AuthResult>;
};

export const cloudSignInNotConfigured = "Cloud sign-in is not configured";
export const emailPasswordRequired = "Email and password are required";
