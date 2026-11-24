import { useEffect, useState } from "react";
import { hideNativeSplash } from "./nativeSplash";
import { launchScreenAfterHold, splashHoldMs, type LaunchScreen } from "./launchScreen";

export function useLaunchScreen(): LaunchScreen {
  const [elapsedMs, setElapsedMs] = useState(0);

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

  return launchScreenAfterHold(elapsedMs, splashHoldMs);
}
