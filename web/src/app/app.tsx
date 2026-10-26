import { ApolloProvider } from "@apollo/client/react";
import { useState } from "react";
import { RouterProvider } from "react-router/dom";

import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { createApolloClient } from "@/lib/apollo";
import { ThemeProvider } from "@/lib/theme";

import { createRouter } from "./router";

export function App() {
  const [client] = useState(createApolloClient);
  const [router] = useState(createRouter);
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
