// <oversight-app> custom element: the micro frontend entry point.
//
//   <script type="module" src="https://<host>/oversight-element.js"></script>
//   <oversight-app api-base="https://oversight-api.example" theme="auto" agent="cve-triage"></oversight-app>
//
// Attributes: api-base (default: VITE_OVERSIGHT_API or this page's origin), theme (light | dark | auto),
// agent (initial agent filter), routing ("hash" to mirror the current view in location.hash). Emits "oversight:navigate" (detail = route) as the user moves around.
import { createRoot, type Root } from "react-dom/client";
import { App } from "./App";
import css from "./styles.css?inline";

const DEFAULT_API = (import.meta.env.VITE_OVERSIGHT_API as string | undefined) || window.location.origin;

// Fonts must be declared in the document (not the shadow root) to apply inside it.
function loadFonts() {
  if (document.querySelector("link[data-oversight-fonts]")) return;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.dataset.oversightFonts = "";
  link.href = "https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap";
  document.head.appendChild(link);
}

class OversightElement extends HTMLElement {
  static observedAttributes = ["api-base", "agent", "routing"];
  private root?: Root;
  private mount?: HTMLDivElement;

  connectedCallback() {
    loadFonts();
    if (!this.shadowRoot) {
      const shadow = this.attachShadow({ mode: "open" });
      const style = document.createElement("style");
      style.textContent = css;
      this.mount = document.createElement("div");
      shadow.append(style, this.mount);
      this.root = createRoot(this.mount);
    }
    this.render();
  }

  attributeChangedCallback() {
    if (this.root) this.render();
  }

  disconnectedCallback() {
    this.root?.unmount();
    this.root = undefined;
    this.shadowRoot?.replaceChildren();
  }

  private render() {
    const apiBase = this.getAttribute("api-base") || DEFAULT_API;
    // key forces a fresh app (and SSE connection) when the API changes.
    this.root!.render(<App key={apiBase} apiBase={apiBase} initialAgent={this.getAttribute("agent") || undefined} host={this} hashRouting={this.getAttribute("routing") === "hash"} />);
  }
}

if (!customElements.get("oversight-app")) customElements.define("oversight-app", OversightElement);
