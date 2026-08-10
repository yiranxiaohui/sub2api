#!/usr/bin/env bash
set -Eeuo pipefail

deploy_path="${1:?deployment path is required}"
compose_file="${2:?Compose file is required}"
release_image="${3:?release image is required}"
rollback_image="sub2api-ci-rollback:previous"

cd "$deploy_path"
test -f "$compose_file"
test -f .env
docker compose version

old_image_id="$(docker inspect --format '{{.Image}}' sub2api 2>/dev/null || true)"
if [ -n "$old_image_id" ]; then
  docker image tag "$old_image_id" "$rollback_image"
fi

rollback_deployment() {
  echo "Production deployment failed; showing application logs" >&2
  docker compose -f "$compose_file" logs --tail=200 sub2api >&2 || true

  if [ -n "$old_image_id" ]; then
    echo "Rolling back to the previous application image" >&2
    export SUB2API_IMAGE="$rollback_image"
    docker compose -f "$compose_file" up -d --no-deps --force-recreate sub2api || true
  fi
  exit 1
}

export SUB2API_IMAGE="$release_image"
docker compose -f "$compose_file" pull sub2api
if ! docker compose -f "$compose_file" up -d --no-deps --force-recreate sub2api; then
  rollback_deployment
fi

deployment_ok=false
for _ in $(seq 1 36); do
  status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' sub2api 2>/dev/null || true)"
  case "$status" in
    healthy|running)
      deployment_ok=true
      break
      ;;
    unhealthy|exited|dead)
      break
      ;;
  esac
  sleep 5
done

if [ "$deployment_ok" = true ]; then
  docker compose -f "$compose_file" ps sub2api
  exit 0
fi

rollback_deployment
