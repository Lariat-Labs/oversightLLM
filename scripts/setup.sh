#!/usr/bin/env bash
# Oversight bootstrap: Azure device-code login, env discovery, validation.
#
#   scripts/setup.sh            # log in if needed, fill .env, verify access
#   scripts/setup.sh --login    # force a fresh device-code login
#   scripts/setup.sh --check    # only verify the current .env (no prompts)
#
# Works with macOS bash 3.2.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="$ROOT/.env"
FORCE_LOGIN=0
CHECK_ONLY=0
for a in "$@"; do
  case "$a" in
    --login) FORCE_LOGIN=1 ;;
    --check) CHECK_ONLY=1 ;;
    -h|--help) sed -n '2,8p' "$0"; exit 0 ;;
  esac
done

bold() { printf '\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✔\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '  \033[31m✘\033[0m %s\n' "$*" >&2; exit 1; }

# Pick one line from a list. Usage: choose "prompt" "line1\nline2..." -> echoes the chosen line.
choose() {
  local prompt="$1" list="$2" n i=1 line pick
  n=$(printf '%s\n' "$list" | grep -c . || true)
  [ "$n" -eq 0 ] && return 1
  if [ "$n" -eq 1 ]; then printf '%s\n' "$list"; return 0; fi
  printf '%s\n' "$prompt" >&2
  while IFS= read -r line; do printf '   %d) %s\n' "$i" "$line" >&2; i=$((i+1)); done <<< "$list"
  read -r -p "   Choose [1-$n, default 1]: " pick </dev/tty
  pick=${pick:-1}
  printf '%s\n' "$list" | sed -n "${pick}p"
}

ask() { # ask "prompt" "default" -> echoes answer
  local ans; read -r -p "  $1 [${2}]: " ans </dev/tty; printf '%s\n' "${ans:-$2}"
}

# ---------- 1. tools ----------
bold "1. Checking tools"
command -v az >/dev/null || die "Azure CLI not found: https://learn.microsoft.com/cli/azure/install-azure-cli"
ok "az $(az version --query '"azure-cli"' -o tsv 2>/dev/null)"
command -v go >/dev/null && ok "$(go version | awk '{print $3}')" || warn "Go not found (needed to run the API): https://go.dev/dl"
command -v node >/dev/null && ok "node $(node --version)" || warn "Node.js not found (needed for the frontend)"

# ---------- 2. env ----------
if [ -f "$ENV_FILE" ]; then
  set -a; . "$ENV_FILE"; set +a
  ok "loaded $ENV_FILE"
fi

# ---------- 3. login ----------
bold "2. Azure sign-in (device code)"
if [ "$FORCE_LOGIN" = 1 ] || ! az account show >/dev/null 2>&1; then
  [ "$CHECK_ONLY" = 1 ] && die "not signed in; run: make login"
  TENANT_ARG=""
  [ -n "${AZURE_TENANT_ID:-}" ] && TENANT_ARG="--tenant $AZURE_TENANT_ID"
  echo "  Open the URL below and enter the code to sign in."
  # shellcheck disable=SC2086
  az login --use-device-code $TENANT_ARG --output none
fi
if [ -n "${AZURE_SUBSCRIPTION_ID:-}" ]; then
  az account set --subscription "$AZURE_SUBSCRIPTION_ID"
fi
ok "signed in as $(az account show --query user.name -o tsv) — subscription: $(az account show --query name -o tsv)"

if [ "$CHECK_ONLY" = 0 ]; then
  # ---------- 4. discovery ----------
  bold "3. Required settings"

  if [ -z "${FOUNDRY_PROJECT_ENDPOINT:-}" ]; then
    projects=$(az resource list --resource-type Microsoft.CognitiveServices/accounts/projects --query "[].name" -o tsv 2>/dev/null || true)
    if [ -n "$projects" ]; then
      p=$(choose "  Foundry projects found:" "$projects")
      acct=${p%%/*}; proj=${p#*/}
      FOUNDRY_PROJECT_ENDPOINT="https://$acct.services.ai.azure.com/api/projects/$proj"
    else
      FOUNDRY_PROJECT_ENDPOINT=$(ask "FOUNDRY_PROJECT_ENDPOINT (https://<resource>.services.ai.azure.com/api/projects/<project>)" "")
    fi
  fi
  [ -n "$FOUNDRY_PROJECT_ENDPOINT" ] || die "FOUNDRY_PROJECT_ENDPOINT is required"
  ok "FOUNDRY_PROJECT_ENDPOINT=$FOUNDRY_PROJECT_ENDPOINT"
  ACCOUNT=$(printf '%s' "$FOUNDRY_PROJECT_ENDPOINT" | sed -E 's#https://([^.]+)\..*#\1#')
  ACCOUNT_ID=$(az resource list --resource-type Microsoft.CognitiveServices/accounts --query "[?name=='$ACCOUNT'].id | [0]" -o tsv 2>/dev/null || true)
  ACCOUNT_RG=$(printf '%s' "$ACCOUNT_ID" | sed -E 's#.*/resourceGroups/([^/]+)/.*#\1#')

  if [ -z "${LOG_ANALYTICS_WORKSPACE_ID:-}" ]; then
    # Prefer the App Insights resource connected to the Foundry project (Tracing tab).
    appi_id=$(az rest --method get --resource https://ai.azure.com \
      --url "$FOUNDRY_PROJECT_ENDPOINT/connections?api-version=v1" \
      --query "value[?type=='AppInsights'].target | [0]" -o tsv 2>/dev/null || true)
    if [ -z "$appi_id" ]; then
      list=$(az monitor app-insights component list --query "[].id" -o tsv 2>/dev/null || true)
      [ -n "$list" ] && appi_id=$(choose "  Application Insights resources:" "$list")
    else
      ok "found the project's Application Insights connection"
    fi
    if [ -n "$appi_id" ]; then
      ws_id=$(az monitor app-insights component show --ids "$appi_id" --query workspaceResourceId -o tsv)
      LOG_ANALYTICS_WORKSPACE_ID=$(az monitor log-analytics workspace show --ids "$ws_id" --query customerId -o tsv)
    else
      LOG_ANALYTICS_WORKSPACE_ID=$(ask "LOG_ANALYTICS_WORKSPACE_ID (workspace customerId GUID)" "")
    fi
  fi
  [ -n "$LOG_ANALYTICS_WORKSPACE_ID" ] || die "LOG_ANALYTICS_WORKSPACE_ID is required"
  ok "LOG_ANALYTICS_WORKSPACE_ID=$LOG_ANALYTICS_WORKSPACE_ID"

  if [ -z "${JUDGE_MODEL_DEPLOYMENT:-}" ]; then
    deps=""
    [ -n "$ACCOUNT_RG" ] && deps=$(az cognitiveservices account deployment list -n "$ACCOUNT" -g "$ACCOUNT_RG" --query "[].name" -o tsv 2>/dev/null || true)
    if [ -n "$deps" ]; then
      JUDGE_MODEL_DEPLOYMENT=$(choose "  Model deployments for the judge:" "$deps")
    else
      JUDGE_MODEL_DEPLOYMENT=$(ask "JUDGE_MODEL_DEPLOYMENT (model deployment name)" "gpt-5-mini")
    fi
  fi
  ok "JUDGE_MODEL_DEPLOYMENT=$JUDGE_MODEL_DEPLOYMENT"

  # Optional: lets the judge send its own calls to App Insights as gen_ai.agent.id = oversight-judge.
  if [ -z "${APPLICATIONINSIGHTS_CONNECTION_STRING:-}" ]; then
    [ -z "${appi_id:-}" ] && appi_id=$(az rest --method get --resource https://ai.azure.com \
      --url "$FOUNDRY_PROJECT_ENDPOINT/connections?api-version=v1" \
      --query "value[?type=='AppInsights'].target | [0]" -o tsv 2>/dev/null || true)
    [ -n "${appi_id:-}" ] && APPLICATIONINSIGHTS_CONNECTION_STRING=$(az monitor app-insights component show --ids "$appi_id" --query connectionString -o tsv 2>/dev/null || true)
  fi
  if [ -n "${APPLICATIONINSIGHTS_CONNECTION_STRING:-}" ]; then ok "APPLICATIONINSIGHTS_CONNECTION_STRING found (judge tracing on)"
  else warn "no App Insights connection string; the judge won't be traced"; fi

  # ---------- 5. write .env ----------
  keep_optional=""
  if [ -f "$ENV_FILE" ]; then
    keep_optional=$(grep -vE '^(export )?(FOUNDRY_PROJECT_ENDPOINT|LOG_ANALYTICS_WORKSPACE_ID|JUDGE_MODEL_DEPLOYMENT|APPLICATIONINSIGHTS_CONNECTION_STRING)=' "$ENV_FILE" | grep -vE '^# --- (required|optional|tracing)' || true)
  else
    keep_optional=$(sed -n '/^# --- optional/,$p' "$ROOT/.env.example" | grep -v '^# --- optional')
  fi
  {
    echo "# --- required (written by scripts/setup.sh)"
    echo "FOUNDRY_PROJECT_ENDPOINT=$FOUNDRY_PROJECT_ENDPOINT"
    echo "LOG_ANALYTICS_WORKSPACE_ID=$LOG_ANALYTICS_WORKSPACE_ID"
    echo "JUDGE_MODEL_DEPLOYMENT=$JUDGE_MODEL_DEPLOYMENT"
    echo
    echo "# --- tracing (the judge's own calls -> App Insights)"
    [ -n "${APPLICATIONINSIGHTS_CONNECTION_STRING:-}" ] && echo "APPLICATIONINSIGHTS_CONNECTION_STRING=\"$APPLICATIONINSIGHTS_CONNECTION_STRING\""
    echo
    echo "# --- optional"
    printf '%s\n' "$keep_optional" | sed '/./,$!d'
  } > "$ENV_FILE.tmp"
  mv "$ENV_FILE.tmp" "$ENV_FILE"
  chmod 600 "$ENV_FILE"
  ok "wrote $ENV_FILE"
fi

# ---------- 6. verify ----------
bold "4. Verifying access"
for v in FOUNDRY_PROJECT_ENDPOINT LOG_ANALYTICS_WORKSPACE_ID JUDGE_MODEL_DEPLOYMENT; do
  [ -n "${!v:-}" ] || die "$v is not set (run: make setup)"
done
n=$(az rest --method get --resource https://ai.azure.com --url "$FOUNDRY_PROJECT_ENDPOINT/agents?api-version=v1&limit=100" --query "length(data)" -o tsv 2>/dev/null) \
  || die "cannot list Foundry agents — need the 'Azure AI User' role on the project"
ok "Foundry project reachable: $n agent(s)"
spans=$(az monitor log-analytics query -w "$LOG_ANALYTICS_WORKSPACE_ID" --timespan P7D \
  --analytics-query 'AppDependencies | where isnotempty(tostring(Properties["gen_ai.operation.name"])) | count' \
  --query "[0].Count" -o tsv 2>/dev/null) || die "cannot query Log Analytics — need 'Log Analytics Reader' on the workspace"
ok "Log Analytics reachable: $spans GenAI spans in the last 7 days"
[ "$spans" = "0" ] && warn "no GenAI spans yet; dashboards will be empty until agents emit OpenTelemetry to this workspace"
OPENAI="${AZURE_OPENAI_ENDPOINT:-$(printf '%s' "$FOUNDRY_PROJECT_ENDPOINT" | sed -E 's#https://([^.]+)\..*#https://\1.openai.azure.com/openai/v1#')}"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$OPENAI/chat/completions" \
  -H "Authorization: Bearer $(az account get-access-token --resource https://cognitiveservices.azure.com --query accessToken -o tsv)" \
  -H 'Content-Type: application/json' \
  -d "{\"model\":\"$JUDGE_MODEL_DEPLOYMENT\",\"messages\":[{\"role\":\"user\",\"content\":\"ping\"}],\"max_completion_tokens\":16}")
case "$code" in
  200) ok "judge model '$JUDGE_MODEL_DEPLOYMENT' responds" ;;
  *) warn "judge model returned HTTP $code — need 'Cognitive Services OpenAI User' and a valid deployment name" ;;
esac

bold "Ready. Start everything with: make dev"
