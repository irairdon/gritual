import { Navigate, Route, Routes } from "react-router-dom";
import Layout from "./components/Layout";
import AuthMagicPage from "./pages/AuthMagicPage";
import ChallengePage from "./pages/ChallengePage";
import CirclePage from "./pages/CirclePage";
import CoachPage from "./pages/CoachPage";
import HomePage from "./pages/HomePage";
import JoinPage from "./pages/JoinPage";
import LoginPage from "./pages/LoginPage";
import LogsPage from "./pages/LogsPage";
import MealsPage from "./pages/MealsPage";
import PrivacyPage from "./pages/PrivacyPage";
import RegisterPage from "./pages/RegisterPage";
import RitualsPage from "./pages/RitualsPage";
import SettingsPage from "./pages/SettingsPage";

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route path="/" element={<HomePage />} />
        <Route path="/circles/:id" element={<CirclePage />} />
        <Route path="/challenges/:id" element={<ChallengePage />} />
        <Route path="/rituals" element={<RitualsPage />} />
        <Route path="/logs" element={<LogsPage />} />
        <Route path="/meals" element={<MealsPage />} />
        <Route path="/coach" element={<CoachPage />} />
        <Route path="/join/:token" element={<JoinPage />} />
        <Route path="/login" element={<LoginPage />} />
        <Route path="/register" element={<RegisterPage />} />
        <Route path="/auth/magic" element={<AuthMagicPage />} />
        <Route path="/privacy" element={<PrivacyPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}
