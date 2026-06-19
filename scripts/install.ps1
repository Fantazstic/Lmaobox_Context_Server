param(
    [switch]$SkipNodeInstall,
    [switch]$SkipDocsFetch,
    [switch]$RunWebRefresh,
    [switch]$SkipMcpConfig
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Resolve-Path (Join-Path $scriptDir "..")
$mcpJson = "$env:APPDATA\Code\User\mcp.json"
$runScript = Join-Path $repoRoot "run-mcp.ps1"

Write-Host "==========================================="
Write-Host " Lmaobox Context MCP - Bootstrap Installer "
Write-Host "==========================================="
Write-Host "Repo root: $repoRoot"
Write-Host ""

# ── 1. Go ────────────────────────────────────────────────────────────────────
Write-Host "[1/4] Checking Go installation..."
$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) {
    Write-Host "  Go not found. Attempting install via winget..."
    winget install GoLang.Go --silent --accept-source-agreements --accept-package-agreements
    # Refresh PATH
    $env:PATH = [System.Environment]::GetEnvironmentVariable("PATH", "Machine") + ";" +
    [System.Environment]::GetEnvironmentVariable("PATH", "User")
    $goCmd = Get-Command go -ErrorAction SilentlyContinue
    if (-not $goCmd) {
        Write-Error "Go install failed. Install manually from: https://go.dev/dl/ then re-run this script."
        exit 1
    }
}
$goVersion = & go version
Write-Host "  Go: $goVersion" -ForegroundColor Green

# ── 2. Build binary ──────────────────────────────────────────────────────────
Write-Host "[2/4] Building MCP server binary..."
Push-Location $repoRoot
try {
    & go build -o lmaobox-mcp.exe .
    if ($LASTEXITCODE -ne 0) {
        Write-Error "go build failed."
        exit 1
    }
}
finally {
    Pop-Location
}
Write-Host "  Built: $repoRoot\lmaobox-mcp.exe" -ForegroundColor Green

# ── 3. Node deps (for bundler + crawler) ────────────────────────────────────
if (-not $SkipNodeInstall) {
    Write-Host "[3/4] Installing Node dependencies in automations/..."
    $npmCmd = Get-Command npm -ErrorAction SilentlyContinue
    if (-not $npmCmd) {
        Write-Host "  npm not found - skipping Node dependencies (bundler may not work)"
    }
    else {
        Push-Location (Join-Path $repoRoot "automations")
        try {
            if (Test-Path "package-lock.json") { npm ci } else { npm install }
        }
        finally {
            Pop-Location
        }
        Write-Host "  Node dependencies installed." -ForegroundColor Green
    }
}
else {
    Write-Host "[3/4] Skipping Node dependency install (-SkipNodeInstall)"
}

# ── 4. mcp.json ──────────────────────────────────────────────────────────────
if (-not $SkipMcpConfig) {
    Write-Host "[4/4] Updating MCP config: $mcpJson"

    $runScriptEscaped = $runScript -replace "\\", "\\\\"

    if (Test-Path $mcpJson) {
        $raw = Get-Content $mcpJson -Raw | ConvertFrom-Json
    }
    else {
        $raw = [PSCustomObject]@{ inputs = @(); servers = [PSCustomObject]@{} }
    }

    $serverEntry = [PSCustomObject]@{
        type     = "stdio"
        command  = "powershell"
        args     = @("-ExecutionPolicy", "Bypass", "-File", $runScript)
        disabled = $false
    }

    if ($raw.servers.PSObject.Properties.Name -contains "lmaobox-context") {
        $raw.servers."lmaobox-context" = $serverEntry
        Write-Host "  Updated existing lmaobox-context entry." -ForegroundColor Green
    }
    else {
        Add-Member -InputObject $raw.servers -MemberType NoteProperty -Name "lmaobox-context" -Value $serverEntry
        Write-Host "  Added lmaobox-context entry." -ForegroundColor Green
    }

    $raw | ConvertTo-Json -Depth 10 | Set-Content $mcpJson -Encoding UTF8
    Write-Host "  mcp.json saved." -ForegroundColor Green
}
else {
    Write-Host "[4/4] Skipping mcp.json update (-SkipMcpConfig)"
}

# ── 5. Install packaged types to a stable local folder so Sumneko can use them across workspaces
Write-Host "[5/5] Installing packaged types to user local app data (for Sumneko)..."
$globalBase = Join-Path $env:LOCALAPPDATA "lmaobox-context-server"
$globalTypesDir = Join-Path $globalBase "types"
$srcTypes = Join-Path $repoRoot "types"
try {
    if (Test-Path $srcTypes) {
        if (Test-Path $globalTypesDir) {
            Remove-Item -Path $globalTypesDir -Recurse -Force -ErrorAction SilentlyContinue
        }
        New-Item -ItemType Directory -Path $globalTypesDir -Force | Out-Null
        Copy-Item -Path (Join-Path $srcTypes "*") -Destination $globalTypesDir -Recurse -Force -ErrorAction Stop
        Write-Host "  Packaged types copied to: $globalTypesDir" -ForegroundColor Green
    }
    else {
        Write-Host "  No types/ directory found in repo; skipping packaged types install." -ForegroundColor Yellow
    }
}
catch {
    Write-Host "  Failed to copy packaged types: $_" -ForegroundColor Yellow
}

# ── Optional: docs fetch ─────────────────────────────────────────────────────
if (-not $SkipDocsFetch) {
    $fetchScript = Join-Path $scriptDir "fetch-upstream-docs.ps1"
    if (Test-Path $fetchScript) {
        Write-Host "[opt] Syncing upstream docs..."
        & $fetchScript
    }
}

if ($RunWebRefresh) {
    $nodeCmd = Get-Command node -ErrorAction SilentlyContinue
    if ($nodeCmd) {
        Write-Host "[opt] Running website crawler refresh..."
        Push-Location $repoRoot
        node automations/refresh-docs.js
        Pop-Location
    }
    else {
        Write-Host "[opt] node not found - skipping web refresh"
    }
}

Write-Host ""
Write-Host "============================================" -ForegroundColor Green
Write-Host " Install complete!" -ForegroundColor Green
Write-Host "============================================" -ForegroundColor Green
Write-Host ""
Write-Host "MCP server entry point: $runScript"
Write-Host "Restart VS Code or reload the MCP config to pick up the changes."
Write-Host ""
Write-Host "On each startup, run-mcp.ps1 will auto-rebuild if .go sources changed."
Write-Host "No binary is stored in git - source is always the truth."
