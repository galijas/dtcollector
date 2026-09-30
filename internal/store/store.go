// Package store keeps admins, sessions, upload keys and reports in SQLite.
package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"dtcollector/internal/report"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrExists    = errors.New("already exists")
	ErrLastAdmin = errors.New("cannot delete the last admin account")
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{
	`CREATE TABLE admins (
		id            INTEGER PRIMARY KEY,
		username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
		password_hash TEXT NOT NULL,
		created_at    TEXT NOT NULL,
		last_login_at TEXT
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		admin_id   INTEGER NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL
	);
	CREATE TABLE upload_keys (
		id           INTEGER PRIMARY KEY,
		name         TEXT NOT NULL,
		prefix       TEXT NOT NULL,
		key_hash     TEXT NOT NULL UNIQUE,
		created_at   TEXT NOT NULL,
		created_by   TEXT NOT NULL,
		last_used_at TEXT,
		revoked_at   TEXT
	);
	CREATE TABLE reports (
		id                  TEXT PRIMARY KEY,
		received_at         TEXT NOT NULL,
		created_at          TEXT NOT NULL,
		upload_key_id       INTEGER REFERENCES upload_keys(id) ON DELETE SET NULL,
		schema_version      INTEGER NOT NULL,
		profile_name        TEXT NOT NULL,
		profile_version     INTEGER NOT NULL,
		swarmdialer_version TEXT NOT NULL,
		serverware_version  TEXT NOT NULL,
		serverware_edition  TEXT NOT NULL,
		cpu_model           TEXT NOT NULL,
		cpu_sockets         INTEGER NOT NULL,
		cpu_cores           INTEGER NOT NULL,
		cpu_threads         INTEGER NOT NULL,
		memory_bytes        INTEGER NOT NULL,
		disks               TEXT NOT NULL,
		pbxware             TEXT NOT NULL,
		test_count          INTEGER NOT NULL,
		results_json        TEXT NOT NULL,
		note                TEXT NOT NULL DEFAULT '',
		body_size           INTEGER NOT NULL,
		body_gz             BLOB NOT NULL
	);
	CREATE INDEX reports_created ON reports(created_at);
	CREATE INDEX reports_profile ON reports(profile_name, profile_version);`,

	`ALTER TABLE reports ADD COLUMN kind TEXT NOT NULL DEFAULT 'benchmark';
	ALTER TABLE reports ADD COLUMN source_name TEXT NOT NULL DEFAULT 'swarmdialer';
	ALTER TABLE reports ADD COLUMN source_version TEXT NOT NULL DEFAULT '';
	ALTER TABLE reports ADD COLUMN system_vendor TEXT NOT NULL DEFAULT '';
	ALTER TABLE reports ADD COLUMN system_model TEXT NOT NULL DEFAULT '';
	UPDATE reports SET source_version = swarmdialer_version;
	UPDATE reports SET kind = 'hardware' WHERE profile_name = 'hardware';
	CREATE INDEX reports_kind ON reports(kind, created_at);`,

	hwSchema,
}

// migrationHooks run after a migration's SQL, in the same transaction.
var migrationHooks = map[int]func(*sql.Tx) error{
	2: seedHardware,
}

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return err
		}
		if hook := migrationHooks[i]; hook != nil {
			if err := hook(tx); err != nil {
				tx.Rollback()
				return err
			}
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func parseT(s sql.NullString) time.Time {
	if !s.Valid {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s.String)
	return t
}

// Backup writes a consistent copy of the database to dest (which must not exist).
func (s *Store) Backup(dest string) error {
	_, err := s.db.Exec(`VACUUM INTO ?`, dest)
	return err
}

// ---- admins ----

type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	LastLoginAt  time.Time
}

func (s *Store) CreateAdmin(username, hash string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO admins(username, password_hash, created_at) VALUES(?,?,?)`, username, hash, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, ErrExists
		}
		return 0, err
	}
	return res.LastInsertId()
}

const adminCols = `id, username, password_hash, created_at, last_login_at`

func scanAdmin(row interface{ Scan(...any) error }) (*Admin, error) {
	var a Admin
	var created, last sql.NullString
	if err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &created, &last); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	a.CreatedAt, a.LastLoginAt = parseT(created), parseT(last)
	return &a, nil
}

func (s *Store) AdminByUsername(username string) (*Admin, error) {
	return scanAdmin(s.db.QueryRow(`SELECT `+adminCols+` FROM admins WHERE username = ?`, username))
}

func (s *Store) AdminByID(id int64) (*Admin, error) {
	return scanAdmin(s.db.QueryRow(`SELECT `+adminCols+` FROM admins WHERE id = ?`, id))
}

func (s *Store) ListAdmins() ([]Admin, error) {
	rows, err := s.db.Query(`SELECT ` + adminCols + ` FROM admins ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		a, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// SetAdminPassword replaces the hash and ends all of that admin's sessions.
func (s *Store) SetAdminPassword(id int64, hash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE admins SET password_hash = ? WHERE id = ?`, hash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE admin_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteAdmin(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	res, err := tx.Exec(`DELETE FROM admins WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Store) TouchAdminLogin(id int64) error {
	_, err := s.db.Exec(`UPDATE admins SET last_login_at = ? WHERE id = ?`, now(), id)
	return err
}

// ---- sessions ----

func (s *Store) CreateSession(tokenHash string, adminID int64, ttl time.Duration) error {
	t := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash, admin_id, created_at, expires_at) VALUES(?,?,?,?)`,
		tokenHash, adminID, t.Format(time.RFC3339), t.Add(ttl).Format(time.RFC3339))
	return err
}

// SessionAdmin returns the admin owning an unexpired session.
func (s *Store) SessionAdmin(tokenHash string) (*Admin, error) {
	return scanAdmin(s.db.QueryRow(`SELECT a.id, a.username, a.password_hash, a.created_at, a.last_login_at
		FROM sessions s JOIN admins a ON a.id = s.admin_id
		WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, now()))
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) PurgeExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now())
	return err
}

// ---- upload keys ----

type UploadKey struct {
	ID         int64
	Name       string
	Prefix     string
	CreatedAt  time.Time
	CreatedBy  string
	LastUsedAt time.Time
	RevokedAt  time.Time
	Reports    int
}

func (k UploadKey) Revoked() bool { return !k.RevokedAt.IsZero() }

func (s *Store) CreateUploadKey(name, prefix, hash, createdBy string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO upload_keys(name, prefix, key_hash, created_at, created_by) VALUES(?,?,?,?,?)`,
		name, prefix, hash, now(), createdBy)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ActiveUploadKey looks up a non-revoked key by hash and records its use.
func (s *Store) ActiveUploadKey(hash string) (*UploadKey, error) {
	var k UploadKey
	var created, last sql.NullString
	err := s.db.QueryRow(`SELECT id, name, prefix, created_at, created_by, last_used_at
		FROM upload_keys WHERE key_hash = ? AND revoked_at IS NULL`, hash).
		Scan(&k.ID, &k.Name, &k.Prefix, &created, &k.CreatedBy, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	k.CreatedAt, k.LastUsedAt = parseT(created), parseT(last)
	if _, err := s.db.Exec(`UPDATE upload_keys SET last_used_at = ? WHERE id = ?`, now(), k.ID); err != nil {
		return nil, err
	}
	return &k, nil
}

func (s *Store) ListUploadKeys() ([]UploadKey, error) {
	rows, err := s.db.Query(`SELECT k.id, k.name, k.prefix, k.created_at, k.created_by, k.last_used_at, k.revoked_at,
		(SELECT COUNT(*) FROM reports r WHERE r.upload_key_id = k.id)
		FROM upload_keys k ORDER BY k.revoked_at IS NOT NULL, k.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UploadKey
	for rows.Next() {
		var k UploadKey
		var created, last, revoked sql.NullString
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &created, &k.CreatedBy, &last, &revoked, &k.Reports); err != nil {
			return nil, err
		}
		k.CreatedAt, k.LastUsedAt, k.RevokedAt = parseT(created), parseT(last), parseT(revoked)
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) RevokeUploadKey(id int64) error {
	res, err := s.db.Exec(`UPDATE upload_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- reports ----

// InsertReport stores a validated report. It returns false (and no error)
// when a report with the same report_id already exists.
func (s *Store) InsertReport(sum report.Summary, raw []byte, keyID int64) (bool, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return false, err
	}
	if err := zw.Close(); err != nil {
		return false, err
	}
	results, _ := json.Marshal(sum.Results)
	res, err := s.db.Exec(`INSERT INTO reports(id, received_at, created_at, upload_key_id, schema_version,
		profile_name, profile_version, swarmdialer_version, serverware_version, serverware_edition,
		cpu_model, cpu_sockets, cpu_cores, cpu_threads, memory_bytes, disks, pbxware, test_count,
		results_json, body_size, body_gz, kind, source_name, source_version, system_vendor, system_model)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`,
		sum.ReportID, now(), sum.CreatedAt, keyID, sum.SchemaVersion,
		sum.ProfileName, sum.ProfileVersion, sum.SwarmDialerVersion, sum.ServerwareVersion, sum.ServerwareEdition,
		sum.CPUModel, sum.CPUSockets, sum.CPUCores, sum.CPUThreads, sum.MemoryBytes, sum.Disks, sum.PBXware,
		sum.TestCount, string(results), len(raw), buf.Bytes(),
		sum.Kind, sum.SourceName, sum.SourceVersion, sum.SystemVendor, sum.SystemModel)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

type ReportRow struct {
	ID                 string
	Kind               string
	SourceName         string
	SourceVersion      string
	SystemVendor       string
	SystemModel        string
	ReceivedAt         time.Time
	CreatedAt          time.Time
	KeyName            string
	ProfileName        string
	ProfileVersion     int
	SwarmDialerVersion string
	ServerwareVersion  string
	ServerwareEdition  string
	CPUModel           string
	CPUSockets         int
	CPUCores           int
	CPUThreads         int
	MemoryBytes        int64
	Disks              string
	PBXware            string
	TestCount          int
	Results            []report.TestResult
	Note               string
	BodySize           int64
}

func (r ReportRow) Profile() string { return fmt.Sprintf("%s v%d", r.ProfileName, r.ProfileVersion) }

func (r ReportRow) IsHardware() bool { return r.Kind == "hardware" }

// SourceLabel names the uploading tool: "SwarmDialer 1.4.0", "hw-collect 1.0.0".
func (r ReportRow) SourceLabel() string {
	name := r.SourceName
	if name == "swarmdialer" {
		name = "SwarmDialer"
	}
	return strings.TrimSpace(name + " " + r.SourceVersion)
}

// System is "vendor model", e.g. "Supermicro SYS-6029BT-DNC0R".
func (r ReportRow) System() string { return strings.TrimSpace(r.SystemVendor + " " + r.SystemModel) }

type ReportFilter struct {
	Kind           string
	ProfileName    string
	ProfileVersion int
	Serverware     string
	PBXware        string
	KeyID          int64
	Text           string // matched against CPU model, disks, note and key name
	From, To       time.Time
	IDs            []string
	Limit          int
}

const reportCols = `r.id, r.received_at, r.created_at, COALESCE(k.name, ''), r.profile_name, r.profile_version,
	r.swarmdialer_version, r.serverware_version, r.serverware_edition, r.cpu_model, r.cpu_sockets,
	r.cpu_cores, r.cpu_threads, r.memory_bytes, r.disks, r.pbxware, r.test_count, r.results_json,
	r.note, r.body_size, r.kind, r.source_name, r.source_version, r.system_vendor, r.system_model`

func (s *Store) ListReports(f ReportFilter) ([]ReportRow, error) {
	var where []string
	var args []any
	if f.Kind != "" {
		where, args = append(where, "r.kind = ?"), append(args, f.Kind)
	}
	if f.ProfileName != "" {
		where, args = append(where, "r.profile_name = ?"), append(args, f.ProfileName)
	}
	if f.ProfileVersion > 0 {
		where, args = append(where, "r.profile_version = ?"), append(args, f.ProfileVersion)
	}
	if f.Serverware != "" {
		where, args = append(where, "r.serverware_version = ?"), append(args, f.Serverware)
	}
	if f.PBXware != "" {
		where, args = append(where, "r.pbxware LIKE ? ESCAPE '\\'"), append(args, "%"+likeEscape(f.PBXware)+"%")
	}
	if f.KeyID > 0 {
		where, args = append(where, "r.upload_key_id = ?"), append(args, f.KeyID)
	}
	if f.Text != "" {
		p := "%" + likeEscape(f.Text) + "%"
		where = append(where, `(r.cpu_model LIKE ? ESCAPE '\' OR r.disks LIKE ? ESCAPE '\' OR r.note LIKE ? ESCAPE '\' OR k.name LIKE ? ESCAPE '\'
			OR r.system_vendor LIKE ? ESCAPE '\' OR r.system_model LIKE ? ESCAPE '\')`)
		args = append(args, p, p, p, p, p, p)
	}
	if !f.From.IsZero() {
		where, args = append(where, "r.created_at >= ?"), append(args, f.From.UTC().Format(time.RFC3339))
	}
	if !f.To.IsZero() {
		where, args = append(where, "r.created_at < ?"), append(args, f.To.UTC().Format(time.RFC3339))
	}
	if len(f.IDs) > 0 {
		where = append(where, "r.id IN (?"+strings.Repeat(",?", len(f.IDs)-1)+")")
		for _, id := range f.IDs {
			args = append(args, id)
		}
	}
	q := `SELECT ` + reportCols + ` FROM reports r LEFT JOIN upload_keys k ON k.id = r.upload_key_id`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY r.created_at DESC"
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReportRow
	for rows.Next() {
		r, err := scanReport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func scanReport(row interface{ Scan(...any) error }) (*ReportRow, error) {
	var r ReportRow
	var received, created sql.NullString
	var results string
	err := row.Scan(&r.ID, &received, &created, &r.KeyName, &r.ProfileName, &r.ProfileVersion,
		&r.SwarmDialerVersion, &r.ServerwareVersion, &r.ServerwareEdition, &r.CPUModel, &r.CPUSockets,
		&r.CPUCores, &r.CPUThreads, &r.MemoryBytes, &r.Disks, &r.PBXware, &r.TestCount, &results,
		&r.Note, &r.BodySize, &r.Kind, &r.SourceName, &r.SourceVersion, &r.SystemVendor, &r.SystemModel)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.ReceivedAt, r.CreatedAt = parseT(received), parseT(created)
	_ = json.Unmarshal([]byte(results), &r.Results)
	return &r, nil
}

func (s *Store) GetReport(id string) (*ReportRow, error) {
	return scanReport(s.db.QueryRow(`SELECT `+reportCols+` FROM reports r
		LEFT JOIN upload_keys k ON k.id = r.upload_key_id WHERE r.id = ?`, id))
}

// ReportJSON returns the report exactly as uploaded.
func (s *Store) ReportJSON(ctx context.Context, id string) ([]byte, error) {
	var gz []byte
	err := s.db.QueryRowContext(ctx, `SELECT body_gz FROM reports WHERE id = ?`, id).Scan(&gz)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

func (s *Store) SetReportNote(id, note string) error {
	res, err := s.db.Exec(`UPDATE reports SET note = ? WHERE id = ?`, note, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteReport(id string) error {
	res, err := s.db.Exec(`DELETE FROM reports WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// FilterOptions lists the distinct values the report list can filter on.
type FilterOptions struct {
	Profiles   []ProfileOption
	Serverware []string
}

type ProfileOption struct {
	Name    string
	Version int
	Count   int
}

func (s *Store) FilterOptions() (*FilterOptions, error) {
	var fo FilterOptions
	rows, err := s.db.Query(`SELECT profile_name, profile_version, COUNT(*) FROM reports
		GROUP BY profile_name, profile_version ORDER BY profile_name, profile_version DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p ProfileOption
		if err := rows.Scan(&p.Name, &p.Version, &p.Count); err != nil {
			rows.Close()
			return nil, err
		}
		fo.Profiles = append(fo.Profiles, p)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT DISTINCT serverware_version FROM reports ORDER BY serverware_version DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		fo.Serverware = append(fo.Serverware, v)
	}
	return &fo, rows.Err()
}
