# Main local Semantic Search verification

This run uses the actual React/nginx/Go entry point **http://localhost:8080**,
`hackathon-postgres-1` and `hackathon-ai-service-1`. It never calls pilot endpoints.
The frozen new corpus has six new subjects (12 positive, six negative questions).
Seven uploaded fixture documents plus the original working-group DOCX make eight
simultaneously accessible documents. The seventh upload retains the unchanged
`f03` evidence-gate false negative separately from the independent score.

Actual execution order: `prepare`, `fts`, `activate`, `semantic`, `security`,
`persistence`, `regression`, `summary`. Use the local Python virtual environment
with httpx/numpy/python-docx/PyMuPDF dependencies. Preparation creates the explicit
`semantic-main-check` local fixture member and seven documents. It refuses to
overwrite a frozen manifest. This is an opt-in maintenance/evaluation tool that
changes the main local app; do not run against another environment or blindly
repeat the preparation/version tests.

`activate` checks the pre-activation restore/backfill evidence and complete index
coverage before changing the local search flag. Off/on use the same frozen
questions, normal session cookies and CSRF, Search and Chat APIs. Exact observation
files, original-source text, IDs and explicit agent claim reviews stay private:
`.local-integration/main-semantic-20261024/e2e/`. Agent reviews are not independent
human review. UI screenshots are actual React responses, not mockups.

See [the main activation report](../../docs/semantic-search-main.md) for results,
backup locations, image/volume identities and rollback. No scoring thresholds or
query-specific retrieval rules were added for these fixtures.
