package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	"ai-assistant/internal/domain"
	"github.com/google/uuid"
)

const dutiesQuestion = "Какие обязанности у руководителя рабочей группы?"
const dutiesContent = "3. Руководителю Рабочей группы организовать работу, распределить задачи и обеспечить соблюдение установленного срока."

func TestIntegrationRelaxedSearchACLAndVersions(t *testing.T) {
	f := newSearchFixture(t)
	chunks := []domain.DocumentChunk{{Content: dutiesContent}}
	allowed, active := f.document(t, "Рабочая группа", domain.VersionReady, chunks)
	f.document(t, "Закрытая рабочая группа", domain.VersionReady, chunks)
	for _, status := range []domain.VersionStatus{domain.VersionUploaded, domain.VersionProcessing, domain.VersionFailed} {
		d, _ := f.document(t, "Не готов", status, chunks)
		f.grant(t, d.ID, f.memberA.ID)
	}
	noActive, _ := f.document(t, "Не опубликован", domain.VersionReady, chunks)
	f.grant(t, noActive.ID, f.memberA.ID)
	if _, err := f.db.ExecContext(f.ctx, `UPDATE documents SET active_version_id=NULL WHERE id=$1`, noActive.ID); err != nil {
		t.Fatal(err)
	}
	next, err := f.docs.CreateVersion(f.ctx, allowed.ID, domain.DocumentVersion{StorageKey: uuid.NewString(), OriginalFilename: "next.txt", MIMEType: "text/plain", SizeBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	f.seedVersion(t, next, domain.VersionReady, chunks)
	f.grant(t, allowed.ID, f.memberA.ID)
	results := f.find(t, f.memberA, dutiesQuestion, 30)
	if len(results) != 1 || results[0].DocumentVersionID != active.ID {
		t.Fatal("relaxed query exposed private/non-active/non-ready sources", results)
	}
	if len(f.find(t, f.memberB, dutiesQuestion, 30)) != 0 {
		t.Fatal("relaxed query bypassed ACL")
	}
	if err := f.acl.RevokeAccess(f.ctx, allowed.ID, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.memberA, dutiesQuestion, 30)) != 0 {
		t.Fatal("relaxed query ignored revoke")
	}
	f.grant(t, allowed.ID, f.memberA.ID)
	if err := f.docs.SetActiveVersion(f.ctx, allowed.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	results = f.find(t, f.memberA, dutiesQuestion, 30)
	if len(results) != 1 || results[0].DocumentVersionID != next.ID {
		t.Fatal("relaxed query ignored publication", results)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE users SET role='member' WHERE id=$1`, f.admin.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.admin, dutiesQuestion, 30)) != 0 {
		t.Fatal("stale admin bypassed SQL role check")
	}
	if err := NewUserRepository(f.db).SetActive(f.ctx, f.memberA.ID, false); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.memberA, dutiesQuestion, 30)) != 0 {
		t.Fatal("inactive user bypassed SQL check")
	}
}

func TestIntegrationSearchCoverageAndStagePriority(t *testing.T) {
	f := newSearchFixture(t)
	const query = "руководитель группа работа задачи сроки"
	strict, _ := f.document(t, "Точный", domain.VersionReady, []domain.DocumentChunk{{Content: query}})
	relaxed, _ := f.document(t, "Четыре из пяти", domain.VersionReady, []domain.DocumentChunk{{Content: "Руководитель группы распределяет задачи и контролирует сроки."}})
	f.document(t, query, domain.VersionReady, []domain.DocumentChunk{{Content: "Группа работает."}})
	f.document(t, "Шум", domain.VersionReady, []domain.DocumentChunk{{Content: strings.Repeat("руководитель группа ", 10)}})
	results := f.find(t, f.admin, query, 10)
	if len(results) != 2 || results[0].DocumentID != strict.ID || results[1].DocumentID != relaxed.ID || results[0].Score <= results[1].Score {
		t.Fatal("coverage, strict priority, or title-only filtering failed", results)
	}
	if got := f.find(t, f.admin, query, 1); len(got) != 1 || got[0].DocumentID != strict.ID {
		t.Fatal("limit=1 must not admit relaxed results", got)
	}
	for i := 0; i < 2; i++ {
		f.document(t, "Ещё точный", domain.VersionReady, []domain.DocumentChunk{{Content: query}})
	}
	results = f.find(t, f.admin, query, 10)
	if len(results) != 3 {
		t.Fatal("three strict results should suppress relaxed stage", results)
	}
	for _, result := range results {
		if result.DocumentID == relaxed.ID {
			t.Fatal("relaxed stage ran despite sufficient strict sources")
		}
	}

	f.document(t, "Обязанности", domain.VersionReady, []domain.DocumentChunk{{Content: dutiesContent}})
	for _, query := range []string{
		`"какие обязанности руководителя рабочей группы"`,
		`Какие обязанности руководителя рабочей группы -задачи`,
		`Какие обязанности руководителя рабочей группы (-задачи)`,
		`Какие обязанности руководителя рабочей группы OR отсутствующееслово`,
		`Какие обязанности бухгалтера рабочей группы группы группы?`,
		`руково`, // No substring/prefix matching of a different lexeme.
	} {
		if got := f.find(t, f.admin, query, 10); len(got) != 0 {
			t.Fatalf("operators or unique-lexeme threshold bypassed for %q: %+v", query, got)
		}
	}
}

func TestIntegrationSearchWeightedRankingAndMetadata(t *testing.T) {
	f := newSearchFixture(t)
	section := "Согласование"
	contentDoc, _ := f.document(t, "Порядок", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование согласование"}})
	sectionDoc, _ := f.document(t, "Порядок", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование", SectionTitle: &section}})
	titleDoc, _ := f.document(t, "Согласование", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование"}})
	plainDoc, _ := f.document(t, "Порядок", domain.VersionReady, []domain.DocumentChunk{{Content: "согласование"}})
	results := f.find(t, f.admin, "согласование", 10)
	want := []uuid.UUID{contentDoc.ID, sectionDoc.ID, titleDoc.ID, plainDoc.ID}
	if len(results) != len(want) {
		t.Fatal(results)
	}
	for i := range want {
		if results[i].DocumentID != want[i] {
			t.Fatal("body/section/title weights incorrect", results)
		}
	}

	wrongNumber, wrongTitle := "2", "Предыдущий раздел"
	goodNumber, goodTitle := "3.1", "Общие положения"
	f.document(t, "Метаданные", domain.VersionReady, []domain.DocumentChunk{
		{ChunkIndex: 0, Content: dutiesContent, SectionNumber: &wrongNumber, SectionTitle: &wrongTitle},
		{ChunkIndex: 1, Content: "3.1 Общие положения\nУникальныйметатест", SectionNumber: &goodNumber, SectionTitle: &goodTitle},
		{ChunkIndex: 2, Content: "2026 год Уникальныйгодтест", SectionNumber: &goodNumber, SectionTitle: &goodTitle},
	})
	result := f.find(t, f.admin, dutiesQuestion, 10)
	if len(result) != 1 || result[0].SectionNumber == nil || *result[0].SectionNumber != "3" || result[0].SectionTitle != nil {
		t.Fatal("stale heading metadata was not corrected", result)
	}
	for _, query := range []string{"Уникальныйметатест", "Уникальныйгодтест"} {
		result = f.find(t, f.admin, query, 10)
		if len(result) != 1 || result[0].SectionNumber == nil || *result[0].SectionNumber != goodNumber || result[0].SectionTitle == nil || *result[0].SectionTitle != goodTitle {
			t.Fatal("valid heading/year metadata changed", result)
		}
	}
}

func TestIntegrationSearchGINPlan(t *testing.T) {
	f := newSearchFixture(t)
	_, version := f.document(t, "План", domain.VersionReady, []domain.DocumentChunk{{Content: dutiesContent}})
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO document_chunks(document_version_id,chunk_index,content)
        SELECT $1, n, 'Технический посторонний материал ' || n FROM generate_series(1,12000) n`, version.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `VACUUM ANALYZE document_chunks`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"documents", "document_versions", "users"} {
		if _, err := f.db.ExecContext(f.ctx, `ANALYZE `+table); err != nil {
			t.Fatal(err)
		}
	}
	var raw []byte
	if err := f.db.QueryRowContext(f.ctx, `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) `+searchSQL, f.admin.ID, f.admin.Role, dutiesQuestion, 10).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var plan []map[string]any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	indexes := 0
	var visit func(map[string]any)
	visit = func(node map[string]any) {
		if node["Relation Name"] == "document_chunks" {
			t.Logf("chunks: %v; index=%v; filter=%v; rows=%v loops=%v", node["Node Type"], node["Index Name"], node["Filter"], node["Actual Rows"], node["Actual Loops"])
		}
		if node["Index Name"] == "document_chunks_content_fts_idx" {
			indexes++
			t.Logf("GIN: %s, loops=%v, rows=%v", node["Node Type"], node["Actual Loops"], node["Actual Rows"])
		}
		children, _ := node["Plans"].([]any)
		for _, child := range children {
			visit(child.(map[string]any))
		}
	}
	visit(plan[0]["Plan"].(map[string]any))
	if indexes < 2 {
		t.Fatalf("both FTS stages must use existing GIN on selective data, got %d GIN scans", indexes)
	}
	t.Logf("12001 chunks: planning=%v ms, execution=%v ms", plan[0]["Planning Time"], plan[0]["Execution Time"])
	// With enough exact sources, the relaxed candidate scan must not execute.
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO document_chunks(document_version_id,chunk_index,content)
        VALUES ($1,12001,$2), ($1,12002,$2)`, version.ID, dutiesContent); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, `EXPLAIN (ANALYZE, FORMAT JSON) `+searchSQL, f.admin.ID, f.admin.Role, "руководитель рабочая группа", 10).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	skippedRelaxed := false
	visit = func(node map[string]any) {
		if node["Index Name"] == "document_chunks_content_fts_idx" && node["Actual Loops"] == float64(0) {
			skippedRelaxed = true
		}
		children, _ := node["Plans"].([]any)
		for _, child := range children {
			visit(child.(map[string]any))
		}
	}
	visit(plan[0]["Plan"].(map[string]any))
	if !skippedRelaxed {
		t.Fatal("relaxed GIN scan executed despite three strict results")
	}
	t.Log("three strict sources: relaxed GIN scan loops=0")
}

func TestIntegrationSearchTableContextBoundaries(t *testing.T) {
	f := newSearchFixture(t)
	one, two := "1", "2"
	chunks := []domain.DocumentChunk{
		{ChunkIndex: 0, Content: "1. Рабочая группа в составе:", SectionNumber: &one},
		{ChunkIndex: 1, Content: "Роль\tУчастник", SectionNumber: &one},
		{ChunkIndex: 2, Content: "Секретарь\t____________", SectionNumber: &one},
		{ChunkIndex: 3, Content: "Далее обычный абзац", SectionNumber: &one},
		{ChunkIndex: 4, Content: "Подписант\tчужойконтекст", SectionNumber: &one},
		{ChunkIndex: 5, Content: "Другой раздел\tчужойконтекст", SectionNumber: &two},
	}
	doc, version := f.document(t, "Рабочая группа", domain.VersionReady, chunks)
	f.grant(t, doc.ID, f.memberA.ID)
	f.document(t, "Закрытая таблица", domain.VersionReady, chunks)
	results := f.find(t, f.memberA, "Кто входит в состав рабочей группы?", 10)
	if len(results) != 3 {
		t.Fatal("table context missing or crossed a block boundary", results)
	}
	for i, source := range results {
		if source.ChunkIndex != i || source.DocumentID != doc.ID || source.DocumentVersionID != version.ID || strings.Contains(source.Content, "чужойконтекст") {
			t.Fatal("wrong neighboring chunk", source)
		}
	}
	if len(f.find(t, f.memberB, "Кто входит в состав рабочей группы?", 10)) != 0 {
		t.Fatal("neighbor expansion bypassed ACL")
	}
	if got := f.find(t, f.memberA, `"Кто входит в состав рабочей группы"`, 10); len(got) != 0 {
		t.Fatal("context expansion bypassed an explicit phrase")
	}
	if got := f.find(t, f.memberA, "Какая зарплата у секретаря рабочей группы?", 10); len(got) != 0 {
		t.Fatal("table metadata compensated for missing topic")
	}
	if err := f.acl.RevokeAccess(f.ctx, doc.ID, f.memberA.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.find(t, f.memberA, "Кто входит в состав рабочей группы?", 10)) != 0 {
		t.Fatal("table context ignored ACL revoke")
	}
}

// Exercise the lexical family outside the working-group document. Equivalent
// forms must work in both directions without admitting a missing topic or
// counting two spellings as two covered concepts.
func TestIntegrationSearchCompositionLexemeFamily(t *testing.T) {
	f := newSearchFixture(t)
	inflected, _ := f.document(t, "Порядок формирования", domain.VersionReady, []domain.DocumentChunk{
		{Content: "Комиссия утверждается в составе представителей подразделений."},
	})
	f.document(t, "Состав комиссии", domain.VersionReady, []domain.DocumentChunk{
		{Content: "Комиссия утверждает решения."},             // The title cannot supply composition.
		{ChunkIndex: 1, Content: "Состав в составе состава."}, // No commission in the body.
	})
	got := f.find(t, f.admin, "Состав комиссии", 10)
	if len(got) != 1 || got[0].DocumentID != inflected.ID {
		t.Fatalf("inflected composition not found, or unrelated body admitted: %+v", got)
	}
	base, _ := f.document(t, "Порядок формирования", domain.VersionReady, []domain.DocumentChunk{
		{Content: "Состав совета утверждается ежегодно."},
	})
	got = f.find(t, f.admin, "Совет в составе", 10)
	if len(got) != 1 || got[0].DocumentID != base.ID {
		t.Fatalf("reverse lexical normalization failed: %+v", got)
	}
	for _, query := range []string{
		"Химический состав комиссии",
		"Состав составе комиссии химический",
		"Состав комиссии Иванов",
	} {
		if got := f.find(t, f.admin, query, 10); len(got) != 0 {
			t.Fatalf("lexical expansion lowered the relevance threshold for %q: %+v", query, got)
		}
	}
}
