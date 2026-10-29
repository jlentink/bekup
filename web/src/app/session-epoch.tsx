import { createContext, useContext } from "react";

/**
 * Counter bumped by RootLayout whenever a session check confirms the
 * session after a live stream ended: the shell keys its stream on it, so
 * the stream reopens with the (possibly new) session cookie.
 */
export const SessionEpochContext = createContext(0);

export function useSessionEpoch(): number {
  return useContext(SessionEpochContext);
}
