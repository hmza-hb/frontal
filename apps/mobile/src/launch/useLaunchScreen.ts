import { useEffect, useState } from "react";
import { AuthSession } from "../auth/session";
import { hideNativeSplash } from "./nativeSplash";
import { launchScreenAfterHold, splashHoldMs, type LaunchScreen } from "./launchScreen";

export type RestoreSession = () => Promise<AuthSession | null>;

async function restoreNone(): Promise<AuthSession | null> {
  return null;
}

export function useLaunchScreen(restoreSession: RestoreSession = restoreNone): LaunchScreen {
  const [elapsedMs, setElapsedMs] = useState(0);
  const [restoredSession, setRestoredSession] = useState<AuthSession | null | undefined>(undefined);

  useEffect(() => {
    void hideNativeSplash();
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => {
      setElapsedMs(splashHoldMs);
    }, splashHoldMs);
    return () => {
      clearTimeout(timer);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    void restoreSession().then((session) => {
      if (!cancelled) {
        setRestoredSession(session);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [restoreSession]);

  return launchScreenAfterHold(elapsedMs, splashHoldMs, restoredSession);
}
