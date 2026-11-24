import { AuthDestination } from "./src/screens/AuthDestination";
import { SplashScreen } from "./src/screens/SplashScreen";
import { preventNativeSplashAutoHide } from "./src/launch/nativeSplash";
import { useLaunchScreen } from "./src/launch/useLaunchScreen";

preventNativeSplashAutoHide();

export default function App() {
  const screen = useLaunchScreen();
  if (screen === "splash") {
    return <SplashScreen />;
  }
  return <AuthDestination />;
}
