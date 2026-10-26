import type { Role } from "@/components/shell/nav";

export interface SessionUser {
  displayName: string;
  username: string;
  role: Role;
}

/**
 * Stand-in for the signed-in user until task 6.1 wires the real session
 * (GraphQL `me`). Never used for authorization: the server decides (D-053).
 */
export const PLACEHOLDER_USER: SessionUser = {
  displayName: "Ada Lovelace",
  username: "ada",
  role: "admin",
};
