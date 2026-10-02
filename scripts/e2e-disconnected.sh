#!/usr/bin/env bash
# Connected provisioning followed by restricted Gemma product E2E (OLS-4226).
# The service checkout owns RHOAI/GPU/model preparation; no copied provisioning.
set -euo pipefail
# Never trace credentials inherited from CI.
set +x

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ ! "${LIGHTSPEED_SERVICE_REF:-}" =~ ^[[:xdigit:]]{40}$ ]]; then
    echo 'LIGHTSPEED_SERVICE_REF must be exactly 40 hexadecimal characters (a full commit SHA)' >&2
    exit 1
fi
export LIGHTSPEED_SERVICE_REF="${LIGHTSPEED_SERVICE_REF,,}"
: "${HUGGING_FACE_HUB_TOKEN:?Required for connected model preparation}"
: "${VLLM_API_KEY:?Required for the authenticated vLLM endpoint}"
: "${SANDBOX_IMAGE:?Provide a sandbox image mirrored to the OpenShift internal registry}"
if [[ "$SANDBOX_IMAGE" != image-registry.openshift-image-registry.svc:5000/* ]]; then
    echo 'SANDBOX_IMAGE must use the OpenShift cluster-local registry' >&2
    exit 1
fi
if [[ -n "${E2E_SKIP_SCENARIOS:-}" || "${E2E_SCENARIO_TAGS:-core}" != core ]]; then
    echo 'Disconnected product E2E requires core tags and no scenario exclusions' >&2
    exit 1
fi

WORKDIR="$(mktemp -d)"
export E2E_DISCONNECTED_ID="e2e-${WORKDIR##*/}"
child_pid=""
# Preserve the original result; never retry after restoring external access.
_cleanup() {
    local rc=$?
    trap - EXIT INT TERM
    if [[ -n "$child_pid" ]] && kill -0 "$child_pid" 2>/dev/null; then
        kill -TERM -- "-$child_pid" 2>/dev/null || true
        wait "$child_pid" || true
    fi
    local cleanup_rc=0
    bash "$SCRIPT_DIR/e2e-disconnected-cleanup.sh" || cleanup_rc=$?
    if [[ "$rc" -eq 0 ]]; then rc=$cleanup_rc; fi
    rm -rf "$WORKDIR" || true
    exit "$rc"
}
trap _cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

git clone https://github.com/openshift/lightspeed-service.git "$WORKDIR/service"
git -C "$WORKDIR/service" cat-file -e "${LIGHTSPEED_SERVICE_REF}^{commit}"
git -C "$WORKDIR/service" checkout --detach "$LIGHTSPEED_SERVICE_REF"
if [[ "$(git -C "$WORKDIR/service" rev-parse HEAD)" != "$LIGHTSPEED_SERVICE_REF" ]]; then
    echo 'lightspeed-service checkout does not match LIGHTSPEED_SERVICE_REF' >&2
    exit 1
fi
provision="$WORKDIR/service/tests/rhoai/scripts/provision-vllm.sh"
if [[ ! -f "$provision" ]]; then
    echo 'Pinned lightspeed-service revision lacks the OLS-4228 provision-vllm.sh contract' >&2
    exit 1
fi
bash "$provision" --profile gemma4 --output-env "$WORKDIR/vllm.env" 2>&1 | python3 "$SCRIPT_DIR/e2e-redact.py"
# The safely shell-quoted, non-secret handoff comes from the verified checkout.
set -a
# shellcheck disable=SC1091
source "$WORKDIR/vllm.env"
set +a
: "${RHOAI_VLLM_BASE_URL:?Provisioning did not return its internal /v1 endpoint}"
: "${RHOAI_VLLM_MODEL:?Provisioning did not return its confirmed model}"

umask 077
printf '%s' "$VLLM_API_KEY" > "$WORKDIR/vllm-key"
export OPENAI_PROVIDER_KEY_PATH="$WORKDIR/vllm-key"
export E2E_MODEL="$RHOAI_VLLM_MODEL"
export E2E_OPENAI_URL="$RHOAI_VLLM_BASE_URL"
export E2E_DISCONNECTED=true E2E_SCENARIO_TAGS=core SANDBOX_MODE=bare-pod
# The narrow boundary permits DNS/API/vLLM only, not optional OTEL/MCP traffic.
export E2E_OTEL_ENABLED=false
export E2E_SUITE_TIMEOUT="${E2E_SUITE_TIMEOUT:-12h}"
# The standard runner clones scenarios and deploys the operator while connected;
# the product test validates all images and installs policies before fixtures/runs.
setsid bash "$SCRIPT_DIR/e2e-cluster.sh" openai &
child_pid=$!
wait "$child_pid"
