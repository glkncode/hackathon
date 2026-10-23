package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ai-assistant/internal/domain"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestSearchRepositoryErrorsDoNotExposeQuery(t *testing.T) {
	db, mock := mockDB(t)
	id := uuid.New()
	const privateQuery = "секретное согласование"
	mock.ExpectQuery(searchSQL).WithArgs(id, domain.RoleMember, privateQuery, 10).
		WillReturnError(&pgconn.PgError{Code: "XX000", Message: privateQuery, Detail: "private document content"})
	_, err := NewSearchRepository(db).Search(context.Background(), id, domain.RoleMember, domain.SearchQuery{Query: privateQuery})
	if err == nil || strings.Contains(err.Error(), privateQuery) || strings.Contains(err.Error(), "private document content") {
		t.Fatal("raw PostgreSQL error escaped the repository", err)
	}
}

func TestSearchRepositoryCancellationAndTimeout(t *testing.T) {
	for _, callerDeadline := range []bool{true, false} {
		t.Run(map[bool]string{true: "caller cancellation", false: "repository timeout"}[callerDeadline], func(t *testing.T) {
			db, mock := mockDB(t)
			id := uuid.New()
			mock.ExpectQuery(searchSQL).WithArgs(id, domain.RoleMember, "документ", 10).
				WillDelayFor(searchTimeout + time.Second).
				WillReturnRows(sqlmock.NewRows([]string{"unused"}))
			ctx := context.Background()
			if callerDeadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
			}
			start := time.Now()
			_, err := NewSearchRepository(db).Search(ctx, id, domain.RoleMember, domain.SearchQuery{Query: "документ"})
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > searchTimeout+time.Second {
				t.Fatal("database timeout/cancellation not enforced", err)
			}
		})
	}
}

func TestSearchRepositoryRejectsInvalidInputBeforeSQL(t *testing.T) {
	db, _ := mockDB(t)
	repo := NewSearchRepository(db)
	if _, err := repo.Search(context.Background(), uuid.Nil, domain.RoleAdmin, domain.SearchQuery{Query: "x"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("nil identity accepted")
	}
	if _, err := repo.Search(context.Background(), uuid.New(), "invalid", domain.SearchQuery{Query: "x"}); !errors.Is(err, domain.ErrAccessDenied) {
		t.Fatal("invalid role accepted")
	}
	if _, err := repo.Search(context.Background(), uuid.New(), domain.RoleMember, domain.SearchQuery{Query: "x", Limit: 31}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatal("invalid limit accepted")
	}
}

func TestSearchRepositoryDiscardsPartialResults(t *testing.T) {
	db, mock := mockDB(t)
	id := uuid.New()
	rows := sqlmock.NewRows([]string{"chunk_id", "document_id", "version_id", "title", "number", "index", "content", "page", "section", "section_title", "score"}).
		AddRow(uuid.NewString(), uuid.NewString(), uuid.NewString(), "Policy", 1, 0, "private content", nil, nil, nil, 0.1).
		AddRow(uuid.NewString(), uuid.NewString(), uuid.NewString(), "Policy", 1, 1, "private content", nil, nil, nil, 0.1).
		RowError(1, errors.New("read failed"))
	mock.ExpectQuery(searchSQL).WithArgs(id, domain.RoleMember, "документ", 10).WillReturnRows(rows)
	results, err := NewSearchRepository(db).Search(context.Background(), id, domain.RoleMember, domain.SearchQuery{Query: "документ"})
	if err == nil || results != nil {
		t.Fatal("returned incomplete sources after database failure")
	}
}
