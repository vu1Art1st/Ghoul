#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$ToshellRoot = '',
    [string]$ServerExe = '',
    [int]$ApiPort = 0,
    [switch]$SkipBuild,
    [switch]$KeepWorkDir
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..\..')).Path
if ($ToshellRoot -eq '') {
    $ToshellRoot = (Resolve-Path (Join-Path $repoRoot '..\ARTEX-fork\Toshell')).Path
} else {
    $ToshellRoot = (Resolve-Path $ToshellRoot).Path
}

$tmpRoot = Join-Path $repoRoot '.tmp\toshell-contract'
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null
$tmpRoot = [IO.Path]::GetFullPath($tmpRoot)
$workspacePrefix = [IO.Path]::GetFullPath($repoRoot).TrimEnd('\') + '\'
if (-not $tmpRoot.StartsWith($workspacePrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "temporary directory escaped workspace: $tmpRoot"
}

if ($ServerExe -eq '') {
    $ServerExe = Join-Path $tmpRoot 'toserver.exe'
}
$ServerExe = [IO.Path]::GetFullPath($ServerExe)

if (-not $SkipBuild) {
    Push-Location $ToshellRoot
    try {
        $env:CGO_ENABLED = '0'
        $env:GOTOOLCHAIN = 'go1.26.3'
        $env:GOMODCACHE = Join-Path $repoRoot '.tmp\gomodcache'
        $env:GOCACHE = Join-Path $repoRoot '.tmp\gocache'
        if ([string]::IsNullOrWhiteSpace($env:GOPROXY)) {
            $env:GOPROXY = 'https://goproxy.cn,direct'
        }
        & go build -o $ServerExe ./cmd/server
        if ($LASTEXITCODE -ne 0) {
            throw 'Toshell go build failed'
        }
    } finally {
        Pop-Location
    }
}

if (-not (Test-Path -LiteralPath $ServerExe)) {
    throw "Toshell server binary not found: $ServerExe"
}

$workDir = Join-Path $tmpRoot 'capture-work'
if (Test-Path -LiteralPath $workDir) {
    $workDir = [IO.Path]::GetFullPath($workDir)
    if (-not $workDir.StartsWith($tmpRoot.TrimEnd('\') + '\', [StringComparison]::OrdinalIgnoreCase)) {
        throw "work directory escaped temporary root: $workDir"
    }
    Remove-Item -LiteralPath $workDir -Recurse -Force
}
New-Item -ItemType Directory -Force -Path $workDir | Out-Null

Push-Location $repoRoot
try {
    $env:GOTOOLCHAIN = 'go1.26.3'
    $env:GOMODCACHE = Join-Path $repoRoot '.tmp\gomodcache'
    $env:GOCACHE = Join-Path $repoRoot '.tmp\gocache'
    if ([string]::IsNullOrWhiteSpace($env:GOPROXY)) {
        $env:GOPROXY = 'https://goproxy.cn,direct'
    }
    $args = @(
        'run', './integrations/toshell/cmd/contract-capture',
        '-server-exe', $ServerExe,
        '-out-dir', (Join-Path $repoRoot 'integrations\toshell\contract'),
        '-surface', (Join-Path $repoRoot 'integrations\toshell\tool_surface.json'),
        '-work-dir', $workDir
    )
    if ($ApiPort -ne 0) {
        $args += @('-api-port', [string]$ApiPort)
    }
    & go @args
    if ($LASTEXITCODE -ne 0) {
        throw 'contract capture failed'
    }
} finally {
    Pop-Location
    if (-not $KeepWorkDir -and (Test-Path -LiteralPath $workDir)) {
        Remove-Item -LiteralPath $workDir -Recurse -Force
    }
}
