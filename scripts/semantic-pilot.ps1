param([ValidateSet('start','stop')][string]$Action='start', [switch]$RecreateServices)
$ErrorActionPreference='Stop'
$taskRoot=(Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$taskContainers=@('hackathon-semantic-api-fts','hackathon-semantic-api-semantic','hackathon-semantic-api-hybrid','hackathon-semantic-ai','hackathon-semantic-pg')
if($Action -eq 'stop') { & docker stop @taskContainers; exit $LASTEXITCODE }
$taskEnv=Join-Path $taskRoot '.local-integration/semantic-private.env'
New-Item -ItemType Directory -Force (Split-Path $taskEnv) | Out-Null
function Start-PilotContainer($Name,$DockerArgs) {
    if($Name -notin $taskContainers) { throw 'Unknown pilot container' }
    $null=& docker container inspect $Name 2>$null
    $taskExists=$LASTEXITCODE -eq 0
    if($taskExists -and $RecreateServices -and $Name -ne 'hackathon-semantic-pg') {
        & docker stop $Name | Out-Null
        if($LASTEXITCODE -ne 0){throw "Cannot stop $Name"}
        & docker rm $Name | Out-Null # Stateless pilot service only, no -v.
        if($LASTEXITCODE -ne 0){throw "Cannot recreate $Name"}
        $taskExists=$false
    }
    if($taskExists) { & docker start $Name | Out-Null }
    else { & docker run --name $Name @DockerArgs | Out-Null }
    if($LASTEXITCODE -ne 0) { throw "Cannot start $Name" }
}
try {
    $taskSource=(& docker inspect hackathon-ai-service-1 | ConvertFrom-Json)[0]
    if($LASTEXITCODE -ne 0) { throw 'Need existing authorized Qwen configuration' }
    $taskPrivate=@($taskSource.Config.Env | Where-Object { $_ -match '^(LLM_|AI_SERVICE_TOKEN=)' })
    $taskToken=($taskPrivate | Where-Object { $_.StartsWith('AI_SERVICE_TOKEN=') }).Substring(17)
    if(-not $taskToken) { throw 'Missing service token' }
    $null=& docker network inspect hackathon-semantic-pilot 2>$null
    if($LASTEXITCODE -ne 0) { & docker network create hackathon-semantic-pilot | Out-Null }
    Start-PilotContainer 'hackathon-semantic-pg' @('--network','hackathon-semantic-pilot','--memory','1g','-e','POSTGRES_USER=search','-e','POSTGRES_PASSWORD=semantic-test-only','-e','POSTGRES_DB=semantic_test','-p','127.0.0.1:55438:5432','-v','hackathon_semantic_pilot_pg:/var/lib/postgresql/data','-d','pgvector/pgvector:pg17-bookworm')
    [IO.File]::WriteAllLines($taskEnv,[string[]]$taskPrivate)
    $taskModel=Join-Path $taskRoot '.local-integration/e5-small'
    if(-not (Test-Path -LiteralPath (Join-Path $taskModel 'onnx/model.onnx'))) { throw 'Provision local pinned model first; no automatic downloads' }
    Start-PilotContainer 'hackathon-semantic-ai' @('--network','hackathon-semantic-pilot','--memory','4g','--cpus','4','--env-file',$taskEnv,'-e','EMBEDDING_MODEL_PATH=/models/e5-small','-v',"${taskModel}:/models/e5-small:ro",'-p','127.0.0.1:18094:8000','-d','hackathon-ai-service:latest')
    for($taskAttempt=0;$taskAttempt -lt 30;$taskAttempt++) {
        & docker exec hackathon-semantic-pg pg_isready -U search -d semantic_test *> $null
        if($LASTEXITCODE -eq 0){break};Start-Sleep -Milliseconds 300
    }
    $taskPort=18091
    foreach($taskMode in @('fts','semantic','hybrid')) {
        $taskEnabled=if($taskMode -eq 'fts'){'false'}else{'true'}
        $taskFusion=if($taskMode -eq 'hybrid'){'true'}else{'false'}
        $taskSettings=@('DATABASE_URL=postgres://search:semantic-test-only@hackathon-semantic-pg:5432/semantic_test?sslmode=disable',
            'ADMIN_PASSWORD=semantic-pilot-test-only-123', 'AI_SERVICE_URL=http://hackathon-semantic-ai:8000',
            ('AI_SERVICE_TOKEN='+$taskToken),('ALLOWED_ORIGINS=http://127.0.0.1:'+$taskPort),
            'RAG_ENABLED=true','INGESTION_ENABLED=true','INGESTION_POLL_INTERVAL=100ms',
            ('SEMANTIC_SEARCH_ENABLED='+$taskEnabled),('SEMANTIC_SUPPLEMENT_FTS='+$taskFusion))
        [IO.File]::WriteAllLines($taskEnv,[string[]]$taskSettings)
        Start-PilotContainer ('hackathon-semantic-api-'+$taskMode) @('--network','hackathon-semantic-pilot','--memory','512m','--env-file',$taskEnv,'-v','hackathon_semantic_pilot_docs:/app/data/documents','-p',"127.0.0.1:${taskPort}:8080",'-d','hackathon-api:latest')
        $taskPort++
    }
} finally { if(Test-Path -LiteralPath $taskEnv){Remove-Item -LiteralPath $taskEnv} }
Write-Output 'Pilot started on loopback 18091 (FTS), 18092 (semantic), 18093 (semantic+FTS), 18094 (private AI). Existing application untouched. Volumes retained.'
