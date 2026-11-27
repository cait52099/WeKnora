package domainengine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrConflict    = errors.New("domain engine version conflict")
	ErrStaleSource = errors.New("domain engine source is no longer current")
)

// Store is an internal persistence port. Callers must authorize the tenant and
// KB before invoking it. It must not be exposed directly to HTTP or Agent tools.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

type Scope struct {
	TenantID uint64
	KBID     string
}

type Binding struct {
	Scope
	Version   int64
	ProfileID string
	Revision  string
	Enabled   bool
}

func (s *Store) Publish(ctx context.Context, tenant uint64, p *CompiledProfile) error {
	if tenant == 0 || p == nil || len(p.raw) == 0 {
		return errors.New("tenant and compiled profile are required")
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO domain_engine.profile_revisions
		(tenant_id, profile_id, revision, sha256, manifest) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT DO NOTHING`, tenant, p.profile.ID, p.profile.Revision, p.Fingerprint(), p.raw)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 1 {
		return err
	}
	var storedHash string
	err = s.db.QueryRowContext(ctx, `SELECT sha256 FROM domain_engine.profile_revisions
		WHERE tenant_id=$1 AND profile_id=$2 AND revision=$3`, tenant, p.profile.ID, p.profile.Revision).Scan(&storedHash)
	if err == nil && storedHash != p.Fingerprint() {
		return ErrConflict
	}
	return err
}

func (s *Store) LoadProfile(ctx context.Context, tenant uint64, id, revision string) (*CompiledProfile, error) {
	var raw []byte
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT manifest, sha256 FROM domain_engine.profile_revisions
		WHERE tenant_id=$1 AND profile_id=$2 AND revision=$3`, tenant, id, revision).Scan(&raw, &hash)
	if err != nil {
		return nil, err
	}
	if digest(raw) != hash {
		return nil, errors.New("domain profile digest mismatch")
	}
	return ImportProfile(bytes.NewReader(raw))
}

// Bind inserts at expected version zero or changes an existing binding by
// compare-and-swap. Disabling advances the version and preserves history.
func (s *Store) Bind(ctx context.Context, binding Binding, expected int64) (int64, error) {
	var version int64
	var err error
	if expected == 0 {
		err = s.db.QueryRowContext(ctx, `INSERT INTO domain_engine.kb_bindings
			(tenant_id,kb_id,version,profile_id,revision,enabled)
			SELECT tenant_id,id,1,$3,$4,$5 FROM knowledge_bases
			WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NULL
			ON CONFLICT DO NOTHING RETURNING version`,
			binding.TenantID, binding.KBID, binding.ProfileID, binding.Revision, binding.Enabled).Scan(&version)
	} else {
		err = s.db.QueryRowContext(ctx, `UPDATE domain_engine.kb_bindings b SET
			version=b.version+1, profile_id=$3, revision=$4, enabled=$5 FROM knowledge_bases k
			WHERE b.tenant_id=$1 AND b.kb_id=$2 AND b.version=$6
			AND k.id=b.kb_id AND k.tenant_id=b.tenant_id AND k.deleted_at IS NULL
			RETURNING b.version`, binding.TenantID, binding.KBID, binding.ProfileID,
			binding.Revision, binding.Enabled, expected).Scan(&version)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrConflict
	}
	return version, err
}

// ActiveBinding returns nil for an ordinary KB and for a disabled binding.
func (s *Store) ActiveBinding(ctx context.Context, scope Scope) (*Binding, error) {
	if s == nil {
		return nil, nil
	}
	binding := &Binding{Scope: scope}
	err := s.db.QueryRowContext(ctx, `SELECT version, profile_id, revision, enabled
		FROM domain_engine.kb_bindings WHERE tenant_id=$1 AND kb_id=$2 AND enabled`,
		scope.TenantID, scope.KBID).Scan(&binding.Version, &binding.ProfileID, &binding.Revision, &binding.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load domain binding: %w", err)
	}
	return binding, nil
}
