import { StatusBar } from "expo-status-bar";
import { StyleSheet, Text, View } from "react-native";
import { placeholderTitle } from "./src/placeholder";

export default function App() {
  return (
    <View style={styles.container}>
      <Text>{placeholderTitle()}</Text>
      <StatusBar style="auto" />
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
  },
});