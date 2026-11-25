import { StyleSheet } from "react-native";
import { launchBackground, launchForeground } from "./launchColors";

export const authStyles = StyleSheet.create({
  container: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: launchBackground,
    paddingHorizontal: 24,
  },
  title: {
    color: launchForeground,
    fontSize: 28,
    fontWeight: "600",
    marginBottom: 24,
  },
  action: {
    color: launchForeground,
    fontSize: 16,
    marginVertical: 8,
  },
  field: {
    color: launchForeground,
    borderColor: launchForeground,
    borderWidth: 1,
    borderRadius: 8,
    width: 280,
    paddingHorizontal: 12,
    paddingVertical: 8,
    marginVertical: 6,
  },
});
