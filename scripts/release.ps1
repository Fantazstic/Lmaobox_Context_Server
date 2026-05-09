param(
    [ValidateSet('patch', 'minor', 'major')]
    [string]$Bump = 'patch',
    [switch]$NoPush
)

$ErrorActionPreference = 'Stop'

function Assert-Ok($Condition, $Message) {
    if (-not $Condition) {
        throw $Message
    }
}

function Read-Utf8Json($Path) {
    Assert-Ok (Test-Path -LiteralPath $Path) "Missing file: $Path"
    $raw = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    return $raw | ConvertFrom-Json
}

function Write-Utf8Json($Path, $Object) {
    $json = $Object | ConvertTo-Json -Depth 20
    $json = $json + "`n"
    [System.IO.File]::WriteAllText($Path, $json, [System.Text.Encoding]::UTF8)
}

function Bump-Semver($Version, $BumpKind) {
    $m = [regex]::Match($Version, '^(\d+)\.(\d+)\.(\d+)$')
    Assert-Ok $m.Success "Unsupported version format: $Version"
    $major = [int]$m.Groups[1].Value
    $minor = [int]$m.Groups[2].Value
    $patch = [int]$m.Groups[3].Value

    if ($BumpKind -eq 'major') {
        $major = $major + 1
        $minor = 0
        $patch = 0
    }
    elseif ($BumpKind -eq 'minor') {
        $minor = $minor + 1
        $patch = 0
    }
    else {
        $patch = $patch + 1
    }

    return "$major.$minor.$patch"
}

function Replace-FileText($Path, $Pattern, $Replacement) {
    Assert-Ok (Test-Path -LiteralPath $Path) "Missing file: $Path"
    $text = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    $next = [regex]::Replace($text, $Pattern, $Replacement)
    Assert-Ok ($next -ne $text) "No changes applied to $Path (pattern not found)"
    [System.IO.File]::WriteAllText($Path, $next, [System.Text.Encoding]::UTF8)
}

function Replace-PackageLockVersion($Path, $NewVersion) {
    Assert-Ok (Test-Path -LiteralPath $Path) "Missing file: $Path"
    $text = Get-Content -LiteralPath $Path -Raw -Encoding UTF8

    $patternRoot = '(?s)("name"\s*:\s*"lmaobox-context"\s*,\s*\r?\n\s*"version"\s*:\s*")\d+\.\d+\.\d+("\s*,)'
    $next = [regex]::Replace($text, $patternRoot, ('${1}' + $NewVersion + '${2}'), 1)
    Assert-Ok ($next -ne $text) "Failed to update package-lock root version"

    $patternPkg = '(?s)("packages"\s*:\s*\{\s*\r?\n\s*""\s*:\s*\{\s*\r?\n\s*"name"\s*:\s*"lmaobox-context"\s*,\s*\r?\n\s*"version"\s*:\s*")\d+\.\d+\.\d+("\s*,)'
    $final = [regex]::Replace($next, $patternPkg, ('${1}' + $NewVersion + '${2}'), 1)
    Assert-Ok ($final -ne $next) "Failed to update package-lock package version"

    [System.IO.File]::WriteAllText($Path, $final, [System.Text.Encoding]::UTF8)
}

function Invoke-Checked($FilePath, $Arguments) {
    $p = Start-Process -FilePath $FilePath -ArgumentList $Arguments -NoNewWindow -PassThru -Wait
    Assert-Ok ($p.ExitCode -eq 0) "Command failed: $FilePath $Arguments"
}

$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

$pkgPath = Join-Path $repoRoot 'vscode-extension\package.json'
$lockPath = Join-Path $repoRoot 'vscode-extension\package-lock.json'
$mainPath = Join-Path $repoRoot 'main.go'

$pkg = Read-Utf8Json $pkgPath
Assert-Ok ($null -ne $pkg.version) 'package.json missing version'

$oldVersion = [string]$pkg.version
$newVersion = Bump-Semver $oldVersion $Bump
$tag = "v$newVersion"

Write-Host "Bumping version $oldVersion -> $newVersion"

$pkg.version = $newVersion
Write-Utf8Json $pkgPath $pkg

Replace-PackageLockVersion $lockPath $newVersion
Replace-FileText $mainPath 'SERVER_VERSION\s*=\s*"\d+\.\d+\.\d+"' ('SERVER_VERSION = "' + $newVersion + '"')

Invoke-Checked 'go' @('test', './...')

Invoke-Checked 'git' @('add', '-A')
Invoke-Checked 'git' @('commit', '-m', "Release $tag")
Invoke-Checked 'git' @('tag', $tag)

if (-not $NoPush) {
    Invoke-Checked 'git' @('push', 'origin', 'HEAD')
    Invoke-Checked 'git' @('push', 'origin', $tag)
}

Write-Host "Done: $tag"

