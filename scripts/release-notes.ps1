# Writes the GitHub release notes for one version: an install line plus that version's section of
# CHANGELOG.md. Fails if the section is missing, so a release can't go out without a changelog.
#   scripts\release-notes.ps1 -Version 0.1.1 -Out notes.md
param(
    [Parameter(Mandatory)][string]$Version,
    [string]$Out = 'release-notes.md'
)
$ErrorActionPreference = 'Stop'
$changelog = Get-Content (Join-Path (Split-Path $PSScriptRoot -Parent) 'CHANGELOG.md') -Raw
$m = [regex]::Match($changelog, "(?ms)^## \[$([regex]::Escape($Version))\][^\r\n]*\r?\n(.*?)(?=^## \[|\z)")
if (-not $m.Success -or -not $m.Groups[1].Value.Trim()) {
    throw "CHANGELOG.md has no entry for $Version. Add a '## [$Version] - <date>' section first."
}
$install = "**Install:** download ``net-merge-setup-$Version.exe`` below and run it (needs admin; it upgrades " +
    "an existing install in place). A portable zip is attached too. Not code-signed yet, so SmartScreen " +
    "will warn: *More info > Run anyway*."
$install += "`n`n**macOS:** download ``net-merge-$Version-macos-universal.tar.gz`` (or ``brew install net-merge/tap/net-merge``). " +
    "**Linux:** install with apt, dnf, zypper, apk or pacman, or download the ``net-merge-$Version-linux-<cpu>.tar.gz`` " +
    "for your CPU, or a ``.deb`` / ``.rpm`` / ``.apk`` / ``.pkg.tar.zst``. The steps are in the Install section of the README."
# UTF-8 without a BOM (Windows PowerShell's -Encoding utf8 adds one, which GitHub would show)
$path = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Out)
$footer = "---`n**From the same developer:**`n`n" +
    "- [RoutEngine](https://routengine.net/?utm_source=net-merge&utm_medium=release): choose the connection for each app, " +
    "and set a speed limit or data cap per app. Windows, macOS and Linux. Free for personal use.`n" +
    "- [DormHost](https://dormhost.dev/?utm_source=net-merge&utm_medium=release): put your college project online. " +
    "Node.js, Python or Docker with a database, 30 days free."
[IO.File]::WriteAllText($path, $install + "`n`n" + $m.Groups[1].Value.Trim() + "`n`n" + $footer + "`n", (New-Object Text.UTF8Encoding $false))
Write-Host "wrote $Out"
