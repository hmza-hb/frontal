import { AuthSession } from "../auth/session";

export type LaunchScreen = "splash" | "auth" | "settings";

export const splashHoldMs = 800;

export function launchScreenAfterHold(
  elapsedMs: number,
  holdMs: number,
  restoredSession: AuthSession | null | undefined,
): LaunchScreen {
  if (elapsedMs < holdMs || restoredSession === undefined) {
    return "splash";
  }
  if (restoredSession === null) {
    return "auth";
  }
  return "settings";
}
