package domainengine

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const (
	MaxOriginalBytes = 100 << 20
	maxSnapshotBytes = 32 << 20
)

type CoverageSignal string

const (
	Observed     CoverageSignal = "observed"
	Unknown      CoverageSignal = "unknown"
	NotEvaluated CoverageSignal = "not_evaluated"
)

// DocumentCoverageReport records positive parser signals and unknowns. A
// parser signal does not establish that all corresponding source items survived.
type DocumentCoverageReport struct {
	Body              CoverageSignal `json:"body"`
	Tables            CoverageSignal `json:"tables"`
	Images            CoverageSignal `json:"images"`
	Captions          CoverageSignal `json:"captions"`
	Supplements       CoverageSignal `json:"supplements"`
	SourcePositions   CoverageSignal `json:"source_positions"`
	ParseCompleteness CoverageSignal `json:"parse_completeness"`
	ModelInput        CoverageSignal `json:"model_input"`
	ExtractionQuality CoverageSignal `json:"extraction_quality"`
}

type SnapshotKey struct {
	Scope
	DocumentID     string
	Attempt        int
	BindingVersion int64
}

type ParseSnapshot struct {
	JobID              string                 `json:"-"`
	Binding            Binding                `json:"-"`
	DocumentID         string                 `json:"-"`
	Attempt            int                    `json:"-"`
	FilePath           string                 `json:"-"`
	NativeFileHash     string                 `json:"-"`
	Original           []byte                 `json:"-"`
	ParserConfigSHA256 string                 `json:"parser_config_sha256"`
	ParsedMarkdown     string                 `json:"parsed_markdown"`
	NormalizedMarkdown string                 `json:"normalized_markdown"`
	SourceMap          json.RawMessage        `json:"source_map"`
	Coverage           DocumentCoverageReport `json:"coverage"`
}

// SaveSnapshot atomically stores the parser input and the pre-split output.
// Retrying the same capture is safe. A conflicting result stays historical
// only if a new parse attempt is explicitly created.
func (s *Store) SaveSnapshot(ctx context.Context, snap ParseSnapshot) error {
	if snap.Attempt < 1 || len(snap.Original) == 0 || len(snap.Original) > MaxOriginalBytes ||
		len(snap.ParserConfigSHA256) != 64 || !json.Valid(snap.SourceMap) {
		return errors.New("invalid or oversized domain capture")
	}
	if len(snap.ParsedMarkdown)+len(snap.NormalizedMarkdown)+len(snap.SourceMap) > maxSnapshotBytes {
		return errors.New("domain parse snapshot exceeds size limit")
	}
	// Native file uploads use MD5 as their source revision; domain artifact
	// identity and verification use SHA-256.
	nativeHash := md5.Sum(snap.Original)
	if hex.EncodeToString(nativeHash[:]) != snap.NativeFileHash {
		return ErrStaleSource
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	coverage, err := json.Marshal(snap.Coverage)
	if err != nil {
		return err
	}
	sourceHash, snapshotHash := digest(snap.Original), digest(raw)
	b := snap.Binding
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var documentID string
	guard := `AND b.version=$6 AND b.profile_id=$7 AND b.revision=$8 AND b.enabled`
	args := []any{b.TenantID, b.KBID, snap.DocumentID, snap.FilePath, snap.NativeFileHash, b.Version, b.ProfileID, b.Revision}
	if snap.JobID != "" {
		guard = `AND b.enabled AND EXISTS (SELECT 1 FROM domain_engine.jobs j WHERE j.id=$9 AND j.tenant_id=b.tenant_id
 AND j.kb_id=b.kb_id AND j.document_id=k.id AND j.source_path=k.file_path AND j.native_hash=k.file_hash
 AND j.binding_version=$6 AND j.profile_id=$7 AND j.revision=$8 AND j.access_epoch=b.access_epoch
 AND j.status IN ('waiting_parse','capture_failed'))`
		args = append(args, snap.JobID)
	}
	err = tx.QueryRowContext(ctx, `SELECT k.id FROM knowledges k
		JOIN knowledge_bases kb ON kb.id=k.knowledge_base_id AND kb.tenant_id=k.tenant_id
		JOIN domain_engine.kb_bindings b ON b.kb_id=kb.id AND b.tenant_id=kb.tenant_id
		WHERE k.tenant_id=$1 AND kb.id=$2 AND k.id=$3 AND k.file_path=$4 AND k.file_hash=$5
		AND k.deleted_at IS NULL AND kb.deleted_at IS NULL AND k.parse_status NOT IN ('deleting','cancelled')
		`+guard+` FOR UPDATE OF k, kb, b`, args...).Scan(&documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStaleSource
	}
	if err != nil {
		return err
	}
	var latest int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt),0) FROM knowledge_processing_spans
		WHERE knowledge_id=$1`, documentID).Scan(&latest); err != nil {
		return err
	}
	if latest != snap.Attempt {
		return ErrStaleSource
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO domain_engine.originals
		(tenant_id,kb_id,document_id,sha256,content) VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		b.TenantID, b.KBID, documentID, sourceHash, snap.Original)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO domain_engine.parse_snapshots
		(tenant_id,kb_id,document_id,parse_attempt,binding_version,profile_id,profile_revision,source_sha256,
		 snapshot_sha256,parser_config_sha256,parsed_markdown,normalized_markdown,source_map,coverage)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13::json,$14::jsonb) ON CONFLICT DO NOTHING`,
		b.TenantID, b.KBID, documentID, snap.Attempt, b.Version, b.ProfileID, b.Revision,
		sourceHash, snapshotHash, snap.ParserConfigSHA256, snap.ParsedMarkdown, snap.NormalizedMarkdown,
		string(snap.SourceMap), string(coverage))
	if err != nil {
		return err
	}
	var actualSource, actualSnapshot string
	err = tx.QueryRowContext(ctx, `SELECT source_sha256,snapshot_sha256 FROM domain_engine.parse_snapshots
		WHERE tenant_id=$1 AND kb_id=$2 AND document_id=$3 AND parse_attempt=$4 AND binding_version=$5`,
		b.TenantID, b.KBID, documentID, snap.Attempt, b.Version).Scan(&actualSource, &actualSnapshot)
	if err != nil {
		return err
	}
	if sourceHash != actualSource || snapshotHash != actualSnapshot {
		return ErrConflict
	}
	if snap.JobID != "" {
		result, err := tx.ExecContext(ctx, `UPDATE domain_engine.jobs SET parse_attempt=$2,snapshot_binding_version=binding_version,status='queued',error_code='',
			due_at=now(),wake_at=now() WHERE id=$1 AND status IN ('waiting_parse','capture_failed')`, snap.JobID, snap.Attempt)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrStaleSource
		}
	}
	return tx.Commit()
}

type StoredSnapshot struct {
	ParseSnapshot
	SourceSHA256   string
	SnapshotSHA256 string
}

func (s *Store) LoadSnapshot(ctx context.Context, key SnapshotKey) (*StoredSnapshot, error) {
	out := &StoredSnapshot{ParseSnapshot: ParseSnapshot{
		Binding:    Binding{Scope: key.Scope, Version: key.BindingVersion},
		DocumentID: key.DocumentID,
		Attempt:    key.Attempt,
	}}
	var coverage []byte
	err := s.db.QueryRowContext(ctx, `SELECT p.profile_id,p.profile_revision,p.source_sha256,p.snapshot_sha256,
		p.parser_config_sha256,p.parsed_markdown,p.normalized_markdown,p.source_map,p.coverage,o.content
		FROM domain_engine.parse_snapshots p JOIN domain_engine.originals o
		ON (o.tenant_id,o.kb_id,o.document_id,o.sha256)=(p.tenant_id,p.kb_id,p.document_id,p.source_sha256)
		JOIN knowledges k ON k.tenant_id=p.tenant_id AND k.knowledge_base_id=p.kb_id AND k.id=p.document_id
		JOIN knowledge_bases kb ON kb.tenant_id=p.tenant_id AND kb.id=p.kb_id
		WHERE p.tenant_id=$1 AND p.kb_id=$2 AND p.document_id=$3 AND p.parse_attempt=$4 AND p.binding_version=$5
		AND k.deleted_at IS NULL AND k.parse_status <> 'deleting' AND kb.deleted_at IS NULL`,
		key.TenantID, key.KBID, key.DocumentID, key.Attempt, key.BindingVersion).
		Scan(&out.Binding.ProfileID, &out.Binding.Revision, &out.SourceSHA256, &out.SnapshotSHA256,
			&out.ParserConfigSHA256, &out.ParsedMarkdown, &out.NormalizedMarkdown, &out.SourceMap, &coverage, &out.Original)
	if err != nil {
		return nil, err
	}
	if digest(out.Original) != out.SourceSHA256 {
		return nil, errors.New("domain original digest mismatch")
	}
	if err := json.Unmarshal(coverage, &out.Coverage); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(out.ParseSnapshot)
	if err != nil {
		return nil, err
	}
	if digest(raw) != out.SnapshotSHA256 {
		return nil, errors.New("domain snapshot digest mismatch")
	}
	return out, nil
}

// PurgeDeletedDocument removes the original bytes and cascading snapshots
// after an authorized native deletion. Retention and backup policy are set by
// the caller. An active or deleting native row must first finish deletion.
func (s *Store) PurgeDeletedDocument(ctx context.Context, scope Scope, documentID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM domain_engine.originals o
		WHERE o.tenant_id=$1 AND o.kb_id=$2 AND o.document_id=$3
		AND NOT EXISTS (SELECT 1 FROM knowledges k
			WHERE k.id=$3 AND k.tenant_id=$1 AND k.deleted_at IS NULL)`,
		scope.TenantID, scope.KBID, documentID)
	return err
}
