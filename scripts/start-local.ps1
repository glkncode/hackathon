$ErrorActionPreference = 'Stop'
$workspaceRoot = Split-Path -Parent $PSScriptRoot
Push-Location -LiteralPath $workspaceRoot
try {
    if (-not (Test-Path -LiteralPath '.env')) {
        throw 'Copy .env.example to .env and fill POSTGRES_PASSWORD, ADMIN_PASSWORD and AI_SERVICE_TOKEN. See docs/docker.md.'
    }
    & docker compose config --quiet
    if ($LASTEXITCODE -ne 0) { throw 'Invalid Docker Compose configuration.' }
    & docker compose up --build -d
    if ($LASTEXITCODE -ne 0) { throw 'Docker Compose startup failed.' }
    & docker compose ps
} finally {
    Pop-Location
}
