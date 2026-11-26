import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import Apis from "./pages/Apis";
import Environments from "./pages/Environments";
import CollectionSettings from "./pages/CollectionSettings";
import History from "./pages/History";
import Monitors from "./pages/Monitors";
import Runner from "./pages/Runner";
import Mocks from "./pages/Mocks";
import Requests from "./pages/requests/Requests";
import Settings from "./pages/Settings";
import Admin from "./pages/admin/Admin";
import SignIn from "./pages/SignIn";
import SignInEmail from "./pages/SignInEmail";
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
            <Route path="/signin/email" element={<SignInEmail />} />
            <Route element={<AppLayout />}>
              <Route index element={<Navigate to="/apis" replace />} />
              <Route path="/apis" element={<Apis />} />
              <Route path="/requests/:id?" element={<Requests />} />
              <Route element={<WorkspaceLayout />}>
                <Route path="/history" element={<History />} />
                <Route path="/mocks" element={<Mocks />} />
                <Route path="/environments/:envId?" element={<Environments />} />
                <Route path="/collections/:id" element={<CollectionSettings />} />
                <Route path="/runner/:id" element={<Runner />} />
                <Route path="/monitors" element={<Monitors />} />
              </Route>
              <Route path="/settings/:section?" element={<Settings />} />
              <Route path="/admin/:section?" element={<Admin />} />
              <Route path="*" element={<Navigate to="/apis" replace />} />
            </Route>
          </Routes>
        </PrefsProvider>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
