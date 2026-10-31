import { skipToken, useQuery } from "@apollo/client/react";

import { DestinationUnrecordedFilesQuery } from "./operations";

/** Files per page of the unrecorded-files list (the server allows 1..200). */
export const UNRECORDED_PAGE_SIZE = 50;

/**
 * The first page of a destination's unrecorded files (D-209). Admins only:
 * pass enabled = false for every other role so the query is never sent.
 * The page banner and the section share it (same variables, one request).
 */
export function useUnrecordedFiles(destinationId: string, enabled: boolean) {
  return useQuery(
    DestinationUnrecordedFilesQuery,
    enabled
      ? {
          variables: { destinationId, first: UNRECORDED_PAGE_SIZE },
          fetchPolicy: "cache-and-network",
        }
      : skipToken,
  );
}

/** Keeps the first row of every id (pages may overlap after a refetch). */
export function uniqueById<R extends { id: string }>(rows: readonly R[]): R[] {
  const seen = new Set<string>();
  return rows.filter((r) => {
    if (seen.has(r.id)) return false;
    seen.add(r.id);
    return true;
  });
}
