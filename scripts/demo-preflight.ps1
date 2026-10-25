#requires -Version 5.1
<#
Read-only readiness checks. Login/logout creates and revokes only a test session.
No startup, migration, upload, ACL mutation, document edit, or volume operation.
Exit 0 = all PASS; 1 = at least one FAIL. Requires Docker Compose, no host Python.
#>
[CmdletBinding()]
param(
    [string]$BaseUrl = 'http://localhost:8080',
    [string]$ManifestPath = '',
    [PSCredential]$Credential,
    [string]$JsonReport = ''
)
$ErrorActionPreference = 'Stop'
if (([uri]$BaseUrl).Host -notin @('localhost','127.0.0.1','[::1]','::1')) { throw 'This preflight is limited to the local demo host.' }
$previousOutputEncoding = $OutputEncoding
$OutputEncoding = New-Object Text.UTF8Encoding($false)
$root = Split-Path -Parent $PSScriptRoot
if (-not $ManifestPath) { $ManifestPath = Join-Path $root '.local-integration/final-demo/manifest.json' }
$results = [System.Collections.Generic.List[object]]::new()
$watch = [Diagnostics.Stopwatch]::StartNew()
$session = $null
$csrf = $null
$manifest = $null
$embedding = $null

function Check([string]$Name, [scriptblock]$Action, [string]$Fix) {
    $timer = [Diagnostics.Stopwatch]::StartNew()
    try {
        $detail = & $Action
        $row = [pscustomobject]@{ name=$Name; status='PASS'; detail=([string]$detail); ms=$timer.ElapsedMilliseconds; fix='' }
        Write-Host ("PASS {0}: {1}" -f $Name, $detail) -ForegroundColor Green
    } catch {
        # Exception messages can contain request bodies, paths, connection strings.
        $row = [pscustomobject]@{ name=$Name; status='FAIL'; detail='Check failed'; ms=$timer.ElapsedMilliseconds; fix=$Fix }
        Write-Host ("FAIL {0}. Fix: {1}" -f $Name, $Fix) -ForegroundColor Red
    }
    $results.Add($row)
}
function Compose([string[]]$Arguments, [string]$InputText = '') {
    if ($InputText) { $output = $InputText | & docker compose @Arguments 2>$null }
    else { $output = & docker compose @Arguments 2>$null }
    if ($LASTEXITCODE -ne 0) { throw 'Docker probe failed' }
    return ($output -join "`n")
}
function Sql([string]$Statement) {
    $raw = Compose -Arguments @('exec','-T','postgres','sh','-c',
        'psql -X -q -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At') -InputText ("BEGIN READ ONLY;`n"+$Statement+"`nCOMMIT;")
    return ($raw | ConvertFrom-Json)
}
function Api([string]$Path, [string]$Method='GET', $Body=$null) {
    $args = @{ Uri=($BaseUrl.TrimEnd('/')+$Path); Method=$Method; WebSession=$script:session; TimeoutSec=55; ErrorAction='Stop' }
    if ($Method -ne 'GET') {
        $args.Headers = @{ Origin=$BaseUrl.TrimEnd('/'); 'X-CSRF-Token'=$script:csrf }
        $args.ContentType='application/json'
        $args.Body = [Text.Encoding]::UTF8.GetBytes(($Body | ConvertTo-Json -Depth 20 -Compress))
    }
    Invoke-RestMethod @args
}
Push-Location $root
try {
    Check 'Compose containers' {
        $rows = @(Compose -Arguments @('ps','--format','json') | ForEach-Object {
            foreach ($line in ($_ -split "`n")) { if ($line.Trim()) { $line | ConvertFrom-Json } }
        })
        foreach ($name in @('postgres','api','ai-service','frontend')) {
            $found = @($rows | Where-Object Service -eq $name)
            if ($found.Count -ne 1 -or $found[0].State -ne 'running' -or $found[0].Health -ne 'healthy') { throw 'Unhealthy service' }
        }
        '4/4 running and healthy'
    } 'Open Docker Desktop; inspect docker compose ps and logs. Use existing start-local.ps1; preserve named volumes.'
    Check 'API and frontend' {
        $health = Invoke-RestMethod ($BaseUrl.TrimEnd('/')+'/healthz') -TimeoutSec 8
        $web = Invoke-WebRequest ($BaseUrl.TrimEnd('/')+'/login') -UseBasicParsing -TimeoutSec 8
        if ($web.StatusCode -ne 200 -or $web.Content -notmatch '<div id="root">') { throw 'Frontend unavailable' }
        'API healthz and React shell respond'
    } 'Check frontend port WEB_PORT, API logs and Nginx proxy; supply -BaseUrl with the configured origin.'
    Check 'PostgreSQL 17 and pgvector' {
        $db = Sql "SELECT json_build_object('major',current_setting('server_version_num')::int/10000,'vector',(SELECT extversion FROM pg_extension WHERE extname='vector'));"
        if ($db.major -ne 17 -or -not $db.vector) { throw 'Database mismatch' }
        "PG17; pgvector $($db.vector)"
    } 'Follow docs/semantic-search-main.md for verified PG17 restore. Never initialize over an existing volume.'
    Check 'Required database tables' {
        $tables = @('users','sessions','documents','document_versions','document_access','ingestion_jobs','document_chunks','chat_sessions','chat_messages','chat_message_sources','chat_message_dependencies','semantic_indexes','semantic_chunks','embedding_windows','document_templates','template_drafts','template_generated_files')
        $db = Sql "SELECT json_build_object('tables',(SELECT json_agg(tablename) FROM pg_tables WHERE schemaname='public'));"
        if (@($tables | Where-Object { $_ -notin $db.tables }).Count) { throw 'Missing tables' }
        "$($tables.Count) required tables exist"
    } 'Inspect migration status and API startup logs. Do not edit migrations 00001-00011 or recreate the database.'
    Check 'Named persistent volumes' {
        foreach ($service in @('postgres','api','ai-service')) {
            $id = Compose -Arguments @('ps','-q',$service)
            $info = (& docker inspect $id 2>$null | ConvertFrom-Json)[0]
            $destination = @{postgres='/var/lib/postgresql/data';api='/app/data/documents';'ai-service'='/models'}[$service]
            $mount = @($info.Mounts | Where-Object Destination -eq $destination)
            if ($mount.Count -ne 1 -or $mount[0].Type -ne 'volume') { throw 'Missing volume' }
            if ($service -eq 'postgres') {
                $config = Compose -Arguments @('config','--format','json') | ConvertFrom-Json
                if ($mount[0].Name -ne $config.volumes.postgres_data.name) { throw 'Wrong database volume' }
            }
        }
        'Database, documents and models use named volumes; PG target matches Compose'
    } 'Check POSTGRES_DATA_VOLUME and existing mount identities before starting services; never delete or replace volumes.'
    Check 'AI service health' {
        $null = Compose -Arguments @('exec','-T','ai-service','python','-c',"import urllib.request; urllib.request.urlopen('http://127.0.0.1:8000/health',timeout=5).close()")
        'Python health responds'
    } 'Inspect docker compose logs ai-service and its existing configuration.'
    Check 'Enabled demo services' {
        $id = Compose -Arguments @('ps','-q','api')
        $info = (& docker inspect $id 2>$null | ConvertFrom-Json)[0]
        foreach ($setting in @('RAG_ENABLED=true','SEMANTIC_SEARCH_ENABLED=true','SEMANTIC_INDEXING_ENABLED=true','INGESTION_ENABLED=true')) {
            if ($setting -notin $info.Config.Env) { throw 'Demo setting disabled' }
        }
        'RAG, semantic search/indexing and ingestion enabled'
    } 'Review .env and existing service configuration; use the verified local startup procedure without replacing volumes.'
    Check 'E5 model control inference' {
        $probe = Get-Content (Join-Path $PSScriptRoot 'demo-ai-probe.py') -Raw
        $script:embedding = Compose -Arguments @('exec','-T','ai-service','python','-','embedding') -InputText $probe | ConvertFrom-Json
        if (-not $script:embedding.ok) { throw 'Model unavailable' }
        "384 finite normalized values; $($script:embedding.ms) ms"
    } 'Check /models/e5-small in embedding_models and docs/semantic-search.md. Wait for a busy embedding worker, then retry.'
    Check 'Separate Qwen generation' {
        $probe = Get-Content (Join-Path $PSScriptRoot 'demo-ai-probe.py') -Raw
        $q = Compose -Arguments @('exec','-T','ai-service','python','-','qwen') -InputText $probe | ConvertFrom-Json
        if (-not $q.ok) { throw 'Qwen unavailable' }
        "$($q.model) returned valid control JSON; $($q.ms) ms"
    } 'Check the separate Qwen inference host, network, LLM_BASE_URL/MODEL and remote-access setting; do not expose API keys.'
    Check 'Demo manifest' {
        $script:manifest = Get-Content -LiteralPath $ManifestPath -Raw -Encoding UTF8 | ConvertFrom-Json
        if (@($script:manifest.documents.PSObject.Properties).Count -lt 13 -or @($script:manifest.templates.PSObject.Properties).Count -ne 7) { throw 'Incomplete demo' }
        '13 demo documents and seven modules recorded'
    } 'Run ai-service/.venv/Scripts/python.exe scripts/demo-fixtures.py setup once; reruns reuse fixtures.'
    Check 'Demo semantic indexes' {
        if (-not $script:manifest -or -not $script:embedding) { throw 'Missing prerequisites' }
        $ids = @($script:manifest.documents.PSObject.Properties | ForEach-Object { "'$([guid]$_.Value.id)'" }) -join ','
        $e = $script:embedding
        foreach ($value in @($e.model,$e.revision,$e.format_revision)) { if ($value -notmatch '^[a-zA-Z0-9_./:-]+$') { throw 'Invalid identity' } }
        $db = Sql ("SELECT json_build_object('ready',count(*)) FROM documents d JOIN document_versions v ON v.id=d.active_version_id WHERE d.id IN ($ids) AND v.status='ready' AND EXISTS (SELECT 1 FROM semantic_indexes s WHERE s.document_version_id=v.id AND s.status='ready' AND s.model='$($e.model)' AND s.revision='$($e.revision)' AND s.format_revision='$($e.format_revision)' AND s.dimensions=384 AND EXISTS(SELECT 1 FROM embedding_windows w WHERE w.index_id=s.id));")
        if ($db.ready -ne @($script:manifest.documents.PSObject.Properties).Count) { throw 'Index not ready' }
        "$($db.ready) active versions indexed with the loaded model identity"
    } 'Wait for ingestion/semantic worker; inspect failed ingestion_jobs and semantic_indexes read-only. Do not reupload duplicates.'
    Check 'File storage' {
        $null = Compose -Arguments @('exec','-T','api','sh','-c','test -d /app/data/documents && test -r /app/data/documents && test -w /app/data/documents && test -x /app/data/documents')
        'Storage directory is readable, writable and traversable; no file written'
    } 'Check documents_data mount, disk space and ownership; preserve existing files.'
    Check 'Login and least-privilege demo user' {
        if (-not $script:manifest) { throw 'Manifest missing' }
        if ($Credential) { $username=$Credential.UserName; $password=$Credential.GetNetworkCredential().Password }
        else {
            $secret = Get-Content (Join-Path $root '.local-integration/final-demo/credentials.json') -Raw -Encoding UTF8 | ConvertFrom-Json
            $username=$secret.demo.username; $password=$secret.demo.password
        }
        $script:session = New-Object Microsoft.PowerShell.Commands.WebRequestSession
        $body = @{username=$username;password=$password} | ConvertTo-Json -Compress
        $null = Invoke-RestMethod ($BaseUrl.TrimEnd('/')+'/api/v1/auth/login') -Method POST -WebSession $script:session -Headers @{Origin=$BaseUrl.TrimEnd('/')} -ContentType application/json -Body ([Text.Encoding]::UTF8.GetBytes($body)) -TimeoutSec 12
        $password=$null; $body=$null
        $script:csrf=$script:session.Cookies.GetCookies([uri]$BaseUrl)['ai_csrf'].Value
        $me=Api '/api/v1/auth/me'
        if ($me.id -ne $script:manifest.demo.id -or $me.role -ne 'member' -or -not $script:csrf) { throw 'Wrong user' }
        'Synthetic member login and CSRF cookie verified'
    } 'Use the local saved demo credential or -Credential (Get-Credential). Check account activity; do not reset existing users.'
    Check 'Document ACL and original downloads' {
        if (-not $script:session -or -not $script:manifest) { throw 'Missing session' }
        $docs=Api '/api/v1/documents?limit=100&offset=0'
        $expected=@($script:manifest.documents.PSObject.Properties | ForEach-Object { $_.Value.id })
        if ($docs.total -ne $expected.Count -or @($docs.items | Where-Object { $_.id -notin $expected }).Count) { throw 'Unexpected ACL scope' }
        foreach ($prop in $script:manifest.documents.PSObject.Properties) {
            $d=$prop.Value
            $response=Invoke-WebRequest ($BaseUrl.TrimEnd('/')+"/api/v1/documents/$($d.id)/versions/$($d.version_id)/download") -WebSession $script:session -UseBasicParsing -TimeoutSec 20
            $sha=[Security.Cryptography.SHA256]::Create()
            try { $hash=([BitConverter]::ToString($sha.ComputeHash($response.RawContentStream.ToArray()))).Replace('-','').ToLowerInvariant() } finally { $sha.Dispose() }
            if ($hash -ne $d.sha256) { throw 'Source checksum changed' }
        }
        'Exactly the demo ACL scope; 13 original files match SHA256'
    } 'Inspect this synthetic user ACL via admin API; restore only intended demo grants. Check storage and source hashes.'
    Check 'Accessible v2 catalog and reserve drafts' {
        if (-not $script:session -or -not $script:manifest) { throw 'Missing session' }
        $catalog=Api '/api/v1/templates?limit=100&offset=0'
        foreach ($prop in $script:manifest.templates.PSObject.Properties) {
            $match=@($catalog.items | Where-Object { $_.id -eq $prop.Value.id -and $_.schema_version -eq 2 -and $_.enabled })
            if ($match.Count -ne 1) { throw 'Module missing' }
        }
        foreach ($prop in $script:manifest.drafts.PSObject.Properties) {
            $draft=Api ('/api/v1/template-drafts/'+$prop.Value)
            if ($draft.owner_id -ne $script:manifest.demo.id -or -not $draft.validation.ready) { throw 'Reserve draft incomplete' }
        }
        'Seven v2 modules accessible; two reserve drafts ready'
    } 'Check template enabled state, source ACL and profile integrity. Resume the saved reserve draft through React.'
    Check 'CSRF-protected search' {
        $search=Api '/api/v1/search' 'POST' @{query='Кто согласует заявку на доступ к учебному архиву?';limit=10}
        $id=$script:manifest.documents.archive.id
        if (-not @($search.results | Where-Object document_id -eq $id).Count) { throw 'Demo evidence missing' }
        'Authenticated search returns the synthetic archive source'
    } 'Inspect active version/index readiness and ACL. Use explicit subject in the question.'
} finally {
    if ($session -and $csrf) {
        try { $null=Api '/api/v1/auth/logout' 'POST' @{} } catch { Write-Host 'FAIL Logout: close the synthetic session before the demo.'; $results.Add([pscustomobject]@{name='Logout';status='FAIL';detail='Session cleanup failed';ms=0;fix='Log out the synthetic account in the app.'}) }
    }
    Pop-Location
    $OutputEncoding = $previousOutputEncoding
}
$failed=@($results | Where-Object status -eq 'FAIL').Count
$report=[pscustomobject]@{checked_at=[DateTimeOffset]::UtcNow.ToString('o');duration_ms=$watch.ElapsedMilliseconds;passed=$results.Count-$failed;failed=$failed;checks=@($results)}
if ($JsonReport) { $report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $JsonReport -Encoding UTF8 }
Write-Host ("Preflight: {0} PASS / {1} FAIL in {2:N1}s" -f $report.passed,$failed,($watch.ElapsedMilliseconds/1000))
if ($failed) { exit 1 }
exit 0
