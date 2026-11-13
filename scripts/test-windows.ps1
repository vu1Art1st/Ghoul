param()

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

$psql = $env:GHOUL_TEST_PSQL
if (-not $psql) {
  $psql = "C:\Users\H4ppy\AppData\Local\com.tinyapp.DBngin\Binaries\postgresql\16.14\bin\psql.exe"
}
if (-not (Test-Path -LiteralPath $psql)) {
  throw "psql not found: $psql"
}

$hostName = if ($env:GHOUL_TEST_PG_HOST) { $env:GHOUL_TEST_PG_HOST } else { "127.0.0.1" }
$port = if ($env:GHOUL_TEST_PG_PORT) { $env:GHOUL_TEST_PG_PORT } else { "5432" }
$user = if ($env:GHOUL_TEST_PG_USER) { $env:GHOUL_TEST_PG_USER } else { "postgres" }
$database = "ghoul_test"

$exists = & $psql -h $hostName -p $port -U $user -d postgres -w -A -t -c "SELECT 1 FROM pg_database WHERE datname = '$database';"
if ($LASTEXITCODE -ne 0) {
  throw "failed to query local PostgreSQL"
}
if ($exists.Trim() -ne "1") {
  & $psql -h $hostName -p $port -U $user -d postgres -w -v ON_ERROR_STOP=1 -c "CREATE DATABASE $database;"
  if ($LASTEXITCODE -ne 0) {
    throw "failed to create $database"
  }
}

$env:GHOUL_TEST_PG_DSN = "postgres://$user@$hostName`:$port/${database}?sslmode=disable"

[string[]]$testPackages = if ($args.Count -gt 0) {
  $args | ForEach-Object { $_.ToString() }
} else {
  @("./db", "./agent", "./server", "./shared", "./hosttools", "./vulnintel")
}

$goArgs = @("-p", "1") + $testPackages
& go test @goArgs
exit $LASTEXITCODE
