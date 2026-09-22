param(
    [ValidateSet("openai", "anthropic", "gemini", "deepseek", "openrouter", "groq", "together", "fireworks", "cerebras", "mistral", "xai", "sambanova", "nvidia", "novita", "upstage", "ollama-cloud", "hyperbolic", "alibaba", "huggingface")]
    [string]$Provider,
    [switch]$Remove
)

$ErrorActionPreference = "Stop"
Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class DZ23Environment {
    [DllImport("user32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint flags, uint timeout, out UIntPtr result);
}
"@

function Publish-EnvironmentChange {
    $result = [UIntPtr]::Zero
    [void][DZ23Environment]::SendMessageTimeout([IntPtr]0xffff, 0x001A, [UIntPtr]::Zero, "Environment", 0x0002, 5000, [ref]$result)
}
$providerVariables = [ordered]@{
    "openai" = "OPENAI_API_KEY"
    "anthropic" = "ANTHROPIC_API_KEY"
    "gemini" = "GEMINI_API_KEY"
    "deepseek" = "DEEPSEEK_API_KEY"
    "openrouter" = "OPENROUTER_API_KEY"
    "groq" = "GROQ_API_KEY"
    "together" = "TOGETHER_API_KEY"
    "fireworks" = "FIREWORKS_API_KEY"
    "cerebras" = "CEREBRAS_API_KEY"
    "mistral" = "MISTRAL_API_KEY"
    "xai" = "XAI_API_KEY"
    "sambanova" = "SAMBANOVA_API_KEY"
    "nvidia" = "NVIDIA_API_KEY"
    "novita" = "NOVITA_API_KEY"
    "upstage" = "UPSTAGE_API_KEY"
    "ollama-cloud" = "OLLAMA_API_KEY"
    "hyperbolic" = "HYPERBOLIC_API_KEY"
    "alibaba" = "ALIBABA_API_KEY"
    "huggingface" = "HUGGINGFACE_TOKEN"
}

if (-not $Provider) {
    Write-Host "Ollama DZ23 - configure a provider" -ForegroundColor Cyan
    $names = @($providerVariables.Keys)
    for ($index = 0; $index -lt $names.Count; $index++) {
        Write-Host ("{0,2}. {1}" -f ($index + 1), $names[$index])
    }
    $selection = Read-Host "Provider number"
    $parsed = 0
    if (-not [int]::TryParse($selection, [ref]$parsed) -or $parsed -lt 1 -or $parsed -gt $names.Count) {
        throw "Invalid provider selection"
    }
    $Provider = $names[$parsed - 1]
}

$variable = $providerVariables[$Provider]
$secretDirectory = Join-Path $env:LOCALAPPDATA "Ollama DZ23\secrets"
$secretPath = Join-Path $secretDirectory "$variable.dpapi"
if ($Remove) {
    [Environment]::SetEnvironmentVariable($variable, $null, "User")
    [Environment]::SetEnvironmentVariable("${variable}_FILE", $null, "User")
    Remove-Item -LiteralPath $secretPath -Force -ErrorAction SilentlyContinue
    Publish-EnvironmentChange
    Write-Host "$Provider credential removed. Restart Ollama DZ23." -ForegroundColor Yellow
    exit 0
}

$secure = Read-Host "Paste the $Provider API key" -AsSecureString
if ($secure.Length -eq 0) {
    throw "The API key cannot be empty"
}
New-Item -ItemType Directory -Path $secretDirectory -Force | Out-Null
$secure | ConvertFrom-SecureString | Set-Content -LiteralPath $secretPath -Encoding ASCII -NoNewline
[Environment]::SetEnvironmentVariable($variable, $null, "User")
[Environment]::SetEnvironmentVariable("${variable}_FILE", $secretPath, "User")
Publish-EnvironmentChange

Write-Host "$Provider configured. Restart Ollama DZ23 to apply it." -ForegroundColor Green
