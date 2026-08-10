#!/usr/bin/env bash
set -Eeuo pipefail

deploy_path="${1:?deployment path is required}"
compose_file="${2:?Compose file is required}"
release_image="${3:?release image is required}"

cd "$deploy_path"
test -f "$compose_file"
docker compose version

old_image_id="$(docker inspect --format '{{.Image}}' sub2api 2>/dev/null || true)"
configured_image="$(docker inspect --format '{{.Config.Image}}' sub2api 2>/dev/null || true)"

if [ -z "$old_image_id" ] || [ -z "$configured_image" ]; then
  echo "Existing sub2api container was not found" >&2
  exit 1
fi
if [[ "$configured_image" == *@* ]]; then
  echo "Existing Compose image is pinned by digest and cannot be retagged: $configured_image" >&2
  exit 1
fi

rollback_deployment() {
  echo "Production deployment failed; showing application logs" >&2
  docker compose -f "$compose_file" logs --tail=200 sub2api >&2 || true

  echo "Rolling back to the previous application image" >&2
  docker image tag "$old_image_id" "$configured_image" || true
  docker compose -f "$compose_file" up -d --no-deps --force-recreate sub2api || true
  exit 1
}

docker pull "$release_image"
docker image tag "$release_image" "$configured_image"
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
