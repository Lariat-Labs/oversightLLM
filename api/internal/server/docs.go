package server

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.json
var openapiJSON []byte

func (s *Server) openapiSpec(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(openapiJSON)
}

// apiDocs serves Swagger UI (pinned version from a CDN) pointed at the spec.
func (s *Server) apiDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(docsHTML))
}

const docsHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Oversight API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui.css" />
  <style>
    body { margin: 0; background: #fafafa; }
    .topbar-o { background: #1a1c20; color: #e8eaed; padding: 12px 20px; font: 600 15px/1.4 system-ui, sans-serif; display: flex; gap: 14px; align-items: center; }
    .topbar-o a { color: #9aa0a8; font-weight: 400; font-size: 13px; text-decoration: none; }
    .topbar-o a:hover { color: #fff; }
    .swagger-ui .topbar { display: none; }
  </style>
</head>
<body>
  <div class="topbar-o">Oversight API <a href="/api/openapi.json">openapi.json</a></div>
  <div id="ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({ url: "/api/openapi.json", dom_id: "#ui", deepLinking: true, tryItOutEnabled: true, docExpansion: "list", defaultModelsExpandDepth: 0 });
  </script>
</body>
</html>`
