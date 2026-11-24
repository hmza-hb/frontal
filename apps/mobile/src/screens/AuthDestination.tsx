import { StatusBar } from "expo-status-bar";
import { StyleSheet, Text, View } from "react-native";
import { authDestinationTitle } from "../branding";
import { launchBackground, launchForeground } from "../theme/launchColors";

export function AuthDestination() {
  return (
    <View accessibilityLabel={authDestinationTitle()} style={styles.container} testID="auth-destination">
      <Text style={styles.title}>{authDestinationTitle()}</Text>
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
    fontSize: 28,
    fontWeight: "600",
  },
});
