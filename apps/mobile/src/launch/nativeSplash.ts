import * as SplashScreen from "expo-splash-screen";

export function preventNativeSplashAutoHide(): void {
  void SplashScreen.preventAutoHideAsync().then(
    () => undefined,
    () => undefined,
  );
}

export function hideNativeSplash(): Promise<void> {
  return SplashScreen.hideAsync().then(
    () => undefined,
    () => undefined,
  );
}
