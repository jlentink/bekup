import { useQuery } from "@apollo/client/react";

import type { DestinationStoredQuery as StoredResult } from "@/graphql/gen/graphql";

import { DestinationStoredQuery } from "./operations";

export type StoredRow = StoredResult["destinationUsage"][number];

/** The stored usage per destination id (destinationUsage, one aggregate). */
export function useStoredByDestination(): {
  byId: Map<string, StoredRow>;
  loaded: boolean;
  failed: boolean;
} {
  const { data, error } = useQuery(DestinationStoredQuery, { fetchPolicy: "cache-and-network" });
  const byId = new Map((data?.destinationUsage ?? []).map((u) => [u.destinationId, u]));
  return { byId, loaded: data !== undefined, failed: !data && error !== undefined };
}
