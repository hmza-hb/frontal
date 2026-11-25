import { StatusBar } from "expo-status-bar";
import { StyleSheet, Text, View } from "react-native";
import { settingsDestinationTitle } from "../branding";
import { launchBackground, launchForeground } from "../theme/launchColors";

export function SettingsDestination() {
  return (
    <View accessibilityLabel={settingsDestinationTitle()} style={styles.container} testID="settings-destination">
      <Text style={styles.title}>{settingsDestinationTitle()}</Text>
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
