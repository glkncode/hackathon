$ErrorActionPreference = 'Stop'
$workspaceRoot = Split-Path -Parent $PSScriptRoot
Push-Location -LiteralPath $workspaceRoot
try {
    $dockerCommand = Get-Command docker -ErrorAction SilentlyContinue
    $dockerExecutable = if ($dockerCommand) { $dockerCommand.Source } else {
        @(
            "$env:LOCALAPPDATA\Programs\DockerDesktop\resources\bin\docker.exe",
            'C:\Program Files\Docker\Docker\resources\bin\docker.exe'
        ) | Where-Object { Test-Path -LiteralPath $_ } | Select-Object -First 1
    }
    if (-not $dockerExecutable) { throw 'Install and start Docker Desktop first.' }
    $taskPython = Join-Path $workspaceRoot 'ai-service\.venv\Scripts\python.exe'
    if (-not (Test-Path -LiteralPath $taskPython)) {
        throw 'Create ai-service/.venv and install ai-service/requirements.txt first.'
    }
    @'
import json, secrets
from pathlib import Path
from dotenv import dotenv_values, set_key
root = Path(".local-integration")
root.mkdir(exist_ok=True)
credential_path = root / "credentials.json"
credentials = json.loads(credential_path.read_text()) if credential_path.exists() else {
    "admin_password": secrets.token_urlsafe(24),
    "postgres_password": secrets.token_urlsafe(24),
}
credential_path.write_text(json.dumps(credentials))
source = Path("ai-service/.env")
config = dotenv_values(source) if source.exists() else {}
if not (config.get("AI_SERVICE_TOKEN") or "").strip():
    token = secrets.token_urlsafe(32)
    set_key(source, "AI_SERVICE_TOKEN", token, quote_mode="never")
    config["AI_SERVICE_TOKEN"] = token
settings = {
    "POSTGRES_PASSWORD": credentials["postgres_password"],
    "ADMIN_PASSWORD": credentials["admin_password"],
    "AI_SERVICE_TOKEN": config["AI_SERVICE_TOKEN"],
    **{key: config.get(key, "") or "" for key in (
        "LLM_BASE_URL", "LLM_MODEL", "LLM_API_KEY", "LLM_TIMEOUT", "LLM_ALLOW_REMOTE"
    )},
}
settings["LLM_TIMEOUT"] = settings["LLM_TIMEOUT"] or "60"
settings["LLM_ALLOW_REMOTE"] = settings["LLM_ALLOW_REMOTE"] or "false"
from dotenv import set_key
destination = root / "docker.env"
destination.touch(exist_ok=True)
for key, value in settings.items():
    if "\n" in value or "\r" in value:
        raise RuntimeError("Invalid multiline local setting")
    set_key(destination, key, value, quote_mode="always")
print("Local Docker configuration ready; credentials were not printed.")
'@ | & $taskPython -
    if ($LASTEXITCODE -ne 0) { throw 'Failed to prepare local Docker configuration.' }
    & $dockerExecutable compose --env-file .local-integration/docker.env up -d --build
    if ($LASTEXITCODE -ne 0) { throw 'Docker Compose startup failed.' }
    & $dockerExecutable compose --env-file .local-integration/docker.env ps
} finally {
    Pop-Location
}
