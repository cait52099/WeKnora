package domainengine

import (
	"context"
	"encoding/json"
)

type ProfileRevision struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

func (s *Store) ListProfiles(ctx context.Context, tenant uint64) ([]ProfileRevision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT profile_id,revision FROM domain_engine.profile_revisions
		WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT 200`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ProfileRevision, 0)
	for rows.Next() {
		var p ProfileRevision
		if err := rows.Scan(&p.ID, &p.Revision); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (p *CompiledProfile) Manifest() []byte { return append([]byte(nil), p.raw...) }

func (s *Store) Binding(ctx context.Context, scope Scope) (Binding, error) {
	b := Binding{Scope: scope}
	err := s.db.QueryRowContext(ctx, `SELECT version,profile_id,revision,enabled,access_epoch FROM domain_engine.kb_bindings
		WHERE tenant_id=$1 AND kb_id=$2`, scope.TenantID, scope.KBID).Scan(&b.Version, &b.ProfileID, &b.Revision, &b.Enabled, &b.AccessEpoch)
	return b, err
}

type StageRun struct {
	InputCoverage    json.RawMessage `json:"input_coverage"`
	Reused           bool            `json:"reused"`
	Fence            int64           `json:"attempt"`
	StageID          string          `json:"stage_id"`
	RawAvailable     bool            `json:"raw_available"`
	ValidAvailable   bool            `json:"valid_available"`
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	ErrorCode        string          `json:"error_code"`
}

func (s *Store) Runs(ctx context.Context, scope Scope, job string) ([]StageRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.fence,r.stage_id,octet_length(r.raw_response)>0,r.valid_json IS NOT NULL,
		r.prompt_tokens,r.completion_tokens,r.error_code,r.reused,r.input_coverage FROM domain_engine.jobs j`+liveJob+`
		JOIN domain_engine.stage_runs r ON r.job_id=j.id WHERE j.tenant_id=$1 AND j.kb_id=$2 AND j.id=$3
		ORDER BY r.fence LIMIT 200`, scope.TenantID, scope.KBID, job)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StageRun, 0)
	for rows.Next() {
		var r StageRun
		if err := rows.Scan(&r.Fence, &r.StageID, &r.RawAvailable, &r.ValidAvailable, &r.PromptTokens, &r.CompletionTokens, &r.ErrorCode, &r.Reused, &r.InputCoverage); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) RunArtifact(ctx context.Context, scope Scope, job string, fence int64, valid bool) ([]byte, error) {
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN $5 THEN r.valid_json ELSE r.raw_response END
		FROM domain_engine.jobs j`+liveJob+` JOIN domain_engine.stage_runs r ON r.job_id=j.id
		WHERE j.tenant_id=$1 AND j.kb_id=$2 AND j.id=$3 AND r.fence=$4`, scope.TenantID, scope.KBID, job, fence, valid).Scan(&data)
	return data, err
}

type ParsePreview struct {
	Parsed     string          `json:"parsed_markdown"`
	Normalized string          `json:"normalized_markdown"`
	Coverage   json.RawMessage `json:"coverage"`
	SourceMap  json.RawMessage `json:"source_map"`
}

func (s *Store) ParsePreview(ctx context.Context, scope Scope, id string) (ParsePreview, error) {
	var out ParsePreview
	err := s.db.QueryRowContext(ctx, `SELECT p.parsed_markdown,p.normalized_markdown,p.coverage,p.source_map
		FROM domain_engine.jobs j`+liveJob+` JOIN domain_engine.parse_snapshots p
		ON p.tenant_id=j.tenant_id AND p.kb_id=j.kb_id AND p.document_id=j.document_id
		AND p.parse_attempt=j.parse_attempt AND p.binding_version=j.snapshot_binding_version
		WHERE j.tenant_id=$1 AND j.kb_id=$2 AND j.id=$3`, scope.TenantID, scope.KBID, id).
		Scan(&out.Parsed, &out.Normalized, &out.Coverage, &out.SourceMap)
	return out, err
}
