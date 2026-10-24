package postgres

import (
	"ai-assistant/internal/domain"
	"context"
	"errors"
	"github.com/google/uuid"
	"math"
	"testing"
	"time"
)

func TestIntegrationSemanticLifecycleACLAndVersions(t *testing.T) {
	f := newSearchFixture(t)
	d, v := f.document(t, "Открытый", domain.VersionReady, []domain.DocumentChunk{{Content: "Датчик проверяют раз в полгода."}, {ChunkIndex: 1, Content: "Запись сохраняют в журнале."}})
	_, secret := f.document(t, "Закрытый", domain.VersionReady, []domain.DocumentChunk{{Content: "Секретные условия."}})
	f.grant(t, d.ID, f.memberA.ID)
	repo := NewEmbeddingRepository(f.db, domain.E5Identity())
	search := NewSemanticSearchRepository(f.db)
	vector := make([]float64, 384)
	vector[0] = 1
	windows := []domain.EmbeddingWindow{{StartToken: 0, EndToken: 10, TotalTokens: 10}}
	if err := repo.EnqueueReady(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := repo.EnqueueReady(f.ctx); err != nil {
		t.Fatal(err)
	}
	jobs := map[uuid.UUID]domain.SemanticJob{}
	for i := 0; i < 2; i++ {
		j, err := repo.Claim(f.ctx, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		jobs[j.VersionID] = j
	}
	j := jobs[v.ID]
	if err := repo.Complete(f.ctx, j); err == nil {
		t.Fatal("incomplete index ready")
	}
	chunks, err := f.chunks.GetChunksByVersion(f.ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveChunk(f.ctx, j, chunks[0].ID, windows, [][]float64{vector}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveChunk(f.ctx, j, chunks[0].ID, windows, [][]float64{vector}, time.Minute); err != nil {
		t.Fatal("idempotency", err)
	}
	state, err := repo.State(f.ctx, v.ID)
	if err != nil || state.CompletedChunks != 1 || state.TotalChunks != 2 || state.Status != "processing" {
		t.Fatal(state, err)
	}
	stale := j
	stale.LeaseToken = uuid.New()
	if err = repo.SaveChunk(f.ctx, stale, chunks[1].ID, windows, [][]float64{vector}, time.Minute); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatal("lease fencing", err)
	}
	if err = repo.SaveChunk(f.ctx, j, chunks[1].ID, windows, [][]float64{vector}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(f.ctx, j); err != nil {
		t.Fatal(err)
	}
	// A chunk from another version must never be accepted by the composite FK.
	secretChunks, _ := f.chunks.GetChunksByVersion(f.ctx, secret.ID)
	sj := jobs[secret.ID]
	if err = repo.SaveChunk(f.ctx, sj, chunks[0].ID, windows, [][]float64{vector}, time.Minute); err == nil {
		t.Fatal("cross-version chunk accepted")
	}
	if err = repo.SaveChunk(f.ctx, sj, secretChunks[0].ID, windows, [][]float64{vector}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = repo.Complete(f.ctx, sj); err != nil {
		t.Fatal(err)
	}
	find := func(actor domain.User) []domain.SearchResult {
		t.Helper()
		out, err := search.SearchSemantic(f.ctx, actor.ID, actor.Role, vector, domain.E5Identity(), 10)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if len(find(f.admin)) != 3 || len(find(f.memberA)) != 2 || len(find(f.memberB)) != 0 {
		t.Fatal("SQL ACL")
	}
	filtered, err := search.FilterVisible(f.ctx, f.memberA.ID, f.memberA.Role, find(f.admin))
	if err != nil || len(filtered) != 2 {
		t.Fatal("visibility recheck", filtered, err)
	}
	// Stored incompatible revisions must be excluded, even when query identity is valid.
	if _, err = f.db.ExecContext(f.ctx, `UPDATE semantic_indexes SET revision='other' WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = search.SearchSemantic(f.ctx, f.memberA.ID, f.memberA.Role, vector, domain.E5Identity(), 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("stored incompatible revision exposed", err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE semantic_indexes SET revision=$2 WHERE id=$1`, j.ID, domain.EmbeddingRevision); err != nil {
		t.Fatal(err)
	}
	if len(find(domain.User{ID: f.memberA.ID, Role: domain.RoleAdmin})) != 0 {
		t.Fatal("stale role bypassed SQL ACL")
	}
	// SQL checks current role and activation, not stale authenticated role.
	if _, err = f.db.ExecContext(f.ctx, `UPDATE users SET is_active=false WHERE id=$1`, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	if len(find(f.memberA)) != 0 {
		t.Fatal("inactive user leaked")
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE users SET is_active=true WHERE id=$1`, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	wrong := domain.E5Identity()
	wrong.Revision = "incompatible"
	if _, err = search.SearchSemantic(f.ctx, f.admin.ID, f.admin.Role, vector, wrong, 10); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("revision", err)
	}
	next, err := f.docs.CreateVersion(f.ctx, d.ID, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "v2.txt", MIMEType: "text/plain", SizeBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	f.seedVersion(t, next, domain.VersionReady, []domain.DocumentChunk{{Content: "Новое условие."}})
	if err = f.docs.SetActiveVersion(f.ctx, d.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = search.SearchSemantic(f.ctx, f.memberA.ID, f.memberA.Role, vector, domain.E5Identity(), 10); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("new version must trigger FTS until indexed", err)
	}
	var old int
	if err = f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM embedding_windows WHERE document_version_id=$1`, v.ID).Scan(&old); err != nil || old != 2 {
		t.Fatal("history lost")
	}
	if err = repo.Reindex(f.ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveChunk(f.ctx, j, chunks[0].ID, windows, [][]float64{vector}, time.Minute); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatal("reindex did not fence old worker", err)
	}
	canceled, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err = search.SearchSemantic(canceled, f.admin.ID, f.admin.Role, vector, domain.E5Identity(), 10); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestIntegrationSemanticWindowAggregationAndAtomicity(t *testing.T) {
	f := newSearchFixture(t)
	d, v := f.document(t, "Окна", domain.VersionReady, []domain.DocumentChunk{{Content: "Длинный источник."}, {ChunkIndex: 1, Content: "Другой источник."}})
	f.grant(t, d.ID, f.memberA.ID)
	r := NewEmbeddingRepository(f.db, domain.E5Identity())
	if err := r.EnqueueReady(f.ctx); err != nil {
		t.Fatal(err)
	}
	j, err := r.Claim(f.ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := f.chunks.GetChunksByVersion(f.ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	query := make([]float64, 384)
	query[0] = 1
	negative := make([]float64, 384)
	negative[0] = -1
	other := make([]float64, 384)
	other[0] = .5
	other[1] = math.Sqrt(.75)
	windows := []domain.EmbeddingWindow{{WindowIndex: 0, StartToken: 0, EndToken: 508, TotalTokens: 600}, {WindowIndex: 1, StartToken: 444, EndToken: 600, TotalTokens: 600}}
	if err = r.SaveChunk(f.ctx, j, chunks[0].ID, windows, [][]float64{negative, {1}}, time.Minute); err == nil {
		t.Fatal("invalid tail vector accepted")
	}
	state, err := r.State(f.ctx, v.ID)
	if err != nil || state.CompletedChunks != 0 {
		t.Fatal("partial invalid chunk persisted", err)
	}
	if err = r.SaveChunk(f.ctx, j, chunks[0].ID, windows, [][]float64{negative, query}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = r.SaveChunk(f.ctx, j, chunks[1].ID, []domain.EmbeddingWindow{{EndToken: 10, TotalTokens: 10}}, [][]float64{other}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = r.Complete(f.ctx, j); err != nil {
		t.Fatal(err)
	}
	search := NewSemanticSearchRepository(f.db)
	results, err := search.SearchSemantic(f.ctx, f.memberA.ID, f.memberA.Role, query, domain.E5Identity(), 1)
	if err != nil || len(results) != 1 || results[0].ChunkID != chunks[0].ID || math.Abs(results[0].Score-1) > .00001 {
		t.Fatal("max-window source aggregation failed", results, err)
	}
	antipodal := make([]float64, 384)
	antipodal[0] = -.5
	antipodal[1] = -math.Sqrt(.75)
	results, err = search.SearchSemantic(f.ctx, f.memberA.ID, f.memberA.Role, antipodal, domain.E5Identity(), 10)
	if err != nil || len(results) != 2 || results[1].ChunkID != chunks[1].ID || results[1].Score != 0 {
		t.Fatal("negative cosine broke legacy citation score contract", results, err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE document_versions SET status='failed' WHERE id=$1`, v.ID); err != nil {
		t.Fatal(err)
	}
	results, err = search.SearchSemantic(f.ctx, f.memberA.ID, f.memberA.Role, query, domain.E5Identity(), 10)
	if err != nil || len(results) != 0 {
		t.Fatal("failed version exposed", results, err)
	}
}

func TestIntegrationSemanticLeaseRecovery(t *testing.T) {
	f := newSearchFixture(t)
	_, v := f.document(t, "Очередь", domain.VersionReady, []domain.DocumentChunk{{Content: "Порядок работы."}})
	repo := NewEmbeddingRepository(f.db, domain.E5Identity())
	if err := repo.EnqueueReady(f.ctx); err != nil {
		t.Fatal(err)
	}
	a, err := repo.Claim(f.ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Claim(f.ctx, time.Minute); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("duplicate claim")
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE semantic_indexes SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	b, err := repo.Claim(f.ctx, time.Minute)
	if err != nil || a.LeaseToken == b.LeaseToken || b.VersionID != v.ID {
		t.Fatal("recovery", err)
	}
	if err = repo.Retry(f.ctx, a, "old", time.Second); !errors.Is(err, domain.ErrLeaseLost) {
		t.Fatal("stale retry")
	}
	if err = repo.Retry(f.ctx, b, "embedding_unavailable", time.Second); err != nil {
		t.Fatal(err)
	}
	state, err := repo.State(f.ctx, v.ID)
	if err != nil || state.Status != "pending" || state.Attempts != 2 {
		t.Fatal(state, err)
	}
}
