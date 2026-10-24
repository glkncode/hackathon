package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"ai-assistant/internal/service"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
)

func TestSearchVariantsRejectsUnboundedOrInvalidPlan(t *testing.T) {
	db, _ := mockDB(t)
	repo := NewSearchRepository(db)
	for _, queries := range [][]domain.SearchQuery{
		nil,
		{{Query: "a"}, {Query: "b"}, {Query: "c"}},
		{{Query: "valid"}, {Query: ""}},
		{{Query: "valid", Limit: domain.MaxSearchLimit + 1}},
	} {
		if _, err := repo.SearchVariants(context.Background(), uuid.New(), domain.RoleMember, queries); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatal("invalid plan reached SQL", err)
		}
	}
	if _, err := repo.SearchVariants(context.Background(), uuid.Nil, domain.RoleMember, []domain.SearchQuery{{Query: "a"}}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("invalid actor reached plan transaction", err)
	}
}

func TestSearchVariantsRollbackDiscardsPartialSources(t *testing.T) {
	db, mock := mockDB(t)
	actor := uuid.New()
	mock.ExpectBegin()
	mock.ExpectQuery(searchSQL).WithArgs(actor, domain.RoleMember, "инженер", 10).
		WillReturnRows(sqlmock.NewRows([]string{"chunk_id", "document_id", "version_id", "title", "number", "index", "content", "page", "section", "section_title", "score"}).
			AddRow(uuid.NewString(), uuid.NewString(), uuid.NewString(), "Регламент", 1, 0, "Инженер проверяет оборудование", nil, nil, nil, 1.1))
	want := errors.New("private remote error")
	mock.ExpectQuery(searchSQL).WithArgs(actor, domain.RoleMember, "оборудование", 10).WillReturnError(want)
	mock.ExpectRollback()
	got, err := NewSearchRepository(db).SearchVariants(context.Background(), actor, domain.RoleMember, []domain.SearchQuery{{Query: "инженер"}, {Query: "оборудование"}})
	if got != nil || !errors.Is(err, want) || strings.Contains(err.Error(), "private") {
		t.Fatal("partial sources or raw error escaped failed plan", got, err)
	}
}

func TestIntegrationSearchPlanGeneralRolesAndACL(t *testing.T) {
	f := newSearchFixture(t)
	chunks := []domain.DocumentChunk{
		{Content: "Инженер\t__________"},
		{ChunkIndex: 1, Content: "Инженер проверяет оборудование и составляет отчёты."},
		{ChunkIndex: 2, Content: "Диспетчер\tКонтролирует движение транспорта."},
	}
	doc, version := f.document(t, "Регламент", domain.VersionReady, chunks)
	f.grant(t, doc.ID, f.memberA.ID)
	f.document(t, "Закрытый регламент", domain.VersionReady, chunks)
	notReady, _ := f.document(t, "Обработка", domain.VersionProcessing, chunks)
	f.grant(t, notReady.ID, f.memberA.ID)
	search := service.NewSearchService(f.repo)
	for _, query := range []string{
		"Какие обязанности у инженера?", "Каковы функции инженера?",
		"Что должен делать диспетчер?", "Чем занимается диспетчер?",
	} {
		got, err := search.Search(f.ctx, f.memberA, domain.SearchQuery{Query: query, Limit: 1})
		wantIndex := 1
		if strings.Contains(query, "диспетчер") {
			wantIndex = 2
		}
		if err != nil || len(got) != 1 || got[0].ChunkIndex != wantIndex || got[0].DocumentID != doc.ID || got[0].DocumentVersionID != version.ID {
			t.Fatalf("general role/filled table or visibility failed for %q: %+v, %v", query, got, err)
		}
		denied, err := search.Search(f.ctx, f.memberB, domain.SearchQuery{Query: query})
		if err != nil || len(denied) != 0 {
			t.Fatal("plan bypassed SQL ACL", denied, err)
		}
	}
	for _, query := range []string{
		"Какая зарплата у инженера?", "Какие обязанности у инженера при пожаре?",
		"Какие обязанности у инженера Иванова?", "Что должен делать бухгалтер?",
	} {
		got, err := search.Search(f.ctx, f.memberA, domain.SearchQuery{Query: query})
		if err != nil || len(got) != 0 {
			t.Fatalf("query understanding dropped a topic/actor for %q: %+v %v", query, got, err)
		}
	}
	if err := f.acl.RevokeAccess(f.ctx, doc.ID, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	got, err := search.Search(f.ctx, f.memberA, domain.SearchQuery{Query: "Какие обязанности у инженера?"})
	if err != nil || len(got) != 0 {
		t.Fatal("plan ignored ACL revoke", got, err)
	}
}
