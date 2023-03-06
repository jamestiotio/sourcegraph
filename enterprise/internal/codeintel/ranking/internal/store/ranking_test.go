package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/keegancsmith/sqlf"
	"github.com/lib/pq"
	"github.com/sourcegraph/log/logtest"

	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/shared/types"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/codeintel/uploads/shared"
	"github.com/sourcegraph/sourcegraph/internal/api"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/database/basestore"
	"github.com/sourcegraph/sourcegraph/internal/database/dbtest"
	"github.com/sourcegraph/sourcegraph/internal/observation"
)

const (
	mockRankingGraphKey    = "mockDev" // NOTE: ensure we don't have hyphens so we can validate derivative keys easily
	mockRankingBatchNumber = 10
)

func TestInsertDefinition(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	store := New(&observation.TestContext, db)

	// Insert definitions
	mockDefinitions := []shared.RankingDefinitions{
		{
			UploadID:     1,
			SymbolName:   "foo",
			Repository:   "deadbeef",
			DocumentPath: "foo.go",
		},
		{
			UploadID:     1,
			SymbolName:   "bar",
			Repository:   "deadbeef",
			DocumentPath: "bar.go",
		},
		{
			UploadID:     1,
			SymbolName:   "foo",
			Repository:   "deadbeef",
			DocumentPath: "foo.go",
		},
	}

	// Test InsertDefinitionsForRanking
	if err := store.InsertDefinitionsForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockDefinitions); err != nil {
		t.Fatalf("unexpected error inserting definitions: %s", err)
	}

	// Test definitions were inserted
	definitions, err := getRankingDefinitions(ctx, t, db, mockRankingGraphKey)
	if err != nil {
		t.Fatalf("unexpected error getting definitions: %s", err)
	}

	if diff := cmp.Diff(mockDefinitions, definitions); diff != "" {
		t.Errorf("unexpected definitions (-want +got):\n%s", diff)
	}
}

func TestInsertReferences(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	store := New(&observation.TestContext, db)

	// Insert references
	mockReferences := []shared.RankingReferences{
		{UploadID: 1, SymbolNames: []string{"foo", "bar", "baz"}},
	}

	for _, reference := range mockReferences {
		// Test InsertReferencesForRanking
		if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, reference); err != nil {
			t.Fatalf("unexpected error inserting references: %s", err)
		}
	}

	// Test references were inserted
	references, err := getRankingReferences(ctx, t, db, mockRankingGraphKey)
	if err != nil {
		t.Fatalf("unexpected error getting references: %s", err)
	}

	if diff := cmp.Diff(mockReferences, references); diff != "" {
		t.Errorf("unexpected references (-want +got):\n%s", diff)
	}
}

func TestInsertPathRanks(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	store := New(&observation.TestContext, db)

	insertUploads(t, db, types.Upload{ID: 1})

	// Insert definitions
	mockDefinitions := []shared.RankingDefinitions{
		{
			UploadID:     1,
			SymbolName:   "foo",
			Repository:   "deadbeef",
			DocumentPath: "foo.go",
		},
		{
			UploadID:     1,
			SymbolName:   "bar",
			Repository:   "deadbeef",
			DocumentPath: "bar.go",
		},
		{
			UploadID:     1,
			SymbolName:   "foo",
			Repository:   "deadbeef",
			DocumentPath: "foo.go",
		},
	}
	if err := store.InsertDefinitionsForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockDefinitions); err != nil {
		t.Fatalf("unexpected error inserting definitions: %s", err)
	}

	// Insert references
	mockReferences := shared.RankingReferences{
		UploadID: 1,
		SymbolNames: []string{
			mockDefinitions[0].SymbolName,
			mockDefinitions[1].SymbolName,
			mockDefinitions[2].SymbolName,
		},
	}
	if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockReferences); err != nil {
		t.Fatalf("unexpected error inserting references: %s", err)
	}

	// Test InsertPathCountInputs
	if _, _, err := store.InsertPathCountInputs(ctx, mockRankingGraphKey+"-123", 1000); err != nil {
		t.Fatalf("unexpected error inserting path count inputs: %s", err)
	}

	// Insert repos
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`INSERT INTO repo (id, name) VALUES (1, 'deadbeef')`)); err != nil {
		t.Fatalf("failed to insert repos: %s", err)
	}

	// Finally! Test InsertPathRanks
	numPathRanksInserted, numInputsProcessed, err := store.InsertPathRanks(ctx, mockRankingGraphKey+"-123", 10)
	if err != nil {
		t.Fatalf("unexpected error inserting path ranks: %s", err)
	}

	if numPathRanksInserted != 2 {
		t.Fatalf("unexpected number of path ranks inserted. want=%d have=%f", 2, numPathRanksInserted)
	}

	if numInputsProcessed != 1 {
		t.Fatalf("unexpected number of inputs processed. want=%d have=%f", 1, numInputsProcessed)
	}
}

func TestInsertPathCountInputs(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	store := New(&observation.TestContext, db)

	t1 := time.Now().Add(-time.Minute * 10)
	t2 := time.Now().Add(-time.Minute * 20)

	insertUploads(t, db,
		types.Upload{ID: 42, RepositoryID: 50},
		types.Upload{ID: 43, RepositoryID: 51},
		types.Upload{ID: 90, RepositoryID: 52},
		types.Upload{ID: 91, RepositoryID: 53, FinishedAt: &t1}, // younger
		types.Upload{ID: 92, RepositoryID: 53, FinishedAt: &t2}, // older
		types.Upload{ID: 93, RepositoryID: 54, Root: "lib/", Indexer: "test"},
		types.Upload{ID: 94, RepositoryID: 54, Root: "lib/", Indexer: "test"},
	)

	// Insert definitions
	mockDefinitions := []shared.RankingDefinitions{
		{
			UploadID:     42,
			SymbolName:   "foo",
			Repository:   "deadbeef",
			DocumentPath: "foo.go",
		},
		{
			UploadID:     42,
			SymbolName:   "bar",
			Repository:   "deadbeef",
			DocumentPath: "bar.go",
		},
		{
			UploadID:     43,
			SymbolName:   "baz",
			Repository:   "cafebabe",
			DocumentPath: "baz.go",
		},
		{
			UploadID:     43,
			SymbolName:   "bonk",
			Repository:   "cafebabe",
			DocumentPath: "bonk.go",
		},
	}
	if err := store.InsertDefinitionsForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockDefinitions); err != nil {
		t.Fatalf("unexpected error inserting definitions: %s", err)
	}

	//
	// Basic test case

	mockReferences := shared.RankingReferences{
		UploadID: 90,
		SymbolNames: []string{
			mockDefinitions[0].SymbolName,
			mockDefinitions[1].SymbolName,
			mockDefinitions[2].SymbolName,
		},
	}
	if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockReferences); err != nil {
		t.Fatalf("unexpected error inserting references: %s", err)
	}

	mockReferences = shared.RankingReferences{
		UploadID: 91,
		SymbolNames: []string{
			mockDefinitions[3].SymbolName,
		},
	}
	if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockReferences); err != nil {
		t.Fatalf("unexpected error inserting references: %s", err)
	}

	// duplicate of 91 (should not be processed as it's older)
	mockReferences = shared.RankingReferences{
		UploadID: 92,
		SymbolNames: []string{
			mockDefinitions[0].SymbolName,
			mockDefinitions[1].SymbolName,
			mockDefinitions[2].SymbolName,
			mockDefinitions[3].SymbolName,
		},
	}
	if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockReferences); err != nil {
		t.Fatalf("unexpected error inserting references: %s", err)
	}

	// Test InsertPathCountInputs: should process existing rows
	if _, _, err := store.InsertPathCountInputs(ctx, mockRankingGraphKey+"-123", 1000); err != nil {
		t.Fatalf("unexpected error inserting path count inputs: %s", err)
	}

	//
	// Incremental insertion test case

	mockReferences = shared.RankingReferences{
		UploadID: 93,
		SymbolNames: []string{
			mockDefinitions[0].SymbolName,
			mockDefinitions[1].SymbolName,
		},
	}
	if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockReferences); err != nil {
		t.Fatalf("unexpected error inserting references: %s", err)
	}

	// Test InsertPathCountInputs: should process unprocessed rows only
	if _, _, err := store.InsertPathCountInputs(ctx, mockRankingGraphKey+"-123", 1000); err != nil {
		t.Fatalf("unexpected error inserting path count inputs: %s", err)
	}

	//
	// No-op test case

	mockReferences = shared.RankingReferences{
		UploadID: 94,
		SymbolNames: []string{
			mockDefinitions[0].SymbolName,
			mockDefinitions[1].SymbolName,
			mockDefinitions[2].SymbolName,
			mockDefinitions[3].SymbolName,
		},
	}
	if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockReferences); err != nil {
		t.Fatalf("unexpected error inserting references: %s", err)
	}

	// Test InsertPathCountInputs: should do nothing, 94 covers the same project as 93
	if _, _, err := store.InsertPathCountInputs(ctx, mockRankingGraphKey+"-123", 1000); err != nil {
		t.Fatalf("unexpected error inserting path count inputs: %s", err)
	}

	//
	// Assertions

	// Test path count inputs were inserted
	inputs, err := getPathCountsInputs(ctx, t, db, mockRankingGraphKey)
	if err != nil {
		t.Fatalf("unexpected error getting path count inputs: %s", err)
	}

	expectedInputs := []pathCountsInput{
		{Repository: "cafebabe", DocumentPath: "baz.go", Count: 1},
		{Repository: "cafebabe", DocumentPath: "bonk.go", Count: 1},
		{Repository: "deadbeef", DocumentPath: "bar.go", Count: 2},
		{Repository: "deadbeef", DocumentPath: "foo.go", Count: 2},
	}
	if diff := cmp.Diff(expectedInputs, inputs); diff != "" {
		t.Errorf("unexpected path count inputs (-want +got):\n%s", diff)
	}
}

func TestVacuumStaleDefinitionsAndReferences(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	store := New(&observation.TestContext, db)

	mockDefinitions := []shared.RankingDefinitions{
		{UploadID: 1, SymbolName: "foo", Repository: "deadbeef", DocumentPath: "foo.go"},
		{UploadID: 1, SymbolName: "bar", Repository: "deadbeef", DocumentPath: "bar.go"},
		{UploadID: 2, SymbolName: "foo", Repository: "deadbeef", DocumentPath: "foo.go"},
		{UploadID: 2, SymbolName: "bar", Repository: "deadbeef", DocumentPath: "bar.go"},
		{UploadID: 3, SymbolName: "baz", Repository: "deadbeef", DocumentPath: "baz.go"},
	}
	mockReferences := []shared.RankingReferences{
		{UploadID: 1, SymbolNames: []string{"foo"}},
		{UploadID: 1, SymbolNames: []string{"bar"}},
		{UploadID: 2, SymbolNames: []string{"foo"}},
		{UploadID: 2, SymbolNames: []string{"bar"}},
		{UploadID: 2, SymbolNames: []string{"baz"}},
		{UploadID: 3, SymbolNames: []string{"bar"}},
		{UploadID: 3, SymbolNames: []string{"baz"}},
	}

	if err := store.InsertDefinitionsForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, mockDefinitions); err != nil {
		t.Fatalf("unexpected error inserting definitions: %s", err)
	}
	for _, reference := range mockReferences {
		if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, reference); err != nil {
			t.Fatalf("unexpected error inserting references: %s", err)
		}
	}

	assertCounts := func(expectedNumDefinitions, expectedNumReferences int) {
		definitions, err := getRankingDefinitions(ctx, t, db, mockRankingGraphKey)
		if err != nil {
			t.Fatalf("failed to get ranking definitions: %s", err)
		}
		if len(definitions) != expectedNumDefinitions {
			t.Fatalf("unexpected number of definitions. want=%d have=%d", expectedNumDefinitions, len(definitions))
		}

		references, err := getRankingReferences(ctx, t, db, mockRankingGraphKey)
		if err != nil {
			t.Fatalf("failed to get ranking references: %s", err)
		}
		if len(references) != expectedNumReferences {
			t.Fatalf("unexpected number of references. want=%d have=%d", expectedNumReferences, len(references))
		}
	}

	// assert initial count
	assertCounts(5, 7)

	// make upload 2 visible at tip (1 and 3 are not)
	insertVisibleAtTip(t, db, 50, 2)

	// remove definitions and references for non-visible uploads
	numStaleDefinitionRecordsDeleted, numStaleReferenceRecordsDeleted, err := store.VacuumStaleDefinitionsAndReferences(ctx, mockRankingGraphKey)
	if err != nil {
		t.Fatalf("unexpected error vacuuming stale definitions and references: %s", err)
	}
	if expected := 3; numStaleDefinitionRecordsDeleted != expected {
		t.Fatalf("unexpected number of definition records deleted. want=%d have=%d", expected, numStaleDefinitionRecordsDeleted)
	}
	if expected := 4; numStaleReferenceRecordsDeleted != expected {
		t.Fatalf("unexpected number of reference records deleted. want=%d have=%d", expected, numStaleReferenceRecordsDeleted)
	}

	// only upload 2's entries remain
	assertCounts(2, 3)
}

func TestVacuumStaleGraphs(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	store := New(&observation.TestContext, db)

	mockReferences := []shared.RankingReferences{
		{UploadID: 1, SymbolNames: []string{"foo"}},
		{UploadID: 1, SymbolNames: []string{"bar"}},
		{UploadID: 2, SymbolNames: []string{"foo"}},
		{UploadID: 2, SymbolNames: []string{"bar"}},
		{UploadID: 2, SymbolNames: []string{"baz"}},
		{UploadID: 3, SymbolNames: []string{"bar"}},
		{UploadID: 3, SymbolNames: []string{"baz"}},
	}
	for _, reference := range mockReferences {
		if err := store.InsertReferencesForRanking(ctx, mockRankingGraphKey, mockRankingBatchNumber, reference); err != nil {
			t.Fatalf("unexpected error inserting references: %s", err)
		}
	}

	for _, graphKey := range []string{mockRankingGraphKey + "-123", mockRankingGraphKey + "-456", mockRankingGraphKey + "-789"} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO codeintel_ranking_references_processed (graph_key, codeintel_ranking_reference_id)
			SELECT $1, id FROM codeintel_ranking_references
	`, graphKey); err != nil {
			t.Fatalf("failed to insert ranking references processed: %s", err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO codeintel_ranking_path_counts_inputs (repository, document_path, count, graph_key)
			SELECT 50, '', 100, $1 FROM generate_series(1, 30)
	`, graphKey); err != nil {
			t.Fatalf("failed to insert ranking path count inputs: %s", err)
		}
	}

	assertCounts := func(expectedMetadataRecords, expectedInputRecords int) {
		store := basestore.NewWithHandle(db.Handle())

		numMetadataRecords, _, err := basestore.ScanFirstInt(store.Query(ctx, sqlf.Sprintf(`SELECT COUNT(*) FROM codeintel_ranking_references_processed`)))
		if err != nil {
			t.Fatalf("failed to count metadata records: %s", err)
		}
		if expectedMetadataRecords != numMetadataRecords {
			t.Fatalf("unexpected number of metadata records. want=%d have=%d", expectedMetadataRecords, numMetadataRecords)
		}

		numInputRecords, _, err := basestore.ScanFirstInt(store.Query(ctx, sqlf.Sprintf(`SELECT COUNT(*) FROM codeintel_ranking_path_counts_inputs`)))
		if err != nil {
			t.Fatalf("failed to count input records: %s", err)
		}
		if expectedInputRecords != numInputRecords {
			t.Fatalf("unexpected number of input records. want=%d have=%d", expectedInputRecords, numInputRecords)
		}
	}

	// assert initial count
	assertCounts(3*7, 3*30)

	// remove records associated with other ranking keys
	metadataRecordsDeleted, inputRecordsDeleted, err := store.VacuumStaleGraphs(ctx, mockRankingGraphKey+"-456")
	if err != nil {
		t.Fatalf("unexpected error vacuuming stale graphs: %s", err)
	}
	if expected := 2 * 7; metadataRecordsDeleted != expected {
		t.Fatalf("unexpected number of metadata records deleted. want=%d have=%d", expected, metadataRecordsDeleted)
	}
	if expected := 2 * 30; inputRecordsDeleted != expected {
		t.Fatalf("unexpected number of input records deleted. want=%d have=%d", expected, inputRecordsDeleted)
	}

	// only the non-stale derivative graph key remains
	assertCounts(1*7, 1*30)
}

func TestVacuumStaleRanks(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	store := newInternal(&observation.TestContext, db)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO repo (name) VALUES ('bar'), ('baz'), ('bonk'), ('foo1'), ('foo2'), ('foo3'), ('foo4'), ('foo5')`); err != nil {
		t.Fatalf("failed to insert repos: %s", err)
	}

	for r, key := range map[string]string{
		"foo1": mockRankingGraphKey + "-123",
		"foo2": mockRankingGraphKey + "-123",
		"foo3": mockRankingGraphKey + "-123",
		"foo4": mockRankingGraphKey + "-123",
		"foo5": mockRankingGraphKey + "-123",
		"bar":  mockRankingGraphKey + "-234",
		"baz":  mockRankingGraphKey + "-345",
		"bonk": mockRankingGraphKey + "-456",
	} {
		if err := store.setDocumentRanks(ctx, api.RepoName(r), nil, key); err != nil {
			t.Fatalf("failed to insert document ranks: %s", err)
		}
	}

	assertNames := func(expectedNames []string) {
		store := basestore.NewWithHandle(db.Handle())

		names, err := basestore.ScanStrings(store.Query(ctx, sqlf.Sprintf(`
			SELECT r.name
			FROM repo r
			JOIN codeintel_path_ranks pr ON pr.repository_id = r.id
			ORDER BY r.name
		`)))
		if err != nil {
			t.Fatalf("failed to fetch names: %s", err)
		}

		if diff := cmp.Diff(expectedNames, names); diff != "" {
			t.Errorf("unexpected names (-want +got):\n%s", diff)
		}
	}

	// assert initial names
	assertNames([]string{"bar", "baz", "bonk", "foo1", "foo2", "foo3", "foo4", "foo5"})

	// remove sufficiently stale records associated with other ranking keys
	rankRecordsDeleted, err := store.VacuumStaleRanks(ctx, mockRankingGraphKey+"-456")
	if err != nil {
		t.Fatalf("unexpected error vacuuming stale ranks: %s", err)
	}
	if expected := 6; rankRecordsDeleted != expected {
		t.Fatalf("unexpected number of rank records deleted. want=%d have=%d", expected, rankRecordsDeleted)
	}

	// stale graph keys have been removed
	assertNames([]string{"baz", "bonk"})
}

func getRankingDefinitions(
	ctx context.Context,
	t *testing.T,
	db database.DB,
	graphKey string,
) (_ []shared.RankingDefinitions, err error) {
	query := fmt.Sprintf(
		`SELECT upload_id, symbol_name, repository, document_path FROM codeintel_ranking_definitions WHERE graph_key = '%s'`,
		graphKey,
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { err = basestore.CloseRows(rows, err) }()

	var definitions []shared.RankingDefinitions
	for rows.Next() {
		var uploadID int
		var symbolName string
		var repository string
		var documentPath string
		err = rows.Scan(&uploadID, &symbolName, &repository, &documentPath)
		if err != nil {
			return nil, err
		}
		definitions = append(definitions, shared.RankingDefinitions{
			UploadID:     uploadID,
			SymbolName:   symbolName,
			Repository:   repository,
			DocumentPath: documentPath,
		})
	}

	return definitions, nil
}

func getRankingReferences(
	ctx context.Context,
	t *testing.T,
	db database.DB,
	graphKey string,
) (_ []shared.RankingReferences, err error) {
	query := fmt.Sprintf(
		`SELECT upload_id, symbol_names FROM codeintel_ranking_references WHERE graph_key = '%s'`,
		graphKey,
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { err = basestore.CloseRows(rows, err) }()

	var references []shared.RankingReferences
	for rows.Next() {
		var uploadID int
		var symbolNames []string
		err = rows.Scan(&uploadID, pq.Array(&symbolNames))
		if err != nil {
			return nil, err
		}
		references = append(references, shared.RankingReferences{
			UploadID:    uploadID,
			SymbolNames: symbolNames,
		})
	}

	return references, nil
}

type pathCountsInput struct {
	Repository   string
	DocumentPath string
	Count        int
}

func getPathCountsInputs(
	ctx context.Context,
	t *testing.T,
	db database.DB,
	graphKey string,
) (_ []pathCountsInput, err error) {
	query := sqlf.Sprintf(`
		SELECT repository, document_path, SUM(count)
		FROM codeintel_ranking_path_counts_inputs
		WHERE graph_key LIKE %s || '%%'
		GROUP BY repository, document_path
		ORDER BY repository, document_path
	`, graphKey)
	rows, err := db.QueryContext(ctx, query.Query(sqlf.PostgresBindVar), query.Args()...)
	if err != nil {
		return nil, err
	}
	defer func() { err = basestore.CloseRows(rows, err) }()

	var pathCountsInputs []pathCountsInput
	for rows.Next() {
		var input pathCountsInput
		if err := rows.Scan(&input.Repository, &input.DocumentPath, &input.Count); err != nil {
			return nil, err
		}

		pathCountsInputs = append(pathCountsInputs, input)
	}

	return pathCountsInputs, nil
}

// insertVisibleAtTip populates rows of the lsif_uploads_visible_at_tip table for the given repository
// with the given identifiers. Each upload is assumed to refer to the tip of the default branch. To mark
// an upload as protected (visible to _some_ branch) butn ot visible from the default branch, use the
// insertVisibleAtTipNonDefaultBranch method instead.
func insertVisibleAtTip(t testing.TB, db database.DB, repositoryID int, uploadIDs ...int) {
	insertVisibleAtTipInternal(t, db, repositoryID, true, uploadIDs...)
}

func insertVisibleAtTipInternal(t testing.TB, db database.DB, repositoryID int, isDefaultBranch bool, uploadIDs ...int) {
	var rows []*sqlf.Query
	for _, uploadID := range uploadIDs {
		rows = append(rows, sqlf.Sprintf("(%s, %s, %s)", repositoryID, uploadID, isDefaultBranch))
	}

	query := sqlf.Sprintf(
		`INSERT INTO lsif_uploads_visible_at_tip (repository_id, upload_id, is_default_branch) VALUES %s`,
		sqlf.Join(rows, ","),
	)
	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while updating uploads visible at tip: %s", err)
	}
}

// insertUploads populates the lsif_uploads table with the given upload models.
func insertUploads(t testing.TB, db database.DB, uploads ...types.Upload) {
	for _, upload := range uploads {
		if upload.Commit == "" {
			upload.Commit = makeCommit(upload.ID)
		}
		if upload.State == "" {
			upload.State = "completed"
		}
		if upload.RepositoryID == 0 {
			upload.RepositoryID = 50
		}
		if upload.Indexer == "" {
			upload.Indexer = "lsif-go"
		}
		if upload.IndexerVersion == "" {
			upload.IndexerVersion = "latest"
		}
		if upload.UploadedParts == nil {
			upload.UploadedParts = []int{}
		}

		// Ensure we have a repo for the inner join in select queries
		insertRepo(t, db, upload.RepositoryID, upload.RepositoryName)

		query := sqlf.Sprintf(`
			INSERT INTO lsif_uploads (
				id,
				commit,
				root,
				uploaded_at,
				state,
				failure_message,
				started_at,
				finished_at,
				process_after,
				num_resets,
				num_failures,
				repository_id,
				indexer,
				indexer_version,
				num_parts,
				uploaded_parts,
				upload_size,
				associated_index_id,
				should_reindex
			) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
		`,
			upload.ID,
			upload.Commit,
			upload.Root,
			upload.UploadedAt,
			upload.State,
			upload.FailureMessage,
			upload.StartedAt,
			upload.FinishedAt,
			upload.ProcessAfter,
			upload.NumResets,
			upload.NumFailures,
			upload.RepositoryID,
			upload.Indexer,
			upload.IndexerVersion,
			upload.NumParts,
			pq.Array(upload.UploadedParts),
			upload.UploadSize,
			upload.AssociatedIndexID,
			upload.ShouldReindex,
		)

		if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
			t.Fatalf("unexpected error while inserting upload: %s", err)
		}
	}
}

// makeCommit formats an integer as a 40-character git commit hash.
func makeCommit(i int) string {
	return fmt.Sprintf("%040d", i)
}

// insertRepo creates a repository record with the given id and name. If there is already a repository
// with the given identifier, nothing happens
func insertRepo(t testing.TB, db database.DB, id int, name string) {
	if name == "" {
		name = fmt.Sprintf("n-%d", id)
	}

	deletedAt := sqlf.Sprintf("NULL")
	if strings.HasPrefix(name, "DELETED-") {
		deletedAt = sqlf.Sprintf("%s", time.Unix(1587396557, 0).UTC())
	}

	query := sqlf.Sprintf(
		`INSERT INTO repo (id, name, deleted_at) VALUES (%s, %s, %s) ON CONFLICT (id) DO NOTHING`,
		id,
		name,
		deletedAt,
	)
	if _, err := db.ExecContext(context.Background(), query.Query(sqlf.PostgresBindVar), query.Args()...); err != nil {
		t.Fatalf("unexpected error while upserting repository: %s", err)
	}
}
