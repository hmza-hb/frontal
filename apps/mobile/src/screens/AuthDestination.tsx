import { StatusBar } from "expo-status-bar";
import { Pressable, Text, TextInput, View } from "react-native";
import {
  authDestinationTitle,
  continueAsGuestLabel,
  continueWithEmailLabel,
  continueWithGoogleLabel,
  emailFieldLabel,
  passwordFieldLabel,
} from "../branding";
import { authStyles } from "../theme/authStyles";

export type AuthDestinationProps = {
  email?: string;
  password?: string;
  errorMessage?: string | null;
  busy?: boolean;
  onEmailChange?: (value: string) => void;
  onPasswordChange?: (value: string) => void;
  onGoogle?: () => void;
  onEmailSignIn?: () => void;
  onGuest?: () => void;
};

export function AuthDestination(props: AuthDestinationProps = {}) {
  return (
    <View accessibilityLabel={authDestinationTitle()} style={authStyles.container} testID="auth-destination">
      <Text style={authStyles.title}>{authDestinationTitle()}</Text>
      <Pressable disabled={props.busy} onPress={props.onGoogle} testID="auth-google">
        <Text style={authStyles.action}>{continueWithGoogleLabel()}</Text>
      </Pressable>
      <TextInput
        autoCapitalize="none"
        autoCorrect={false}
        editable={!props.busy}
        keyboardType="email-address"
        onChangeText={props.onEmailChange}
        placeholder={emailFieldLabel()}
        placeholderTextColor="#888888"
        style={authStyles.field}
        testID="auth-email"
        value={props.email}
      />
      <TextInput
        editable={!props.busy}
        onChangeText={props.onPasswordChange}
        placeholder={passwordFieldLabel()}
        placeholderTextColor="#888888"
        secureTextEntry
        style={authStyles.field}
        testID="auth-password"
        value={props.password}
      />
      <Pressable disabled={props.busy} onPress={props.onEmailSignIn} testID="auth-email-submit">
        <Text style={authStyles.action}>{continueWithEmailLabel()}</Text>
      </Pressable>
      <Pressable disabled={props.busy} onPress={props.onGuest} testID="auth-guest">
        <Text style={authStyles.action}>{continueAsGuestLabel()}</Text>
      </Pressable>
      {props.errorMessage != null ? <Text testID="auth-error">{props.errorMessage}</Text> : null}
      <StatusBar style="light" />
    </View>
  );
}
