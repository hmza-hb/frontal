export type LaunchScreen = "splash" | "auth";

export const splashHoldMs = 800;

export function launchScreenAfterHold(elapsedMs: number, holdMs: number): LaunchScreen {
  if (elapsedMs < holdMs) {
    return "splash";
  }
  return "auth";
}
