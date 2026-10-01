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

func TestRemoveVirtualParts(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, r := range [][3]string{{"Virtual CD", "swhw", "virtualcd"}, {"Virtual Floppy", "test_script", "virtualfloppy"},
		{"Real SSD", "swhw", "realssd"}, {"Virtual Lab Box", "manual", "virtuallabbox"}} {
		st.db.Exec(`INSERT INTO hw_parts(category, name, name_key, status, source, created_at, updated_at)
			VALUES('drive', ?, ?, 'supported', ?, '', '')`, r[0], r[2], r[1])
	}
	tx, _ := st.db.Begin()
	if err := removeVirtualParts(tx); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	var left []string
	rows, _ := st.db.Query(`SELECT name FROM hw_parts WHERE category = 'drive' AND name IN ('Virtual CD', 'Virtual Floppy', 'Real SSD', 'Virtual Lab Box') ORDER BY name`)
	for rows.Next() {
		var n string
		rows.Scan(&n)
		left = append(left, n)
	}
	rows.Close()
	if strings.Join(left, ",") != "Real SSD,Virtual Lab Box" {
		t.Errorf("left: %v", left)
	}
}

func TestRemoveSkippedControllers(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, r := range [][3]string{
		{"Intel C620 Series Chipset Family SATA Controller [AHCI mode]", "test_script", "k1"},
		{"Avocent Mass Storage Function", "swhw", "k2"},
		{"Broadcom / LSI MegaRAID SAS-3 3108 [Invader]", "swhw", "k3"},
		{"Intel C610 chipset sSATA Controller [AHCI mode] (kept by hand)", "manual", "k4"},
	} {
		st.db.Exec(`INSERT INTO hw_parts(category, name, name_key, status, source, created_at, updated_at)
			VALUES('storage_controller', ?, ?, 'supported', ?, '', '')`, r[0], r[2], r[1])
	}
	tx, _ := st.db.Begin()
	if err := removeSkippedControllers(tx); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	var n int
	st.db.QueryRow(`SELECT COUNT(*) FROM hw_parts WHERE name_key IN ('k1','k2','k3','k4')`).Scan(&n)
	var kept string
	st.db.QueryRow(`SELECT group_concat(name_key) FROM (SELECT name_key FROM hw_parts WHERE name_key IN ('k1','k2','k3','k4') ORDER BY name_key)`).Scan(&kept)
	if kept != "k3,k4" {
		t.Errorf("kept %q, want k3 (RAID card) and k4 (manual)", kept)
	}
}
