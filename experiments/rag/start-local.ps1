param(
    [Parameter(Mandatory=$true)][ValidateSet('start','stop')][string]$Action
)
$ErrorActionPreference = 'Stop'
$taskRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$taskNames = @('hackathon-hybrid-test-api', 'hackathon-hybrid-test-ai', 'hackathon-hybrid-test-pg')
$taskNetwork = 'hackathon-hybrid-experiment'
if ($Action -eq 'stop') {
    # Stop only these explicitly named disposable fixtures; retain DB/files.
    & docker stop @taskNames
    if ($LASTEXITCODE -ne 0) { throw 'Cannot stop experiment containers' }
    exit
}
foreach ($taskName in $taskNames) {
    $null = & docker inspect $taskName 2>$null
    if ($LASTEXITCODE -eq 0) { throw "Container already exists: $taskName. Resume it explicitly; this script never deletes data." }
}
$taskSourceAI = (& docker inspect hackathon-ai-service-1 | ConvertFrom-Json)[0]
if ($LASTEXITCODE -ne 0) { throw 'Need the already authorized inference configuration from the local Python service' }
$taskImages = @{}
foreach ($taskImage in @('hackathon-api:latest','hackathon-ai-service:latest','pgvector/pgvector:pg17')) {
    $taskInfo = (& docker image inspect $taskImage | ConvertFrom-Json)[0]
    if ($LASTEXITCODE -ne 0) { throw "Missing image $taskImage; build/pull it explicitly before starting. No automatic downloads." }
    $taskImages[$taskImage] = $taskInfo.Id
}
$taskTemp = Join-Path $taskRoot '.local-integration'
New-Item -ItemType Directory -Force $taskTemp | Out-Null
$taskEnvFile = Join-Path $taskTemp 'hybrid-private.env'
try {
    $null = & docker network inspect $taskNetwork 2>$null
    if ($LASTEXITCODE -ne 0) {
        & docker network create $taskNetwork
        if ($LASTEXITCODE -ne 0) { throw 'Cannot create isolated network' }
    }
    & docker run --name $taskNames[2] --network $taskNetwork --cpus 1 --memory 768m `
        -e POSTGRES_USER=search -e POSTGRES_PASSWORD=search-test-only -e POSTGRES_DB=search_test `
        -p 127.0.0.1:55437:5432 -d $taskImages['pgvector/pgvector:pg17']
    if ($LASTEXITCODE -ne 0) { throw 'Cannot start disposable Postgres' }
    $taskPrivate = @($taskSourceAI.Config.Env | Where-Object { $_ -match '^(LLM_|AI_SERVICE_TOKEN=)' })
    $taskToken = ($taskPrivate | Where-Object { $_.StartsWith('AI_SERVICE_TOKEN=') }).Substring(17)
    if (-not $taskToken) { throw 'Missing private service token' }
    [IO.File]::WriteAllLines($taskEnvFile, [string[]]$taskPrivate)
    & docker run --name $taskNames[1] --network $taskNetwork --env-file $taskEnvFile --cpus 1 --memory 512m `
        -p 127.0.0.1:18083:8000 -d $taskImages['hackathon-ai-service:latest']
    if ($LASTEXITCODE -ne 0) { throw 'Cannot start isolated Python generator' }
    $taskGoEnv = @(
        'DATABASE_URL=postgres://search:search-test-only@hackathon-hybrid-test-pg:5432/search_test?sslmode=disable',
        'ADMIN_PASSWORD=experiment-test-only-123', 'ALLOWED_ORIGINS=http://127.0.0.1:18081',
        'AI_SERVICE_URL=http://hackathon-hybrid-test-ai:8000', ('AI_SERVICE_TOKEN='+$taskToken),
        'RAG_ENABLED=true', 'INGESTION_ENABLED=true', 'INGESTION_POLL_INTERVAL=100ms', 'HTTP_PORT=8080'
    )
    [IO.File]::WriteAllLines($taskEnvFile, [string[]]$taskGoEnv)
    for ($taskAttempt=0; $taskAttempt -lt 30; $taskAttempt++) {
        & docker exec $taskNames[2] pg_isready -U search -d search_test *> $null
        if ($LASTEXITCODE -eq 0) { break }
        Start-Sleep -Milliseconds 300
    }
    & docker run --name $taskNames[0] --network $taskNetwork --env-file $taskEnvFile --cpus 1 --memory 384m `
        -p 127.0.0.1:18081:8080 -d $taskImages['hackathon-api:latest']
    if ($LASTEXITCODE -ne 0) { throw 'Cannot start isolated Go API' }
} finally {
    if (Test-Path -LiteralPath $taskEnvFile) { Remove-Item -LiteralPath $taskEnvFile }
}
Write-Output 'Only experiment containers started. Model downloads and benchmarks are separate explicit commands.'
