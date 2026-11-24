import { StatusBar } from "expo-status-bar";
import { StyleSheet, Text, View } from "react-native";
import { productName } from "../branding";
import { launchBackground, launchForeground } from "../theme/launchColors";

export function SplashScreen() {
  return (
    <View accessibilityLabel={productName()} style={styles.container} testID="splash-screen">
      <Text style={styles.title}>{productName()}</Text>
      <StatusBar style="light" />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: launchBackground,
  },
  title: {
    color: launchForeground,
    fontSize: 32,
    fontWeight: "600",
  },
});
