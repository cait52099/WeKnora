package domainengine

import (
	"context"
	"fmt"
)

type JobPage struct {
	Jobs   []DashboardJob   `json:"jobs"`
	Next   string           `json:"next,omitempty"`
	Counts map[string]int64 `json:"counts"`
	Usage  UsageSummary     `json:"usage"`
}
type DashboardJob struct {
	Job
	FileName string `json:"file_name"`
	Title    string `json:"title"`
	Current  bool   `json:"current"`
}
type UsageSummary struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	ModelCalls       int64 `json:"model_calls"`
	ReportedCalls    int64 `json:"reported_calls"`
	UnknownCalls     int64 `json:"unknown_calls"`
	ReusedRuns       int64 `json:"reused_runs"`
	LegacyRuns       int64 `json:"legacy_runs"`
}

func (s *Store) Dashboard(ctx context.Context, scope Scope, status, search, before string, limit int) (JobPage, error) {
	out := JobPage{Jobs: make([]DashboardJob, 0), Counts: make(map[string]int64)}
	limit = max(1, min(100, limit))
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+`,COALESCE(k.file_name,''),COALESCE(k.title,''),j.binding_version=b.version
		FROM domain_engine.jobs j`+liveJob+`WHERE j.tenant_id=$1 AND j.kb_id=$2
		AND ($3='' OR j.status=$3)
		AND ($4='' OR strpos(lower(COALESCE(k.file_name,'')),lower($4))>0 OR strpos(lower(COALESCE(k.title,'')),lower($4))>0)
		AND (NULLIF($5,'') IS NULL OR (j.created_at,j.id)<(SELECT anchor.created_at,anchor.id FROM domain_engine.jobs anchor
			WHERE anchor.id=NULLIF($5,'')::uuid AND anchor.tenant_id=$1 AND anchor.kb_id=$2 AND anchor.access_epoch=j.access_epoch))
		ORDER BY j.created_at DESC,j.id DESC LIMIT $6`, scope.TenantID, scope.KBID, status, search, before, limit+1)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var job DashboardJob
		if err := rows.Scan(append(jobFields(&job.Job), &job.FileName, &job.Title, &job.Current)...); err != nil {
			rows.Close()
			return out, err
		}
		out.Jobs = append(out.Jobs, job)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(out.Jobs) > limit {
		out.Jobs = out.Jobs[:limit]
		out.Next = out.Jobs[limit-1].ID
	}
	rows, err = s.db.QueryContext(ctx, `SELECT j.status,count(*) FROM domain_engine.jobs j`+liveJob+`
		WHERE j.tenant_id=$1 AND j.kb_id=$2 GROUP BY j.status`, scope.TenantID, scope.KBID)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var key string
		var n int64
		if err := rows.Scan(&key, &n); err != nil {
			rows.Close()
			return out, err
		}
		out.Counts[key] = n
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	u := &out.Usage
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(sum(r.prompt_tokens),0),COALESCE(sum(r.completion_tokens),0),
		COALESCE(sum(r.model_calls),0),COALESCE(sum(r.reported_calls),0),
		count(*) FILTER (WHERE r.reused),count(*) FILTER (WHERE r.model_calls IS NULL AND NOT r.reused)
		FROM domain_engine.jobs j`+liveJob+`JOIN domain_engine.stage_runs r ON r.job_id=j.id WHERE j.tenant_id=$1 AND j.kb_id=$2`,
		scope.TenantID, scope.KBID).Scan(&u.PromptTokens, &u.CompletionTokens, &u.ModelCalls, &u.ReportedCalls, &u.ReusedRuns, &u.LegacyRuns)
	u.UnknownCalls = u.ModelCalls - u.ReportedCalls
	if err != nil {
		return out, fmt.Errorf("read domain usage: %w", err)
	}
	return out, nil
}
