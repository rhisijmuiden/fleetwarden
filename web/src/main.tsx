import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router";
import logo from "../../branding/logo.svg";
import "./i18n";
import "./styles.css";
import { App } from "./App";

document.querySelector<HTMLLinkElement>("#favicon")?.setAttribute("href", logo);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
