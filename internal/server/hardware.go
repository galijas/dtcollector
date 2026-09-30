package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"dtcollector/internal/hardware"
	"dtcollector/internal/store"
)

// canEditHardware: every account may add and edit list entries.
func canEditHardware(a *store.Admin) bool { return a != nil }

// canDeleteHardware: only Admin accounts may delete list entries.
func canDeleteHardware(a *store.Admin) bool { return a.IsAdmin() }

// partForm holds submitted values, so a rejected form reopens as entered.
type partForm struct {
	ID        int64  `json:"id,omitempty"`
	Category  string `json:"category"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Comment   string `json:"comment"`
	Speed     string `json:"speed"`
	Ports     string `json:"ports"`
	Driver    string `json:"driver"`
	DriveType string `json:"drive_type"`
}

type option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type hwPayload struct {
	Categories []hardware.Category `json:"categories"`
	Statuses   []option            `json:"statuses"`
	Sources    []option            `json:"sources"`
	DriveTypes []string            `json:"drive_types"`
	Parts      []store.HWPart      `json:"parts"`
	CanEdit    bool                `json:"can_edit"`
	CanDelete  bool                `json:"can_delete"`
	Form       *partForm           `json:"form,omitempty"`
}

type hwPageData struct {
	JSON    string
	Error   string
	CanEdit bool
}

func options(list []struct{ ID, Label string }) []option {
	out := make([]option, len(list))
	for i, x := range list {
		out[i] = option{x.ID, x.Label}
	}
	return out
}

func (s *Server) renderHardware(w http.ResponseWriter, r *http.Request, code int, errMsg string, form *partForm) {
	parts, err := s.st.ListParts()
	if err != nil {
		s.internalError(w, r, "list hardware", err)
		return
	}
	if parts == nil {
		parts = []store.HWPart{}
	}
	for i := range parts {
		if parts[i].Aliases == nil {
			parts[i].Aliases = []string{}
		}
	}
	p := hwPayload{
		Categories: hardware.Categories, Statuses: options(hardware.Statuses), Sources: options(hardware.Sources),
		DriveTypes: hardware.DriveTypes, Parts: parts, CanEdit: canEditHardware(adminFrom(r)),
		CanDelete: canDeleteHardware(adminFrom(r)), Form: form,
	}
	b, err := json.Marshal(p)
	if err != nil {
		s.internalError(w, r, "encode hardware", err)
		return
	}
	s.render(w, r, code, "hardware.html", "HW Validation", hwPageData{JSON: string(b), Error: errMsg, CanEdit: p.CanEdit})
}

func (s *Server) handleHowtoSWHW(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "howto-swhw.html", "HowTo: Upload SWHW data", nil)
}

func (s *Server) handleHardware(w http.ResponseWriter, r *http.Request) {
	s.renderHardware(w, r, http.StatusOK, "", nil)
}

var driverRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{0,40}$`)

func formValue(r *http.Request, k string) string { return strings.TrimSpace(r.PostFormValue(k)) }

// parsePart validates the form; on failure it returns a message for the user.
func parsePart(r *http.Request) (*hardware.Part, *partForm, string) {
	f := &partForm{
		Category: formValue(r, "category"), Name: formValue(r, "name"), Status: formValue(r, "status"),
		Comment: strings.TrimSpace(r.PostFormValue("comment")), Speed: formValue(r, "speed"),
		Ports: formValue(r, "ports"), Driver: formValue(r, "driver"), DriveType: formValue(r, "drive_type"),
	}
	switch {
	case !hardware.ValidCategory(f.Category):
		return nil, f, "Choose the type of hardware."
	case f.Name == "" || len([]rune(f.Name)) > 200:
		return nil, f, "Enter the hardware name (up to 200 characters)."
	case !hardware.ValidStatus(f.Status):
		return nil, f, "Choose Supported, Unsupported or Not validated."
	case len([]rune(f.Comment)) > 2000:
		return nil, f, "The comment can be at most 2000 characters."
	}
	p := &hardware.Part{Category: f.Category, Name: f.Name, Status: f.Status, Comment: f.Comment}
	switch f.Category {
	case hardware.CatNIC:
		ports, err := hardware.ParsePorts(f.Ports)
		if err != nil {
			return nil, f, "Ports seen: " + err.Error() + "."
		}
		if len([]rune(f.Speed)) > 20 {
			return nil, f, "Speed can be at most 20 characters (for example 10GbE)."
		}
		if !driverRe.MatchString(f.Driver) {
			return nil, f, "Linux driver is a kernel module name, such as ixgbe or i40e."
		}
		p.Attrs = hardware.Attrs{Speed: f.Speed, Ports: ports, Driver: f.Driver}
	case hardware.CatDrive:
		ok := f.DriveType == ""
		for _, t := range hardware.DriveTypes {
			ok = ok || t == f.DriveType
		}
		if !ok {
			return nil, f, "Choose the drive type: HDD, SSD or NVMe SSD."
		}
		p.Attrs = hardware.Attrs{Type: f.DriveType}
	}
	return p, f, ""
}

func (s *Server) handlePartCreate(w http.ResponseWriter, r *http.Request) {
	me := adminFrom(r)
	if !canEditHardware(me) {
		s.errorPage(w, r, http.StatusForbidden, "Your account can't change the hardware list.")
		return
	}
	p, f, msg := parsePart(r)
	if msg != "" {
		s.renderHardware(w, r, http.StatusBadRequest, msg, f)
		return
	}
	p.Source, p.CreatedBy, p.UpdatedBy = hardware.SourceManual, me.Username, me.Username
	if err := s.st.CreatePart(p); err != nil {
		var dup *store.ErrDuplicatePart
		if errors.As(err, &dup) {
			s.renderHardware(w, r, http.StatusConflict, "This hardware is "+dup.Error()+".", f)
			return
		}
		s.internalError(w, r, "create part", err)
		return
	}
	s.log.Printf("hardware: %q added (%s) by %q", p.Name, p.Category, me.Username)
	http.Redirect(w, r, "/?m=hwadded", http.StatusSeeOther)
}

func (s *Server) handlePartUpdate(w http.ResponseWriter, r *http.Request) {
	me := adminFrom(r)
	if !canEditHardware(me) {
		s.errorPage(w, r, http.StatusForbidden, "Your account can't change the hardware list.")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, f, msg := parsePart(r)
	f.ID = id
	if msg != "" {
		s.renderHardware(w, r, http.StatusBadRequest, msg, f)
		return
	}
	p.ID, p.UpdatedBy = id, me.Username
	err := s.st.UpdatePart(p)
	var dup *store.ErrDuplicatePart
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.errorPage(w, r, http.StatusNotFound, "That hardware entry no longer exists.")
		return
	case errors.As(err, &dup):
		s.renderHardware(w, r, http.StatusConflict, "Another entry already covers this name: "+dup.Name+".", f)
		return
	case err != nil:
		s.internalError(w, r, "update part", err)
		return
	}
	s.log.Printf("hardware: entry %d (%q) edited by %q", id, p.Name, me.Username)
	http.Redirect(w, r, "/?m=hwsaved", http.StatusSeeOther)
}

func (s *Server) handlePartDelete(w http.ResponseWriter, r *http.Request) {
	me := adminFrom(r)
	if !canDeleteHardware(me) {
		s.errorPage(w, r, http.StatusForbidden, "Only Admin accounts can delete hardware entries.")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.st.DeletePart(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.errorPage(w, r, http.StatusNotFound, "That hardware entry no longer exists.")
			return
		}
		s.internalError(w, r, "delete part", err)
		return
	}
	s.log.Printf("hardware: entry %d deleted by %q", id, me.Username)
	http.Redirect(w, r, "/?m=hwdeleted", http.StatusSeeOther)
}
