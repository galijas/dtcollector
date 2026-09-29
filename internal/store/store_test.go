package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// A database created before migration 2 gets kind/source columns backfilled.
func TestMigrationBackfill(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0] + `; PRAGMA user_version = 1;`); err != nil {
		t.Fatal(err)
	}
	for _, r := range [][]any{
		{"00000000-0000-4000-a000-000000000001", "standard", "1.4.0"},
		{"00000000-0000-4000-a000-000000000002", "hardware", "1.5.0"},
	} {
		_, err := db.Exec(`INSERT INTO reports(id, received_at, created_at, schema_version, profile_name, profile_version,
			swarmdialer_version, serverware_version, serverware_edition, cpu_model, cpu_sockets, cpu_cores, cpu_threads,
			memory_bytes, disks, pbxware, test_count, results_json, body_size, body_gz)
			VALUES(?, '2026-09-28T00:00:00Z', '2026-09-28T00:00:00Z', 1, ?, 1, ?, '5.2.1', '', 'cpu', 1, 1, 1, 1, '', '', 0, 'null', 0, x'')`,
			r[0], r[1], r[2])
		if err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.ListReports(ReportFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ReportRow{}
	for _, r := range rows {
		got[r.ProfileName] = r
	}
	if r := got["standard"]; r.Kind != "benchmark" || r.SourceName != "swarmdialer" || r.SourceVersion != "1.4.0" {
		t.Errorf("standard row: %+v", r)
	}
	if r := got["hardware"]; r.Kind != "hardware" || r.SourceLabel() != "SwarmDialer 1.5.0" {
		t.Errorf("hardware row: kind %q label %q", r.Kind, r.SourceLabel())
	}
}
