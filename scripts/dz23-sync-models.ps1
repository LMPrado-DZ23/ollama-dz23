param(
    [string]$Python,
    [switch]$Apply,
    [switch]$Restart
)
$ErrorActionPreference = 'Stop'
if ($Restart -and -not $Apply) { throw '-Restart requires -Apply' }
if (-not $Python) {
    $candidate = Join-Path $env:USERPROFILE 'DZ23-LiteLLM\runtime\Scripts\python.exe'
    if (Test-Path $candidate) { $Python = $candidate }
    else { $Python = (Get-Command python -ErrorAction Stop).Source }
}
$scriptPath = Join-Path $PSScriptRoot 'dz23_sync_models.py'
$argsList = @('-u', $scriptPath)
if ($Apply) { $argsList += '--apply' }
& $Python @argsList
if ($LASTEXITCODE -ne 0) { throw 'Model synchronization failed; Ollama has not been restarted.' }
if (-not $Restart) { return }
$status = Get-Content (Join-Path $PSScriptRoot 'sync-status.json') -Raw | ConvertFrom-Json
if (-not $status.applied) { Write-Host 'Catalog unchanged; restart unnecessary.'; return }
$install = Join-Path $env:LOCALAPPDATA 'Programs\Ollama DZ23'
$app = Join-Path $install 'ollama app.exe'
$server = Join-Path $install 'ollama.exe'
if (-not (Test-Path $app)) { throw 'Catalog saved. Restart your Ollama installation manually.' }
# Restart is explicit because it interrupts current inference requests.
Get-Process | Where-Object { $_.Path -eq $app } | Stop-Process
Get-Process | Where-Object { $_.Path -eq $server } | Stop-Process
foreach ($entry in [Environment]::GetEnvironmentVariables('User').GetEnumerator()) {
    if ($entry.Key -match '^OLLAMA') { [Environment]::SetEnvironmentVariable($entry.Key, $entry.Value, 'Process') }
}
Start-Process -FilePath $app
Write-Host 'Ollama restarted. See sync-status.json for per-provider results.'
