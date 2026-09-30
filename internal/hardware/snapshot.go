package hardware

import (
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

// The starter hardware list a new database is seeded with: an export of the
// live list (`dtcollector export-hardware`), which began as the Supported
// Hardware datasheet and SW Analytics imports and includes the admins' edits.
// To update it, export the live list over this file and rebuild.
//
//go:embed seed/hardware-list.json
var seedFS embed.FS

const SnapshotFormat = "dtcollector-hardware-snapshot"

type Snapshot struct {
	Format        string         `json:"format"`
	FormatVersion int            `json:"format_version"`
	ExportedAt    string         `json:"exported_at"`
	Parts         []SnapshotPart `json:"parts"`
}

// SnapshotPart is a list entry without its database ID or source report
// (reports aren't part of a new installation).
type SnapshotPart struct {
	Category  string   `json:"category"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Comment   string   `json:"comment"`
	Attrs     Attrs    `json:"attrs"`
	Aliases   []string `json:"aliases"`
	Source    string   `json:"source"`
	CreatedBy string   `json:"created_by"`
	CreatedAt string   `json:"created_at"`
	UpdatedBy string   `json:"updated_by"`
	UpdatedAt string   `json:"updated_at"`
}

// StarterList returns the parts a new database starts with.
func StarterList() ([]Part, error) {
	b, err := seedFS.ReadFile("seed/hardware-list.json")
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("hardware-list.json: %w", err)
	}
	if s.Format != SnapshotFormat || s.FormatVersion != 1 {
		return nil, fmt.Errorf("hardware-list.json: unsupported format %q v%d", s.Format, s.FormatVersion)
	}
	out := make([]Part, 0, len(s.Parts))
	for i, p := range s.Parts {
		if !ValidCategory(p.Category) || !ValidStatus(p.Status) || p.Name == "" {
			return nil, fmt.Errorf("hardware-list.json: part %d (%q) has an invalid category, status or name", i, p.Name)
		}
		out = append(out, Part{Category: p.Category, Name: p.Name, Status: p.Status, Comment: p.Comment,
			Attrs: p.Attrs, Aliases: p.Aliases, Source: p.Source, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt,
			UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt})
	}
	return out, nil
}

// NewSnapshot builds the export of a list.
func NewSnapshot(parts []Part) Snapshot {
	s := Snapshot{Format: SnapshotFormat, FormatVersion: 1, ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Parts: make([]SnapshotPart, 0, len(parts))}
	for _, p := range parts {
		aliases := p.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		s.Parts = append(s.Parts, SnapshotPart{Category: p.Category, Name: p.Name, Status: p.Status, Comment: p.Comment,
			Attrs: p.Attrs, Aliases: aliases, Source: p.Source, CreatedBy: p.CreatedBy, CreatedAt: p.CreatedAt,
			UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt})
	}
	return s
}
