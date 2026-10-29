import { ApolloProvider } from "@apollo/client/react";
import type { ApolloClient } from "@apollo/client";
import { useState } from "react";
import type { createBrowserRouter } from "react-router";
import { RouterProvider } from "react-router/dom";

import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { createApolloClient } from "@/lib/apollo";
import { ThemeProvider } from "@/lib/theme";

import { createRouter } from "./router";

type Router = ReturnType<typeof createBrowserRouter>;

/** Providers around a router; tests pass a memory router and a mock client. */
export function AppProviders({ client, router }: { client: ApolloClient; router: Router }) {
  return (
    <ThemeProvider>
      <ApolloProvider client={client}>
        <TooltipProvider delayDuration={300}>
          <RouterProvider router={router} />
          <Toaster />
        </TooltipProvider>
      </ApolloProvider>
    </ThemeProvider>
  );
}

export function App() {
  const [client] = useState(() => createApolloClient());
  const [router] = useState(createRouter);
  return <AppProviders client={client} router={router} />;
}
