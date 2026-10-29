import { createContext, useContext } from "react";

import type { SessionUser } from "./session-model";

export const SessionContext = createContext<SessionUser | null>(null);

/** The signed-in user. Only inside RequireSession. Never use it for authorization: the server decides. */
export function useSessionUser(): SessionUser {
  const user = useContext(SessionContext);
  if (!user) throw new Error("useSessionUser must be used inside <RequireSession>");
  return user;
}
