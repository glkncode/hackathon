# Local Go Python PostgreSQL stack

Docker Desktop must have a running Linux engine using WSL 2.
From the repository root in PowerShell:

```powershell
.\scripts\start-local.ps1
```

The script uses the existing ai-service/.venv (python-dotenv required), copies
AI_SERVICE_TOKEN and inference settings from ai-service/.env, generates local
database/admin passwords once and starts compose.yaml. Secrets are stored only in
the ignored .local-integration directory, never in images or committed configs.
An existing AI_SERVICE_TOKEN is preserved. Keep credentials.json and docker.env
private; do not paste full Docker inspect/config output containing environment values.

Services:

| Service | Address |
| --- | --- |
| Go HTTP API | http://127.0.0.1:8080/api/v1 |
| Python Swagger | http://127.0.0.1:8000/docs |
| PostgreSQL | Internal Docker network only |
| Inference | External approved server configured in ai-service/.env |

Only 127.0.0.1 ports are published. Inside the container Uvicorn listens on
0.0.0.0 so Go can reach it at http://python:8000; the host port remains bound to
loopback. A previously running local Uvicorn must be stopped before using port 8000.
No frontend container is included.

The local database is ingestion_local_test. Startup applies Go migrations and
creates integration_admin on an empty database. The password is in
.local-integration/credentials.json. Existing accounts are not overwritten.
RAG and ingestion are enabled. No inference model is downloaded.

Docker volumes postgres-data and document-data preserve the database and original
files between restarts. restart:unless-stopped restarts the containers with Docker.
Do not run compose down -v unless you intend to delete the local database/originals.
This is a development configuration, not a production deployment.

If Docker is not on PATH, use its full executable path:

```powershell
$dockerExecutable = "$env:LOCALAPPDATA\Programs\DockerDesktop\resources\bin\docker.exe"
& $dockerExecutable compose --env-file .local-integration/docker.env ps
& $dockerExecutable compose --env-file .local-integration/docker.env logs --tail 50 go python
& $dockerExecutable compose --env-file .local-integration/docker.env stop
```

Re-run start-local.ps1 after changing inference settings; it refreshes Docker's
environment and recreates affected containers while preserving volumes.
