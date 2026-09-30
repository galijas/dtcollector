package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dtcollector/internal/hardware"
)

// hwSchema is migration 3: the HW Validation list, seeded from the datasheet.
const hwSchema = `CREATE TABLE hw_parts (
		id               INTEGER PRIMARY KEY,
		category         TEXT NOT NULL,
		name             TEXT NOT NULL,
		name_key         TEXT NOT NULL,
		status           TEXT NOT NULL,
		comment          TEXT NOT NULL DEFAULT '',
		attrs            TEXT NOT NULL DEFAULT '{}',
		aliases          TEXT NOT NULL DEFAULT '[]',
		source           TEXT NOT NULL,
		source_report_id TEXT REFERENCES reports(id) ON DELETE SET NULL,
		created_by       TEXT NOT NULL DEFAULT '',
		created_at       TEXT NOT NULL,
		updated_by       TEXT NOT NULL DEFAULT '',
		updated_at       TEXT NOT NULL,
		UNIQUE(category, name_key)
	);
	CREATE INDEX hw_parts_category ON hw_parts(category, name);`

// ErrDuplicatePart means a part with the same (normalized) name or alias exists.
type ErrDuplicatePart struct{ Name string }

func (e *ErrDuplicatePart) Error() string { return "already in the list as " + e.Name }

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func seedHardware(tx *sql.Tx) error {
	parts, err := hardware.Seed()
	if err != nil {
		return err
	}
	for i := range parts {
		if _, err := insertPart(tx, &parts[i]); err != nil {
			return fmt.Errorf("seed %s %q: %w", parts[i].Category, parts[i].Name, err)
		}
	}
	return nil
}

// seedSWAnalytics (migration 4) adds the SW Analytics list. A part already
// listed keeps its row and source; it gains the SW Analytics names as aliases
// and its SW Analytics count in the comment.
func seedSWAnalytics(tx *sql.Tx) error {
	parts, err := hardware.SWAnalytics()
	if err != nil {
		return err
	}
	indexes := map[string]map[string]*hardware.Part{}
	for i := range parts {
		p := &parts[i]
		idx, ok := indexes[p.Category]
		if !ok {
			if idx, err = keyIndex(tx, p.Category); err != nil {
				return err
			}
			indexes[p.Category] = idx
		}
		if existing := conflict(idx, p); existing != nil {
			var comment string
			if err := tx.QueryRow(`SELECT comment FROM hw_parts WHERE id = ?`, existing.ID).Scan(&comment); err != nil {
				return err
			}
			for _, a := range p.Aliases {
				existing.Aliases = appendUnique(existing.Aliases, a)
			}
			if c := swCount(p.Comment); c != "" && !strings.Contains(comment, c) {
				comment = strings.TrimSpace(comment + " Also in " + c)
			}
			aliases, _ := json.Marshal(existing.Aliases)
			if _, err := tx.Exec(`UPDATE hw_parts SET aliases = ?, comment = ? WHERE id = ?`, string(aliases), comment, existing.ID); err != nil {
				return err
			}
			continue
		}
		if _, err := insertPart(tx, p); err != nil {
			return fmt.Errorf("seed SW Analytics %s %q: %w", p.Category, p.Name, err)
		}
		for _, k := range p.Keys() {
			idx[k] = p
		}
	}
	return nil
}

// updateSWAnalytics (migration 5) removes SW Analytics parts that were taken
// out of the list (desktop CPUs and boards) and adds the new ones (NVMe
// drives); parts already present are left as they are.
func updateSWAnalytics(tx *sql.Tx) error {
	removed, err := hardware.SWAnalyticsRemoved()
	if err != nil {
		return err
	}
	for _, p := range removed {
		if _, err := tx.Exec(`DELETE FROM hw_parts WHERE source = ? AND category = ? AND name_key = ?`,
			hardware.SourceSWAnalytics, p.Category, hardware.Key(p.Category, p.Name)); err != nil {
			return err
		}
	}
	return seedSWAnalytics(tx)
}

// swCount picks the "SW Analytics count: ..." sentence out of a comment.
func swCount(comment string) string {
	if i := strings.Index(comment, "SW Analytics count:"); i >= 0 {
		return comment[i:]
	}
	return ""
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

func insertPart(x execer, p *hardware.Part) (int64, error) {
	t := now()
	if p.CreatedAt == "" {
		p.CreatedAt = t
	}
	p.UpdatedAt = t
	attrs, _ := json.Marshal(p.Attrs)
	aliases, _ := json.Marshal(nonNil(p.Aliases))
	var rep any
	if p.SourceReportID != "" {
		rep = p.SourceReportID
	}
	res, err := x.Exec(`INSERT INTO hw_parts(category, name, name_key, status, comment, attrs, aliases, source,
		source_report_id, created_by, created_at, updated_by, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Category, p.Name, hardware.Key(p.Category, p.Name), p.Status, p.Comment, string(attrs), string(aliases),
		p.Source, rep, p.CreatedBy, p.CreatedAt, p.UpdatedBy, p.UpdatedAt)
	if err != nil {
		return 0, err
	}
	p.ID, err = res.LastInsertId()
	return p.ID, err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// HWPart is a list row with its source report's details, for the Source column.
type HWPart struct {
	hardware.Part
	ReportCreatedAt string `json:"report_created_at,omitempty"`
	ReportKeyName   string `json:"report_key_name,omitempty"`
}

const hwCols = `p.id, p.category, p.name, p.status, p.comment, p.attrs, p.aliases, p.source,
	COALESCE(p.source_report_id, ''), p.created_by, p.created_at, p.updated_by, p.updated_at,
	COALESCE(r.created_at, ''), COALESCE(k.name, '')`

const hwFrom = ` FROM hw_parts p LEFT JOIN reports r ON r.id = p.source_report_id
	LEFT JOIN upload_keys k ON k.id = r.upload_key_id`

func scanPart(row interface{ Scan(...any) error }) (*HWPart, error) {
	var p HWPart
	var attrs, aliases string
	err := row.Scan(&p.ID, &p.Category, &p.Name, &p.Status, &p.Comment, &attrs, &aliases, &p.Source,
		&p.SourceReportID, &p.CreatedBy, &p.CreatedAt, &p.UpdatedBy, &p.UpdatedAt, &p.ReportCreatedAt, &p.ReportKeyName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(attrs), &p.Attrs)
	json.Unmarshal([]byte(aliases), &p.Aliases)
	return &p, nil
}

func (s *Store) ListParts() ([]HWPart, error) {
	rows, err := s.db.Query(`SELECT ` + hwCols + hwFrom + ` ORDER BY p.category, p.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HWPart
	for rows.Next() {
		p, err := scanPart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) GetPart(id int64) (*HWPart, error) {
	return scanPart(s.db.QueryRow(`SELECT `+hwCols+hwFrom+` WHERE p.id = ?`, id))
}

// keyIndex maps each match key of a category's parts to the part.
func keyIndex(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, category string) (map[string]*hardware.Part, error) {
	rows, err := q.Query(`SELECT id, category, name, attrs, aliases, source FROM hw_parts WHERE category = ?`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]*hardware.Part{}
	for rows.Next() {
		p := &hardware.Part{}
		var attrs, aliases string
		if err := rows.Scan(&p.ID, &p.Category, &p.Name, &attrs, &aliases, &p.Source); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(attrs), &p.Attrs)
		json.Unmarshal([]byte(aliases), &p.Aliases)
		for _, k := range p.Keys() {
			if _, taken := idx[k]; !taken {
				idx[k] = p
			}
		}
	}
	return idx, rows.Err()
}

func conflict(idx map[string]*hardware.Part, p *hardware.Part) *hardware.Part {
	for _, k := range p.Keys() {
		if other, ok := idx[k]; ok && other.ID != p.ID {
			return other
		}
	}
	return nil
}

// CreatePart adds a manually entered part.
func (s *Store) CreatePart(p *hardware.Part) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	idx, err := keyIndex(tx, p.Category)
	if err != nil {
		return err
	}
	if other := conflict(idx, p); other != nil {
		return &ErrDuplicatePart{other.Name}
	}
	if _, err := insertPart(tx, p); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdatePart saves an edited part; its source and aliases are kept.
func (s *Store) UpdatePart(p *hardware.Part) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var aliases string
	if err := tx.QueryRow(`SELECT aliases FROM hw_parts WHERE id = ?`, p.ID).Scan(&aliases); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	json.Unmarshal([]byte(aliases), &p.Aliases)
	idx, err := keyIndex(tx, p.Category)
	if err != nil {
		return err
	}
	if other := conflict(idx, p); other != nil {
		return &ErrDuplicatePart{other.Name}
	}
	attrs, _ := json.Marshal(p.Attrs)
	if _, err := tx.Exec(`UPDATE hw_parts SET category = ?, name = ?, name_key = ?, status = ?, comment = ?, attrs = ?,
		updated_by = ?, updated_at = ? WHERE id = ?`, p.Category, p.Name, hardware.Key(p.Category, p.Name), p.Status,
		p.Comment, string(attrs), p.UpdatedBy, now(), p.ID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return &ErrDuplicatePart{p.Name}
		}
		return err
	}
	return tx.Commit()
}

func (s *Store) DeletePart(id int64) error {
	res, err := s.db.Exec(`DELETE FROM hw_parts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddDiscoveredParts records the parts found in a report. Parts already in the
// list (by name or alias) are not added again; a NIC's newly seen port count,
// and empty speed, driver or drive type, are filled in on the existing entry.
// It returns the number of parts added.
func (s *Store) AddDiscoveredParts(parts []hardware.Part, source, reportID string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	indexes := map[string]map[string]*hardware.Part{}
	for i := range parts {
		p := parts[i]
		idx, ok := indexes[p.Category]
		if !ok {
			if idx, err = keyIndex(tx, p.Category); err != nil {
				return 0, err
			}
			indexes[p.Category] = idx
		}
		if existing := conflict(idx, &p); existing != nil {
			if mergeAttrs(&existing.Attrs, p.Attrs) {
				attrs, _ := json.Marshal(existing.Attrs)
				if _, err := tx.Exec(`UPDATE hw_parts SET attrs = ? WHERE id = ?`, string(attrs), existing.ID); err != nil {
					return 0, err
				}
			}
			continue
		}
		p.Source, p.SourceReportID = source, reportID
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		if _, err := insertPart(tx, &p); err != nil {
			return 0, err
		}
		for _, k := range p.Keys() {
			idx[k] = &p
		}
		added++
	}
	return added, tx.Commit()
}

func mergeAttrs(dst *hardware.Attrs, src hardware.Attrs) bool {
	changed := false
	for _, n := range src.Ports {
		before := len(dst.Ports)
		dst.Ports = hardware.AddPort(dst.Ports, n)
		changed = changed || len(dst.Ports) != before
	}
	for _, f := range []struct {
		d *string
		s string
	}{{&dst.Speed, src.Speed}, {&dst.Driver, src.Driver}, {&dst.Type, src.Type}} {
		if *f.d == "" && f.s != "" {
			*f.d, changed = f.s, true
		}
	}
	return changed
}
