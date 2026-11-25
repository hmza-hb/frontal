import { act, renderHook } from "@testing-library/react-native";
import { createMemoryAuth } from "./memoryAuth";
import { useAuthActions } from "./useAuthActions";

test("guest succeeds and reports the session", async () => {
  const memory = createMemoryAuth({ cloudSignIn: false });
  const onSession = jest.fn();
  const { result } = renderHook(() => useAuthActions(memory.auth, onSession));
  await act(async () => {
    await result.current.onGuest();
  });
  expect(onSession).toHaveBeenCalledWith({ kind: "guest", userId: null, email: null });
  expect(memory.networkCallCount()).toBe(0);
});

test("email sign-in reports the session", async () => {
  const memory = createMemoryAuth({ cloudSignIn: true });
  const onSession = jest.fn();
  const { result } = renderHook(() => useAuthActions(memory.auth, onSession));
  await act(async () => {
    result.current.onEmailChange("a@b.c");
    result.current.onPasswordChange("secret");
  });
  await act(async () => {
    await result.current.onEmailSignIn();
  });
  expect(onSession).toHaveBeenCalledWith({ kind: "signedIn", userId: "email-user", email: "a@b.c" });
});

test("cloud error stays on the form", async () => {
  const memory = createMemoryAuth({ cloudSignIn: false });
  const onSession = jest.fn();
  const { result } = renderHook(() => useAuthActions(memory.auth, onSession));
  await act(async () => {
    await result.current.onGoogle();
  });
  expect(onSession).not.toHaveBeenCalled();
  expect(result.current.errorMessage).toBe("Cloud sign-in is not configured");
});
