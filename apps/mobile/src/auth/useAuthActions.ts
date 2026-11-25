import { useState } from "react";
import { AuthPort, AuthResult } from "./authPort";
import { AuthSession } from "./session";

export function useAuthActions(auth: AuthPort, onSession: (session: AuthSession) => void) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function run(action: () => Promise<AuthResult>): Promise<void> {
    setBusy(true);
    setErrorMessage(null);
    const result = await action();
    setBusy(false);
    if (result.ok) {
      onSession(result.session);
      return;
    }
    setErrorMessage(result.message);
  }

  return {
    email,
    password,
    errorMessage,
    busy,
    onEmailChange: setEmail,
    onPasswordChange: setPassword,
    onGoogle: () => run(() => auth.signInWithGoogle()),
    onEmailSignIn: () => run(() => auth.signInWithEmail(email, password)),
    onGuest: () => run(() => auth.continueAsGuest()),
  };
}
