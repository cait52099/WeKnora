package domainengine

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

const MaxPackageBytes = 8 << 20

type ProfilePackage struct {
	Profile    *CompiledProfile  `json:"-"`
	SHA256     string            `json:"sha256"`
	Files      map[string]string `json:"files"`
	DOCXText   string            `json:"docx_text"`
	Conversion string            `json:"conversion"`
	archive    []byte
}

func ImportPackage(raw []byte) (*ProfilePackage, error) {
	if len(raw) > MaxPackageBytes {
		return nil, errors.New("profile package exceeds size limit")
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	names := []string{"manifest.json", "original/flow1.txt", "original/flow2.docx", "original/flow1.json", "original/flow2.json"}
	allowed := make(map[string]bool)
	for _, name := range names {
		allowed[name] = true
	}
	files := make(map[string][]byte)
	hashes := make(map[string]string)
	for _, entry := range reader.File {
		if !allowed[entry.Name] || files[entry.Name] != nil || !entry.Mode().IsRegular() || entry.UncompressedSize64 > 1<<20 {
			return nil, errors.New("invalid profile package member")
		}
		file, err := entry.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		file.Close()
		if err != nil || len(body) == 0 || len(body) > 1<<20 {
			return nil, errors.New("invalid or oversized package file")
		}
		files[entry.Name] = body
		hashes[entry.Name] = digest(body)
	}
	if len(files) != len(names) {
		return nil, errors.New("profile package requires manifest and four original files")
	}
	profile, err := ImportProfile(bytes.NewReader(files["manifest.json"]))
	if err != nil {
		return nil, err
	}
	if _, err = PromptPreview("txt", files["original/flow1.txt"]); err != nil {
		return nil, err
	}
	text, err := PromptPreview("docx", files["original/flow2.docx"])
	if err != nil {
		return nil, err
	}
	for _, name := range names[3:] {
		if err = ValidateExample(files[name]); err != nil {
			return nil, err
		}
	}
	return &ProfilePackage{Profile: profile, SHA256: digest(raw), Files: hashes, DOCXText: text,
		Conversion: "word/document.xml paragraph text; Word formatting excluded", archive: append([]byte(nil), raw...)}, nil
}

func (s *Store) PublishPackage(ctx context.Context, tenant uint64, p *ProfilePackage) error {
	if p == nil || p.Profile == nil || tenant == 0 {
		return errors.New("compiled package required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	manifest := p.Profile
	def := manifest.profile
	record, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO domain_engine.profile_revisions(tenant_id,profile_id,revision,sha256,manifest)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, tenant, def.ID, def.Revision, manifest.Fingerprint(), manifest.raw)
	if err != nil {
		return err
	}
	var manifestHash string
	if err = tx.QueryRowContext(ctx, `SELECT sha256 FROM domain_engine.profile_revisions WHERE tenant_id=$1 AND profile_id=$2 AND revision=$3`,
		tenant, def.ID, def.Revision).Scan(&manifestHash); err != nil {
		return err
	}
	if manifestHash != manifest.Fingerprint() {
		return ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO domain_engine.profile_packages(tenant_id,profile_id,revision,archive,sha256,description)
		VALUES($1,$2,$3,$4,$5,$6::jsonb) ON CONFLICT DO NOTHING`, tenant, def.ID, def.Revision, p.archive, p.SHA256, string(record))
	if err != nil {
		return err
	}
	var archiveHash string
	if err = tx.QueryRowContext(ctx, `SELECT sha256 FROM domain_engine.profile_packages WHERE tenant_id=$1 AND profile_id=$2 AND revision=$3`, tenant, def.ID, def.Revision).Scan(&archiveHash); err != nil {
		return err
	}
	if archiveHash != p.SHA256 {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *Store) ExportPackage(ctx context.Context, tenant uint64, id, revision string) ([]byte, error) {
	var raw []byte
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT archive,sha256 FROM domain_engine.profile_packages WHERE tenant_id=$1 AND profile_id=$2 AND revision=$3`,
		tenant, id, revision).Scan(&raw, &hash)
	if err != nil {
		return nil, err
	}
	if digest(raw) != hash {
		return nil, errors.New("package checksum mismatch")
	}
	return raw, nil
}
