package domainengine

import (
	"context"
	"encoding/json"
)

// Reprocess applies the current binding to a captured document. It preserves
// the previous job and reuses only the contiguous validated prefix whose stage
// dependencies and pinned models match.
func (s *Store) Reprocess(ctx context.Context, scope Scope, id string, models Models) error {
	old, err := s.GetJob(ctx, scope, id)
	if err != nil {
		return err
	}
	if !old.ParseAttempt.Valid || old.Status == "running" || old.Status == "waiting_parse" {
		return ErrConflict
	}
	intent, err := s.prepare(ctx, scope, models, "preparing")
	if err != nil {
		return err
	}
	if intent == nil || intent.status != "preparing" || intent.binding.Version == old.BindingVersion {
		return ErrConflict
	}
	binding := intent.binding
	var path, hash string
	err = s.db.QueryRowContext(ctx, `SELECT source_path,native_hash FROM domain_engine.jobs WHERE id=$1`, old.ID).Scan(&path, &hash)
	if err != nil {
		return err
	}

	from, err := s.LoadProfile(ctx, scope.TenantID, old.ProfileID, old.Revision)
	if err != nil {
		return err
	}
	to, err := s.LoadProfile(ctx, scope.TenantID, binding.ProfileID, binding.Revision)
	if err != nil {
		return err
	}
	oldHashes, err := from.StageFingerprints()
	if err != nil {
		return err
	}
	newHashes, err := to.StageFingerprints()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	created, err := intent.insert(ctx, tx, old.DocumentID, path, hash, nil)
	if err != nil {
		return err
	}
	if !created {
		var exists bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM domain_engine.jobs j`+liveJob+`
			WHERE j.tenant_id=$1 AND j.kb_id=$2 AND j.document_id=$3 AND j.source_path=$4 AND j.binding_version=$5
			AND j.status<>'preparing')`, scope.TenantID, scope.KBID, old.DocumentID, path, binding.Version).Scan(&exists)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		return ErrConflict
	}

	var newID string
	var oldPins, newPins []byte
	var snapshotVersion int64
	err = tx.QueryRowContext(ctx, `SELECT next.id,old.model_pins,next.model_pins,old.snapshot_binding_version
		FROM domain_engine.jobs old JOIN domain_engine.jobs next
		ON next.tenant_id=old.tenant_id AND next.kb_id=old.kb_id AND next.document_id=old.document_id
		AND next.source_path=old.source_path AND next.native_hash=old.native_hash
		JOIN domain_engine.kb_bindings b ON b.tenant_id=next.tenant_id AND b.kb_id=next.kb_id
		 AND b.version=next.binding_version AND b.access_epoch=next.access_epoch AND b.access_epoch=old.access_epoch AND b.enabled
		JOIN knowledges k ON k.id=next.document_id AND k.tenant_id=next.tenant_id AND k.knowledge_base_id=next.kb_id
		 AND k.file_path=next.source_path AND k.file_hash=next.native_hash AND k.deleted_at IS NULL
		JOIN knowledge_bases kb ON kb.id=next.kb_id AND kb.tenant_id=next.tenant_id AND kb.deleted_at IS NULL
		WHERE old.id=$1 AND next.binding_version=$2 AND next.status='preparing'
		FOR UPDATE OF next,b,k,kb`, old.ID, binding.Version).Scan(&newID, &oldPins, &newPins, &snapshotVersion)
	if err != nil {
		return err
	}
	var previous, pinned map[string]ModelPin
	if err = json.Unmarshal(oldPins, &previous); err != nil {
		return err
	}
	if err = json.Unmarshal(newPins, &pinned); err != nil {
		return err
	}
	var index, reused int
	for index < len(to.profile.Stages) {
		stage := to.profile.Stages[index]
		if stage.Optional && !stage.Enabled {
			index++
			continue
		}
		if oldHashes[stage.ID] != newHashes[stage.ID] || previous[stage.ID] != pinned[stage.ID] {
			break
		}
		out, err := tx.ExecContext(ctx, `INSERT INTO domain_engine.stage_runs
			(job_id,fence,stage_index,stage_id,raw_response,valid_json,prompt_tokens,completion_tokens,error_code,reused,input_coverage)
			SELECT $2,$3,$4,stage_id,raw_response,valid_json,0,0,'',true,input_coverage FROM domain_engine.stage_runs
			WHERE job_id=$1 AND stage_id=$5 AND valid_json IS NOT NULL ORDER BY fence DESC LIMIT 1`,
			old.ID, newID, reused+1, index, stage.ID)
		if err != nil {
			return err
		}
		n, err := out.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		reused++
		index++
	}
	status := "queued"
	if index == len(to.profile.Stages) {
		status = "succeeded"
	}
	_, err = tx.ExecContext(ctx, `UPDATE domain_engine.jobs SET parse_attempt=$2,snapshot_binding_version=$3,
		stage_index=$4,fence=$5,status=$6,due_at=now(),wake_at=now() WHERE id=$1`,
		newID, old.ParseAttempt.Int64, snapshotVersion, index, reused, status)
	if err != nil {
		return err
	}
	return tx.Commit()
}
