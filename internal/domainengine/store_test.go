package domainengine

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresDomainStore(t *testing.T) {
	dsn := os.Getenv("DOMAIN_ENGINE_TEST_DSN")
	if dsn == "" {
		t.Skip("DOMAIN_ENGINE_TEST_DSN is required for the PostgreSQL integration test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var databaseName string
	must(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&databaseName))
	if !strings.HasPrefix(databaseName, "weknora_domain_") || !strings.HasSuffix(databaseName, "_test") {
		t.Fatal("use a dedicated weknora_domain_*_test database")
	}
	run := func(statement string) {
		t.Helper()
		_, err := db.ExecContext(ctx, statement)
		must(t, err)
	}
	run(`CREATE TABLE knowledge_bases (id text PRIMARY KEY, tenant_id bigint NOT NULL, deleted_at timestamptz);
	CREATE TABLE knowledges (id text PRIMARY KEY, tenant_id bigint NOT NULL, knowledge_base_id text NOT NULL,
		file_path text, file_hash text, parse_status text, deleted_at timestamptz);
	CREATE TABLE knowledge_processing_spans (knowledge_id text NOT NULL, attempt integer NOT NULL);
	INSERT INTO knowledge_bases VALUES ('kb', 1, NULL), ('other', 2, NULL);`)
	defer run("DROP TABLE knowledge_processing_spans, knowledges, knowledge_bases")
	up, err := os.ReadFile("../../migrations/versioned/000138_domain_engine.up.sql")
	must(t, err)
	down, err := os.ReadFile("../../migrations/versioned/000138_domain_engine.down.sql")
	must(t, err)
	run(string(up))
	defer run(string(down))

	store := NewStore(db)
	scope := Scope{TenantID: 1, KBID: "kb"}
	profile := fixture(t, "cosmetic")
	must(t, store.Publish(ctx, 1, profile))
	must(t, store.Publish(ctx, 1, profile))
	definition, err := profile.Definition()
	must(t, err)
	definition.Name = "Different content at same revision"
	changed, err := importDefinition(t, definition)
	must(t, err)
	if err := store.Publish(ctx, 1, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("same revision conflict: %v", err)
	}
	loaded, err := store.LoadProfile(ctx, 1, definition.ID, definition.Revision)
	must(t, err)
	if loaded.Fingerprint() != profile.Fingerprint() {
		t.Fatal("profile changed on round trip")
	}
	if _, err := store.LoadProfile(ctx, 2, definition.ID, definition.Revision); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant profile lookup: %v", err)
	}
	binding := Binding{Scope: scope, ProfileID: definition.ID, Revision: definition.Revision, Enabled: true}
	version, err := store.Bind(ctx, binding, 0)
	must(t, err)
	if version != 1 {
		t.Fatalf("first binding has version %d", version)
	}
	if _, err := store.Bind(ctx, binding, 99); !errors.Is(err, ErrConflict) {
		t.Fatalf("binding CAS: %v", err)
	}
	if _, err := store.Bind(ctx, Binding{Scope: Scope{TenantID: 1, KBID: "other"},
		ProfileID: definition.ID, Revision: definition.Revision, Enabled: true}, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-tenant bind: %v", err)
	}
	active, err := store.ActiveBinding(ctx, scope)
	must(t, err)
	if active == nil || active.Version != 1 {
		t.Fatal("expected active binding")
	}

	original := []byte("raw bytes\x00 中文\r\n")
	native := md5.Sum(original)
	nativeHash := hex.EncodeToString(native[:])
	_, err = db.ExecContext(ctx, "INSERT INTO knowledges VALUES ('doc',1,'kb','source',$1,'processing',NULL)", nativeHash)
	must(t, err)
	run("INSERT INTO knowledge_processing_spans VALUES ('doc',1)")
	snapshot := ParseSnapshot{
		Binding: *active, DocumentID: "doc", Attempt: 1, FilePath: "source", NativeFileHash: nativeHash,
		Original: original, ParserConfigSHA256: digest([]byte("parser v1")), ParsedMarkdown: "中😀\r\n文",
		NormalizedMarkdown: "中😀\n文",
		SourceMap:          []byte(`{"parsed":[{"start":0,"end":2}],"normalized":[],"images":[]}`),
		Coverage:           DocumentCoverageReport{Body: Observed, ModelInput: NotEvaluated, ExtractionQuality: NotEvaluated},
	}
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- store.SaveSnapshot(ctx, snapshot) }()
	}
	must(t, <-results)
	must(t, <-results)
	key := SnapshotKey{Scope: scope, DocumentID: "doc", Attempt: 1, BindingVersion: 1}
	saved, err := store.LoadSnapshot(ctx, key)
	must(t, err)
	if string(saved.Original) != string(original) || saved.ParsedMarkdown != snapshot.ParsedMarkdown ||
		saved.NormalizedMarkdown != snapshot.NormalizedMarkdown || saved.Coverage != snapshot.Coverage ||
		saved.SourceSHA256 != digest(original) || !strings.Contains(string(saved.SourceMap), `"end":2`) {
		t.Fatal("snapshot did not preserve original, parser text, coordinates and coverage")
	}
	conflicting := snapshot
	conflicting.NormalizedMarkdown = "changed"
	if err := store.SaveSnapshot(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("snapshot conflict: %v", err)
	}
	unauthorized := key
	unauthorized.TenantID = 2
	if _, err := store.LoadSnapshot(ctx, unauthorized); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-tenant snapshot lookup: %v", err)
	}
	run("INSERT INTO knowledge_processing_spans VALUES ('doc',2)")
	if err := store.SaveSnapshot(ctx, snapshot); !errors.Is(err, ErrStaleSource) {
		t.Fatalf("old attempt was accepted: %v", err)
	}
	snapshot.Attempt = 2
	run("UPDATE knowledges SET file_path='new-source' WHERE id='doc'")
	if err := store.SaveSnapshot(ctx, snapshot); !errors.Is(err, ErrStaleSource) {
		t.Fatalf("replaced upload was accepted: %v", err)
	}
	run("UPDATE knowledges SET file_path='source' WHERE id='doc'")
	must(t, store.SaveSnapshot(ctx, snapshot))
	binding.Enabled = false
	_, err = store.Bind(ctx, binding, 1)
	must(t, err)
	active, err = store.ActiveBinding(ctx, scope)
	must(t, err)
	if active != nil {
		t.Fatal("disabled binding was returned")
	}
	if err := store.SaveSnapshot(ctx, snapshot); !errors.Is(err, ErrStaleSource) {
		t.Fatalf("revoked binding was accepted: %v", err)
	}
	must(t, store.PurgeDeletedDocument(ctx, scope, "doc"))
	_, err = store.LoadSnapshot(ctx, key)
	must(t, err) // an active upload is not eligible for purge.
	run("UPDATE knowledges SET deleted_at=now() WHERE id='doc'")
	if _, err := store.LoadSnapshot(ctx, key); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted source snapshot remained readable: %v", err)
	}
	must(t, store.PurgeDeletedDocument(ctx, scope, "doc"))
	var count int
	must(t, db.QueryRowContext(ctx, "SELECT count(*) FROM domain_engine.parse_snapshots").Scan(&count))
	if count != 0 {
		t.Fatal("authorized post-delete purge left snapshots")
	}
	must(t, db.QueryRowContext(ctx, "SELECT count(*) FROM domain_engine.originals").Scan(&count))
	if count != 0 {
		t.Fatal("authorized post-delete purge left original bytes")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestOversizedSnapshotFailsBeforePersistence(t *testing.T) {
	store := NewStore(nil)
	snapshot := ParseSnapshot{
		Attempt: 1, Original: []byte("fixture"), ParserConfigSHA256: digest([]byte("parser")),
		SourceMap: []byte("{}"), ParsedMarkdown: strings.Repeat("x", maxSnapshotBytes),
	}
	if err := store.SaveSnapshot(context.Background(), snapshot); err == nil ||
		!strings.Contains(err.Error(), "exceeds size limit") {
		t.Fatalf("expected size error before database write, got %v", err)
	}
}
