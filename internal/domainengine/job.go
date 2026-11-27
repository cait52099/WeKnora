package domainengine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	TaskType = "domain:stage"
	Queue    = "domain"
)

func NativeTaskID(documentID, sourcePath, sourceHash string) string {
	return "domain-native:" + digest([]byte(documentID+"\x00"+sourceHash))
}

type ProviderFailure struct {
	Code       string
	RetryAfter time.Duration
	Permanent  bool
}

func (e *ProviderFailure) Error() string { return e.Code }

type ModelPin struct {
	Fingerprint  string `json:"fingerprint"`
	InputBudget  int    `json:"input_budget"`
	OutputBudget int    `json:"output_budget"`
}

type ModelResult struct {
	Text             string
	FinishReason     string
	PromptTokens     int
	CompletionTokens int
	ToolAttempt      bool
}

type Models interface {
	Pin(context.Context, uint64, string) (ModelPin, error)
	Generate(context.Context, uint64, string, ModelPin, string, string) (ModelResult, error)
}

type Job struct {
	ID string `json:"id"`
	Scope
	DocumentID     string        `json:"document_id"`
	ProfileID      string        `json:"profile_id"`
	Revision       string        `json:"revision"`
	BindingVersion int64         `json:"binding_version"`
	Status         string        `json:"status"`
	ErrorCode      string        `json:"error_code"`
	StageIndex     int           `json:"stage_index"`
	ParseAttempt   sql.NullInt64 `json:"-"`
	CreatedAt      time.Time     `json:"created_at"`
}

const jobColumns = `j.id,j.tenant_id,j.kb_id,j.document_id,j.profile_id,j.revision,j.binding_version,
	j.status,j.error_code,j.stage_index,j.parse_attempt,j.created_at`

func jobFields(j *Job) []any {
	return []any{&j.ID, &j.TenantID, &j.KBID, &j.DocumentID, &j.ProfileID, &j.Revision, &j.BindingVersion,
		&j.Status, &j.ErrorCode, &j.StageIndex, &j.ParseAttempt, &j.CreatedAt}
}

const liveJob = ` JOIN domain_engine.kb_bindings b ON b.tenant_id=j.tenant_id AND b.kb_id=j.kb_id
		AND b.enabled AND b.access_epoch=j.access_epoch
	JOIN knowledges k ON k.id=j.document_id AND k.tenant_id=j.tenant_id AND k.knowledge_base_id=j.kb_id
		AND k.file_path=j.source_path AND k.file_hash=j.native_hash AND k.deleted_at IS NULL
	JOIN knowledge_bases kb ON kb.id=j.kb_id AND kb.tenant_id=j.tenant_id AND kb.deleted_at IS NULL
	JOIN tenants t ON t.id=j.tenant_id AND t.deleted_at IS NULL `

// Start records the intent before the native parser is enqueued. Only hashes
// and limits are pinned. Provider credentials remain in the native model store.
func (s *Store) Start(ctx context.Context, scope Scope, doc, path, hash string, models Models, nativePayload ...[]byte) error {
	return s.start(ctx, scope, doc, path, hash, models, "waiting_parse", nativePayload...)
}

func (s *Store) start(ctx context.Context, scope Scope, doc, path, hash string, models Models, initialStatus string, nativePayload ...[]byte) error {
	b, err := s.Binding(ctx, scope)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || !b.Enabled {
		return err
	}
	var payload []byte
	if len(nativePayload) > 0 {
		payload = nativePayload[0]
	}
	p, err := s.LoadProfile(ctx, scope.TenantID, b.ProfileID, b.Revision)
	if err != nil {
		return err
	}
	pins := make(map[string]ModelPin)
	options := make(map[string]bool)
	jobState, code := initialStatus, ""
	for _, stage := range p.profile.Stages {
		if stage.Optional && !stage.Enabled {
			continue
		}
		options[stage.ID] = true
		pin, err := models.Pin(ctx, scope.TenantID, stage.ModelRef)
		if err != nil {
			jobState, code = "failed", "model_unavailable_at_submission"
			break
		}
		pins[stage.ID] = pin
	}
	pinJSON, _ := json.Marshal(pins)
	optionJSON, _ := json.Marshal(options)
	_, err = s.db.ExecContext(ctx, `INSERT INTO domain_engine.jobs
		(tenant_id,kb_id,document_id,source_path,native_hash,profile_id,revision,binding_version,access_epoch,model_pins,optional_stages,status,error_code,native_payload)
		SELECT b.tenant_id,b.kb_id,k.id,k.file_path,k.file_hash,$11,$12,$6,b.access_epoch,$7::jsonb,$8::jsonb,$9,$10,$14
		FROM domain_engine.kb_bindings b JOIN knowledges k ON k.tenant_id=b.tenant_id AND k.knowledge_base_id=b.kb_id
		WHERE b.tenant_id=$1 AND b.kb_id=$2 AND k.id=$3 AND k.file_path=$4 AND k.file_hash=$5 AND b.access_epoch=$13 AND b.enabled
		AND k.deleted_at IS NULL
		ON CONFLICT DO NOTHING`, scope.TenantID, scope.KBID, doc, path, hash, b.Version, string(pinJSON), string(optionJSON), jobState, code, b.ProfileID, b.Revision, b.AccessEpoch, payload)
	return err
}

// CaptureBinding uses the submitted version. The compatibility path allows
// Phase 2A snapshots on documents with no submitted Phase 2B intent.
func (s *Store) CaptureBinding(ctx context.Context, scope Scope, doc, path, hash string) (*Binding, string, error) {
	var b Binding
	b.Scope, b.Enabled = scope, true
	var jobID string
	err := s.db.QueryRowContext(ctx, `SELECT j.binding_version,j.profile_id,j.revision,j.id
		FROM domain_engine.jobs j`+liveJob+`WHERE j.tenant_id=$1 AND j.kb_id=$2 AND j.document_id=$3
		AND j.source_path=$4 AND j.native_hash=$5 AND j.status IN ('waiting_parse','capture_failed')
		ORDER BY j.created_at DESC LIMIT 1`, scope.TenantID, scope.KBID, doc, path, hash).
		Scan(&b.Version, &b.ProfileID, &b.Revision, &jobID)
	if errors.Is(err, sql.ErrNoRows) {
		active, err := s.ActiveBinding(ctx, scope)
		return active, "", err
	}
	return &b, jobID, err
}

func (s *Store) CaptureFailed(ctx context.Context, id string) {
	if id != "" {
		_, _ = s.db.ExecContext(ctx, `UPDATE domain_engine.jobs SET status='capture_failed',error_code='snapshot_unavailable'
			WHERE id=$1 AND status='waiting_parse'`, id)
	}
}

func (s *Store) ListJobs(ctx context.Context, scope Scope) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM domain_engine.jobs j`+liveJob+
		`WHERE j.tenant_id=$1 AND j.kb_id=$2 ORDER BY j.created_at DESC LIMIT 100`, scope.TenantID, scope.KBID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		var j Job
		if err := rows.Scan(jobFields(&j)...); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) GetJob(ctx context.Context, scope Scope, id string) (Job, error) {
	var j Job
	err := s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM domain_engine.jobs j`+liveJob+
		`WHERE j.tenant_id=$1 AND j.kb_id=$2 AND j.id=$3`, scope.TenantID, scope.KBID, id).Scan(jobFields(&j)...)
	return j, err
}

func (s *Store) ChangeJob(ctx context.Context, scope Scope, id string, cancel bool) error {
	status, from := "queued", "failed"
	if cancel {
		status, from = "cancelled", "running"
	}
	result, err := s.db.ExecContext(ctx, `UPDATE domain_engine.jobs target SET status=$4,retries=0,error_code='',fence=target.fence+1,
		due_at=now(),wake_at=now(),lease_until=NULL FROM domain_engine.jobs j`+liveJob+`
		WHERE target.id=j.id AND j.id=$1 AND j.tenant_id=$2 AND j.kb_id=$3
		AND (target.status=$5 AND ($4='cancelled' OR target.parse_attempt IS NOT NULL)
		OR $4='cancelled' AND target.status IN ('queued','retry_wait','waiting_parse','capture_failed','needs_review'))`,
		id, scope.TenantID, scope.KBID, status, from)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}
