#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

mode="${1:-targeted}"
env_file="${GHOUL_TEST_ENV_FILE:-.env.test}"

if [[ ! -f "$env_file" ]]; then
  echo "missing $env_file; copy .env.test.example and set GHOUL_TEST_POSTGRES_PASSWORD" >&2
  exit 1
fi

compose=(
  docker compose
  --project-name ghoul-test
  --env-file "$env_file"
  -f docker-compose.test.yml
)

"${compose[@]}" up -d --wait postgres-test

postgres_id="$("${compose[@]}" ps -q postgres-test)"
network="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "$postgres_id")"
pgpass="$("${compose[@]}" exec -T postgres-test printenv POSTGRES_PASSWORD | tr -d '\r\n')"

if [[ -z "$postgres_id" || -z "$network" || -z "$pgpass" ]]; then
  echo "failed to resolve isolated test postgres metadata" >&2
  exit 1
fi

encoded="$(PGPASS="$pgpass" python3 -c 'import os, urllib.parse; print(urllib.parse.quote(os.environ["PGPASS"], safe=""))')"
dsn="postgres://ghoul_test:${encoded}@postgres-test:5432/ghoul_test?sslmode=disable"

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
  docker build --target gobuild -t ghoul-test:go .
  docker build --target web -t ghoul-test:web .
fi
docker run --rm ghoul-test:web npm exec tsc -- --noEmit

run_backend() {
  docker run --rm --network "$network" \
    -v "$root":/src -w /src \
    -e GHOUL_TEST_PG_DSN="$dsn" \
    -e GHOUL_FINDING_CASE_IT=1 \
    -e GOCACHE=/tmp/go-cache \
    ghoul-test:go "$@"
}

case "$mode" in
  targeted)
    run_backend go test ./db ./server ./report -run \
      'Test(AddFindingCaseMembers|CreateAutoFindingMergeJob|SaveFindingCaseAnalysesAllowsAutoEmptyResult|AutoFindingCaseSaveDoesNotOverwriteManualLock|TaskDocxEmbedsCaseMemberScreenshots|FindingEvidenceImageKindsCoverC2Screenshots|FindingMergeClusterRejectsBlockedPair|FindingAutoMergeInput|ReconcileAutoFindingMerge|SaveFindingCaseAnalysisPersistsDeliveryFieldsAtomically|FindingMergeJobLifecycle|FindingCaseAPIIntegrationTask4AutoClustersMultipleGroups)' -v
    ;;
  backend)
    run_backend go test -p 1 ./...
    ;;
  backend-unit)
    run_backend go test -p 1 ./db ./server ./report
    ;;
  shared)
    run_backend go test ./shared ./server \
      -run 'TestPublishAndMaterializeLifecycle|TestSharedAssetMetadataAndVersionPreviewLifecycle|TestSharedAssetMetadataAndContentAPI' \
      -count=1 -v
    ;;
  *)
    echo "usage: $0 [targeted|backend|backend-unit|shared]" >&2
    exit 2
    ;;
esac
