package store

import (
	"database/sql"
	"path/filepath"
	"strings"
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

// Migration 5 on a database that got the first SW Analytics list: the
// removed desktop parts go, manual entries of the same name stay.
func TestUpdateSWAnalyticsRemoves(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, src := range []string{"sw_analytics", "manual"} {
		cat := "cpu"
		name := "AMD Ryzen 7 5800X"
		if src == "manual" {
			cat, name = "server_model", "ASUS PRIME B760M-A D4"
		}
		if _, err := st.db.Exec(`INSERT INTO hw_parts(category, name, name_key, status, source, created_at, updated_at)
			VALUES(?, ?, ?, 'supported', ?, '', '')`, cat, name, strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(name)), src); err != nil {
			t.Fatal(err)
		}
	}
	tx, _ := st.db.Begin()
	if err := updateSWAnalytics(tx); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM hw_parts WHERE name = 'AMD Ryzen 7 5800X'`).Scan(&n)
	if n != 0 {
		t.Error("SW Analytics Ryzen entry not removed")
	}
	st.db.QueryRow(`SELECT COUNT(*) FROM hw_parts WHERE name = 'ASUS PRIME B760M-A D4'`).Scan(&n)
	if n != 1 {
		t.Error("a manual entry must not be removed")
	}
}
