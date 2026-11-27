package domainengine

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hibiken/asynq"
)

type recordingModels struct {
	responses []ModelResult
	calls     []string
	hook      func()
}

func (m *recordingModels) Pin(context.Context, uint64, string) (ModelPin, error) {
	return ModelPin{Fingerprint: "fixture", InputBudget: 8000, OutputBudget: 500}, nil
}
func (m *recordingModels) Generate(_ context.Context, _ uint64, _ string, _ ModelPin, _ string, input string) (ModelResult, error) {
	m.calls = append(m.calls, input)
	if m.hook != nil {
		m.hook()
		m.hook = nil
	}
	if len(m.responses) == 0 {
		return ModelResult{}, errors.New("unexpected mock call")
	}
	r := m.responses[0]
	m.responses = m.responses[1:]
	return r, nil
}

type losingQueue struct{ ids []string }

func (q *losingQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.ids = append(q.ids, string(task.Payload()))
	return nil, errors.New("synthetic lost wake")
}

type trackingQueue struct {
	client *asynq.Client
	info   *asynq.TaskInfo
}

func (q *trackingQueue) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	info, err := q.client.Enqueue(task, opts...)
	q.info = info
	return info, err
}

func TestPostgresDomainWorker(t *testing.T) {
	dsn := os.Getenv("DOMAIN_ENGINE_TEST_DSN")
	if dsn == "" {
		t.Skip("DOMAIN_ENGINE_TEST_DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	must(t, err)
	defer db.Close()
	ctx := context.Background()
	var name string
	must(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name))
	if !strings.HasPrefix(name, "weknora_domain_") || !strings.HasSuffix(name, "_test") {
		t.Fatal("use a dedicated weknora_domain_*_test database")
	}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		must(t, err)
	}
	exec(`CREATE TABLE tenants (id bigint PRIMARY KEY,deleted_at timestamptz);
	CREATE TABLE knowledge_bases (id text PRIMARY KEY,tenant_id bigint,deleted_at timestamptz);
	CREATE TABLE knowledges (id text PRIMARY KEY,tenant_id bigint,knowledge_base_id text,file_path text,file_hash text,parse_status text,deleted_at timestamptz);
	CREATE TABLE knowledge_processing_spans (knowledge_id text,attempt integer);
	INSERT INTO tenants VALUES (1,NULL),(2,NULL);
	INSERT INTO knowledge_bases VALUES ('cosmetic',1,NULL),('automotive',1,NULL),('foreign',2,NULL);`)
	defer exec("DROP TABLE knowledge_processing_spans,knowledges,knowledge_bases,tenants")
	for _, migration := range []string{"000138_domain_engine", "000139_domain_jobs"} {
		up, err := os.ReadFile("../../migrations/versioned/" + migration + ".up.sql")
		must(t, err)
		down, err := os.ReadFile("../../migrations/versioned/" + migration + ".down.sql")
		must(t, err)
		exec(string(up))
		defer exec(string(down))
	}
	for _, tc := range []struct{ name, extract, semantic string }{
		{"cosmetic", `{"formula_groups":[{"group_code":"A","mixing_temperature_c":21}]}`, `{"study_observations":[]}`},
		{"automotive", `{"vehicle_platforms":[{"platform_code":"P","cycle_count":3}]}`, `{"failure_modes":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(db)
			doc := "file-" + tc.name
			scope := Scope{TenantID: 1, KBID: tc.name}
			original := []byte("ORIGINAL_SENTINEL")
			md := md5.Sum(original)
			hash := hex.EncodeToString(md[:])
			exec("INSERT INTO knowledges VALUES($1,1,$2,'source',$3,'processing',NULL)", doc, tc.name, hash)
			exec("INSERT INTO knowledge_processing_spans VALUES($1,1)", doc)
			p := fixture(t, tc.name)
			must(t, store.Publish(ctx, 1, p))
			b := Binding{Scope: scope, Enabled: true, ProfileID: p.profile.ID, Revision: p.profile.Revision}
			_, err := store.Bind(ctx, b, 0)
			must(t, err)
			model := &recordingModels{responses: []ModelResult{
				{Text: tc.extract, PromptTokens: 10, CompletionTokens: 2},
				{Text: "invalid", PromptTokens: 12, CompletionTokens: 1},
				{Text: tc.semantic, PromptTokens: 12, CompletionTokens: 3},
			}}
			must(t, store.Start(ctx, scope, doc, "source", hash, model))
			binding, id, err := store.CaptureBinding(ctx, scope, doc, "source", hash)
			must(t, err)
			if binding == nil || id == "" {
				t.Fatal("missing submitted intent")
			}
			newRevision, _ := p.Definition()
			newRevision.Revision = "2"
			newRevision.Stages[1].Prompt += " Changed later."
			next, err := importDefinition(t, newRevision)
			must(t, err)
			must(t, store.Publish(ctx, 1, next))
			b.Revision = "2"
			_, err = store.Bind(ctx, b, 1)
			must(t, err)
			must(t, store.SaveSnapshot(ctx, ParseSnapshot{JobID: id, Binding: *binding, DocumentID: doc, Attempt: 1, FilePath: "source",
				NativeFileHash: hash, Original: original, ParserConfigSHA256: digest([]byte("mock-parser")),
				ParsedMarkdown: string(original), NormalizedMarkdown: string(original), SourceMap: []byte("{}")}))
			w := &Worker{Store: store, Models: model}
			if address := os.Getenv("DOMAIN_ENGINE_TEST_REDIS_ADDR"); address != "" {
				redis := asynq.RedisClientOpt{Addr: address}
				client := asynq.NewClient(redis)
				t.Cleanup(func() { must(t, client.Close()) })
				inspector := asynq.NewInspector(redis)
				t.Cleanup(func() { must(t, inspector.Close()) })
				actual := &trackingQueue{client: client}
				must(t, w.Dispatch(ctx, actual))
				if actual.info == nil || actual.info.Queue != Queue || string(actual.info.Payload) != id {
					t.Fatalf("Redis received wrong wake: %+v", actual.info)
				}
				must(t, inspector.DeleteTask(Queue, actual.info.ID))
				exec("UPDATE domain_engine.jobs SET wake_at=now() WHERE id=$1", id)
				must(t, (&Worker{Store: store, Models: model}).Dispatch(ctx, actual))
				if actual.info == nil || string(actual.info.Payload) != id {
					t.Fatal("deleted Redis message was not recovered")
				}
				must(t, inspector.DeleteTask(Queue, actual.info.ID))
				exec("UPDATE domain_engine.jobs SET wake_at=now() WHERE id=$1", id)
			}
			queue := new(losingQueue)
			if err := w.Dispatch(ctx, queue); err == nil || len(queue.ids) != 1 || queue.ids[0] != id {
				t.Fatalf("durable wake: %v %v", queue.ids, err)
			}
			exec("UPDATE domain_engine.jobs SET wake_at=now() WHERE id=$1", id)
			restarted := &Worker{Store: store, Models: model}
			if err := restarted.Dispatch(ctx, queue); err == nil || len(queue.ids) != 2 {
				t.Fatal("wake was not available after restart")
			}
			must(t, restarted.Run(ctx, id))
			must(t, restarted.Run(ctx, id))
			must(t, restarted.Run(ctx, id)) // retry is not yet due
			if len(model.calls) != 2 {
				t.Fatalf("model called during backoff: %d", len(model.calls))
			}
			exec("UPDATE domain_engine.jobs SET due_at=now() WHERE id=$1", id)
			must(t, restarted.Run(ctx, id))
			must(t, restarted.Run(ctx, id))
			if len(model.calls) != 3 {
				t.Fatalf("unexpected model calls: %d", len(model.calls))
			}
			var input map[string]string
			must(t, json.Unmarshal([]byte(model.calls[2]), &input))
			if len(input) != 1 || input["extraction"] != tc.extract || strings.Contains(model.calls[2], "ORIGINAL_SENTINEL") {
				t.Fatal("analysis did not receive only the complete prior JSON")
			}
			j, err := store.GetJob(ctx, scope, id)
			must(t, err)
			if j.Status != "succeeded" || j.Revision != p.profile.Revision {
				t.Fatalf("pinned job: %+v", j)
			}
			runs, err := store.Runs(ctx, scope, id)
			must(t, err)
			if len(runs) != 3 || runs[1].ValidAvailable || runs[2].PromptTokens != 12 || !runs[2].ValidAvailable {
				t.Fatalf("runs: %+v", runs)
			}
			artifact, err := store.RunArtifact(ctx, scope, id, runs[2].Fence, true)
			must(t, err)
			if string(artifact) != tc.semantic {
				t.Fatal("valid artifact did not round trip")
			}
			if _, err = store.GetJob(ctx, Scope{TenantID: 2, KBID: scope.KBID}, id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("tenant boundary: %v", err)
			}
			must(t, store.Reprocess(ctx, scope, id, model))
			jobs, err := store.ListJobs(ctx, scope)
			must(t, err)
			if len(jobs) != 2 || jobs[0].StageIndex != 1 || jobs[0].Revision != "2" {
				t.Fatalf("incremental jobs: %+v", jobs)
			}
			model.responses = append(model.responses, ModelResult{Text: tc.semantic, PromptTokens: 12})
			must(t, restarted.Run(ctx, jobs[0].ID))
			if len(model.calls) != 4 {
				t.Fatal("incremental run repeated extraction")
			}
			newRuns, err := store.Runs(ctx, scope, jobs[0].ID)
			must(t, err)
			if len(newRuns) != 2 || !newRuns[0].Reused || newRuns[0].PromptTokens != 0 {
				t.Fatalf("incremental runs: %+v", newRuns)
			}

			newRevision.Revision = "3"
			newRevision.Stages[1].Prompt += " New analysis config."
			p3, err := importDefinition(t, newRevision)
			must(t, err)
			must(t, store.Publish(ctx, 1, p3))
			b.Revision = "3"
			_, err = store.Bind(ctx, b, 2)
			must(t, err)
			must(t, store.Reprocess(ctx, scope, jobs[0].ID, model))
			third, err := store.ListJobs(ctx, scope)
			must(t, err)
			cancelID := third[0].ID
			model.responses = append(model.responses, ModelResult{Text: tc.semantic, PromptTokens: 12})
			model.hook = func() { must(t, store.ChangeJob(ctx, scope, cancelID, true)) }
			must(t, restarted.Run(ctx, cancelID))
			cancelled, err := store.GetJob(ctx, scope, cancelID)
			must(t, err)
			cancelledRuns, err := store.Runs(ctx, scope, cancelID)
			must(t, err)
			if cancelled.Status != "cancelled" || len(cancelledRuns) != 1 || !cancelledRuns[0].Reused {
				t.Fatalf("late result escaped cancellation: %+v %+v", cancelled, cancelledRuns)
			}
			if tc.name == "cosmetic" {
				exec(`CREATE FUNCTION domain_engine.fixture_fail_cleanup() RETURNS trigger LANGUAGE plpgsql
					AS $$ BEGIN RAISE EXCEPTION 'synthetic cleanup failure'; END; $$;
					CREATE TRIGGER fixture_cleanup BEFORE DELETE ON domain_engine.originals
					FOR EACH ROW EXECUTE FUNCTION domain_engine.fixture_fail_cleanup();`)
				_, err = db.ExecContext(ctx, "UPDATE knowledges SET deleted_at=now() WHERE id=$1", doc)
				if err == nil {
					t.Fatal("synthetic cleanup failure did not roll back native deletion")
				}
				_, err = store.GetJob(ctx, scope, id)
				must(t, err)
				exec("DROP TRIGGER fixture_cleanup ON domain_engine.originals; DROP FUNCTION domain_engine.fixture_fail_cleanup()")
				exec("UPDATE knowledges SET deleted_at=now() WHERE id=$1", doc)
			} else {
				exec("UPDATE knowledges SET knowledge_base_id='foreign',tenant_id=2 WHERE id=$1", doc)
			}

			if _, err = store.GetJob(ctx, scope, id); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("move boundary: %v", err)
			}
			var count int
			must(t, db.QueryRowContext(ctx, `SELECT count(*) FROM domain_engine.originals WHERE document_id=$1`, doc).Scan(&count))
			if count != 0 {
				t.Fatal("move left original bytes in old scope")
			}
		})
	}
}

func TestCompleteInputCannotBeSilentlyShortened(t *testing.T) {
	p := fixture(t, "automotive")
	m := &recordingModels{}
	w := &Worker{Models: m}
	r := w.execute(context.Background(), 1, p, p.profile.Stages[0], ModelPin{InputBudget: 100, OutputBudget: 10},
		map[string]string{"document": strings.Repeat("A", 8000)}, 0, nil)
	if r.state != "needs_review" || len(m.calls) != 0 {
		t.Fatal("oversized input was submitted")
	}
}

func TestPartitionProcessesEveryByteAndMergesInOrder(t *testing.T) {
	initial := fixture(t, "automotive")
	definition, _ := initial.Definition()
	definition.Stages[0].Partition = &ArrayPartition{ArrayKey: "vehicle_platforms", MaxParts: 4}
	p, err := importDefinition(t, definition)
	must(t, err)
	m := &recordingModels{responses: []ModelResult{
		{Text: `{"vehicle_platforms":[{"platform_code":"A","cycle_count":1}]}`, PromptTokens: 2},
		{Text: `{"vehicle_platforms":[{"platform_code":"B","cycle_count":1}]}`, PromptTokens: 3},
	}}
	w := &Worker{Models: m}
	source := strings.Repeat("A", 1000) + "\n\n" + strings.Repeat("B", 1000)
	out := w.execute(context.Background(), 1, p, p.profile.Stages[0], ModelPin{InputBudget: 2400, OutputBudget: 100}, map[string]string{"source": source}, 0, nil)
	if out.valid == nil || out.coverage.CompletedParts != 2 || out.coverage.CompletedBytes != len(source) || out.result.PromptTokens != 5 {
		t.Fatalf("partition result: %+v", out)
	}
	var reconstructed string
	for _, call := range m.calls {
		var input map[string]string
		must(t, json.Unmarshal([]byte(call), &input))
		reconstructed += input["source"]
	}
	if reconstructed != source || strings.Index(string(out.valid), `"A"`) >= strings.Index(string(out.valid), `"B"`) {
		t.Fatal("partition lost source bytes or changed order")
	}
	definition.Stages[1].Partition = &ArrayPartition{ArrayKey: "failure_modes", MaxParts: 4}
	if _, err := importDefinition(t, definition); err == nil {
		t.Fatal("analysis stage unexpectedly allowed partitioning")
	}
	guarded := &recordingModels{responses: []ModelResult{{Text: `{"vehicle_platforms":[]}`}}}
	w.Models = guarded
	checks := 0
	revoked := w.execute(context.Background(), 1, p, p.profile.Stages[0], ModelPin{InputBudget: 2400, OutputBudget: 100},
		map[string]string{"source": source}, 0, func(context.Context) (bool, error) { checks++; return checks == 1, nil })
	if len(guarded.calls) != 1 || revoked.state != "cancelled" || revoked.valid != nil {
		t.Fatal("revoked partition reached the model")
	}
}

type failingModel struct {
	err    error
	result ModelResult
}

func (m failingModel) Pin(context.Context, uint64, string) (ModelPin, error) {
	return ModelPin{}, m.err
}
func (m failingModel) Generate(context.Context, uint64, string, ModelPin, string, string) (ModelResult, error) {
	return m.result, m.err
}

func TestProviderFailureAndTruncationStates(t *testing.T) {
	p := fixture(t, "cosmetic")
	stage := p.profile.Stages[0]
	pin := ModelPin{InputBudget: 8000, OutputBudget: 500}
	input := map[string]string{"source": "synthetic input"}
	tests := []struct {
		model       failingModel
		retries     int
		state, code string
	}{
		{failingModel{err: &ProviderFailure{Code: "provider_rate_limited", RetryAfter: time.Minute}}, 0, "retry_wait", "provider_rate_limited"},
		{failingModel{err: context.DeadlineExceeded}, 2, "failed", "model_timeout"},
		{failingModel{result: ModelResult{Text: "{}", FinishReason: "length"}}, 0, "needs_review", "output_truncated"},
		{failingModel{result: ModelResult{Text: "{}", ToolAttempt: true}}, 0, "retry_wait", "model_tool_request_rejected"},
	}
	for _, tc := range tests {
		w := Worker{Models: tc.model}
		out := w.execute(context.Background(), 1, p, stage, pin, input, tc.retries, nil)
		if out.state != tc.state || out.code != tc.code || out.valid != nil {
			t.Fatalf("unexpected outcome for %s: %+v", tc.code, out)
		}
		if tc.code == "provider_rate_limited" && out.retryAfter != time.Minute {
			t.Fatal("missing provider retry hint")
		}
	}
}
