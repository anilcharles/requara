import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import Apis from "./pages/Apis";
import Design from "./pages/Design";
import Docs from "./pages/Docs";
import Environments from "./pages/Environments";
import History from "./pages/History";
import Mocks from "./pages/Mocks";
import Requests from "./pages/requests/Requests";
import Settings from "./pages/Settings";
import SignIn from "./pages/SignIn";
import { AppLayout, WorkspaceLayout } from "./shell/Layouts";
import { PrefsProvider } from "./shell/session";
import "./styles.css";

const queryClient = new QueryClient({ defaultOptions: { queries: { refetchOnWindowFocus: false, retry: 1 } } });

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <PrefsProvider>
          <Routes>
            <Route path="/signin" element={<SignIn />} />
            <Route element={<AppLayout />}>
              <Route index element={<Navigate to="/apis" replace />} />
              <Route path="/apis" element={<Apis />} />
              <Route path="/requests/:id?" element={<Requests />} />
              <Route path="/design/:apiId?" element={<Design />} />
              <Route path="/docs/:apiId?" element={<Docs />} />
              <Route element={<WorkspaceLayout />}>
                <Route path="/history" element={<History />} />
                <Route path="/mocks" element={<Mocks />} />
                <Route path="/environments/:envId?" element={<Environments />} />
              </Route>
              <Route path="/settings/:section?" element={<Settings />} />
              <Route path="*" element={<Navigate to="/apis" replace />} />
            </Route>
          </Routes>
        </PrefsProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
