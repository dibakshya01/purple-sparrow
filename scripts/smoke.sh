#!/usr/bin/env bash
# Purple Sparrow smoke test — exercises the core path end-to-end against a running
# server. Usage:
#   PS_ADMIN_API_KEY=ps_sk_... [PS_BASE=http://127.0.0.1:8787] ./scripts/smoke.sh
set -euo pipefail

BASE="${PS_BASE:-http://127.0.0.1:8787}"
KEY="${PS_ADMIN_API_KEY:?set PS_ADMIN_API_KEY}"
AUTH="Authorization: Bearer ${KEY}"
fail=0

check() { # check <label> <expected-code> <actual-code>
  if [ "$2" = "$3" ]; then printf '  ok   %-26s %s\n' "$1" "$3"
  else printf '  FAIL %-26s got %s want %s\n' "$1" "$3" "$2"; fail=1; fi
}
# get <method> <path> [json-body]  -> prints the HTTP status code
get() {
  local method="$1" path="$2" body="${3:-}"
  if [ -n "$body" ]; then
    curl -s -o /dev/null -w '%{http_code}' -X "$method" -H "$AUTH" -H 'Content-Type: application/json' -d "$body" "${BASE}${path}"
  else
    curl -s -o /dev/null -w '%{http_code}' -X "$method" -H "$AUTH" "${BASE}${path}"
  fi
}
anon() { curl -s -o /dev/null -w '%{http_code}' "${BASE}$1"; }

echo "smoke: ${BASE}"
check "healthz"          200 "$(anon /healthz)"
check "readyz"           200 "$(anon /readyz)"
check "service info"     200 "$(anon /v1)"
check "docs index"       200 "$(anon /docs)"
check "meta (admin)"     200 "$(get GET /meta)"
check "meta (anon→403)"  403 "$(anon /meta)"

T="smoke_$(date +%s)"
tbody=$(printf '{"name":"%s","columns":[{"name":"owner_id","type":"text"},{"name":"body","type":"text"}]}' "$T")
pbody=$(printf '{"table":"%s","action":"select","roles":["anon"],"using":"true"}' "$T")
check "create table"     201 "$(get POST /v1/tables "$tbody")"
check "anon read→403"    403 "$(anon "/v1/tables/${T}/records")"
check "create policy"    201 "$(get POST /v1/policies "$pbody")"
check "insert record"    201 "$(get POST "/v1/tables/${T}/records" '{"owner_id":"me","body":"hello"}')"
check "anon read→200"    200 "$(anon "/v1/tables/${T}/records")"

B="smokebucket$(date +%s)"
bbody=$(printf '{"name":"%s","public":true}' "$B")
check "create bucket"    201 "$(get POST /v1/storage/buckets "$bbody")"
check "upload object"    201 "$(curl -s -o /dev/null -w '%{http_code}' -X PUT -H "$AUTH" --data-binary 'bytes' "${BASE}/v1/storage/${B}/hello.txt")"
check "download object"  200 "$(anon "/v1/storage/${B}/hello.txt")"

check "list functions"   200 "$(get GET /v1/functions)"

# cleanup (best-effort)
curl -s -o /dev/null -X DELETE -H "$AUTH" "${BASE}/v1/tables/${T}" || true
curl -s -o /dev/null -X DELETE -H "$AUTH" "${BASE}/v1/storage/buckets/${B}" || true

if [ "$fail" = 0 ]; then echo "SMOKE OK"; else echo "SMOKE FAILED"; exit 1; fi
