#!/usr/bin/env bash
set -euo pipefail

COMPOSE=${COMPOSE:-"docker compose -f deploy/docker-compose.yml"}
ADMIN_URL=${ADMIN_URL:-http://localhost:8081}

temp="$($COMPOSE run --rm api staff create -username ci-smoke | sed -n 's/^临时密码.*：//p')"
test -n "$temp"
jar="$(mktemp)"
trap 'rm -f "$jar"' EXIT
admin() {
	curl -sf -b "$jar" -c "$jar" -H 'Content-Type: application/json' -H "Origin: $ADMIN_URL" "$@"
}
admin -X POST "$ADMIN_URL/admin/v1/auth/login" -d "{\"username\":\"ci-smoke\",\"password\":\"$temp\"}" >/dev/null
admin -X PUT "$ADMIN_URL/admin/v1/me/password" -d "{\"current_password\":\"$temp\",\"new_password\":\"ci-smoke-new-password\"}" >/dev/null
role_id="$(admin -X POST "$ADMIN_URL/admin/v1/roles" -d '{"name":"ci smoke role","description":"CI"}' | jq -r .id)"
test -n "$role_id"
admin -X PUT "$ADMIN_URL/admin/v1/roles/$role_id/permissions" -d '{"permissions":[]}' >/dev/null
account_id="$(admin "$ADMIN_URL/admin/v1/accounts?size=1" | jq -r '.items[0].id')"
admin -X PUT "$ADMIN_URL/admin/v1/accounts/$account_id/roles" -d "{\"role_ids\":[\"$role_id\"]}" >/dev/null
admin "$ADMIN_URL/admin/v1/audit-logs?size=100" | jq -e --arg id "$role_id" '.items[] | select(.target == $id)' >/dev/null
