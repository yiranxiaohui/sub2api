#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TEMP_DIR"' EXIT

mkdir -p "$TEMP_DIR/bin" "$TEMP_DIR/deployment"
touch "$TEMP_DIR/deployment/.env" "$TEMP_DIR/deployment/docker-compose.yml"

cat > "$TEMP_DIR/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
printf '%s|%s\n' "${SUB2API_IMAGE:-}" "$*" >> "$FAKE_DOCKER_LOG"

if [ "$1" = "inspect" ]; then
  case "$3" in
    '{{.Image}}')
      echo "sha256:previous"
      ;;
    '{{.Config.Image}}')
      echo "weishaw/sub2api:latest"
      ;;
    *)
      echo "${FAKE_HEALTH_STATUS:-healthy}"
      ;;
  esac
fi
FAKE_DOCKER
chmod +x "$TEMP_DIR/bin/docker"

export PATH="$TEMP_DIR/bin:$PATH"
export FAKE_DOCKER_LOG="$TEMP_DIR/docker.log"

FAKE_HEALTH_STATUS=healthy \
  bash "$ROOT_DIR/deploy/ci-production-deploy.sh" \
  "$TEMP_DIR/deployment" docker-compose.yml ghcr.io/example/sub2api:1.2.3

grep -Fq '|pull ghcr.io/example/sub2api:1.2.3' "$FAKE_DOCKER_LOG"
grep -Fq '|image tag ghcr.io/example/sub2api:1.2.3 weishaw/sub2api:latest' "$FAKE_DOCKER_LOG"
grep -Fq '|compose -f docker-compose.yml up -d --no-deps --force-recreate sub2api' "$FAKE_DOCKER_LOG"

: > "$FAKE_DOCKER_LOG"
if FAKE_HEALTH_STATUS=unhealthy \
  bash "$ROOT_DIR/deploy/ci-production-deploy.sh" \
  "$TEMP_DIR/deployment" docker-compose.yml ghcr.io/example/sub2api:2.0.0; then
  echo "unhealthy deployment unexpectedly succeeded" >&2
  exit 1
fi

grep -Fq '|image tag sha256:previous weishaw/sub2api:latest' "$FAKE_DOCKER_LOG"
grep -Fq '|compose -f docker-compose.yml up -d --no-deps --force-recreate sub2api' "$FAKE_DOCKER_LOG"

printf 'CI production deploy test passed\n'
