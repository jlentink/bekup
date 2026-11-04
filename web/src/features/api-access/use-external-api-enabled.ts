import { useQuery } from "@apollo/client/react";

import { ServerInfoQuery } from "@/lib/server-info";

/**
 * Whether this deployment runs the opt-in external API (D-220;
 * serverInfo.externalApiEnabled from BEKUP_EXTERNAL_API). undefined while
 * unknown. A UI hint only: the server decides nothing from it.
 */
export function useExternalApiEnabled(): boolean | undefined {
  const { data } = useQuery(ServerInfoQuery, { fetchPolicy: "cache-first" });
  return data?.serverInfo.externalApiEnabled;
}
