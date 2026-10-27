import { graphql } from "@/graphql/gen";

/** Public server facts (Query.serverInfo); safe before login. */
export const ServerInfoQuery = graphql(`
  query ServerInfo {
    serverInfo {
      version
      insecureTransport
      devMode
    }
  }
`);

/**
 * True when the page itself was not loaded over HTTPS. Checked in the browser
 * as well, so the D-095 warning shows even when the server cannot be reached.
 */
export function pageIsInsecure(loc: Pick<Location, "protocol"> = window.location): boolean {
  return loc.protocol !== "https:";
}
