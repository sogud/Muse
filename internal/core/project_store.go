package core

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// The JSON field names and SQLite schema match existing oto projects.
type Note struct {
	Pitch    int     `json:"pitch"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Velocity int     `json:"velocity"`
}

type CompositionTrack struct {
	ID      string  `json:"id"`
	Type    string  `json:"type"`
	Name    string  `json:"name"`
	Role    string  `json:"role"`
	Channel int     `json:"channel"`
	Program int     `json:"program,omitempty"`
	Volume  int     `json:"volume"`
	Pan     float64 `json:"pan"`
	Notes   []Note  `json:"notes"`
}

type Composition struct {
	Version       string             `json:"version"`
	ID            string             `json:"id"`
	Title         string             `json:"title"`
	Description   string             `json:"description,omitempty"`
	BPM           float64            `json:"bpm"`
	TimeSignature [2]int             `json:"timeSignature"`
	Key           string             `json:"key"`
	Bars          int                `json:"bars"`
	Tracks        []CompositionTrack `json:"tracks"`
	CreatedAt     int64              `json:"createdAt"`
	UpdatedAt     int64              `json:"updatedAt"`
}

type Project struct {
	ID                  string      `json:"id"`
	Title               string      `json:"title"`
	Description         string      `json:"description,omitempty"`
	SourceTrackID       string      `json:"sourceTrackId,omitempty"`
	SourceInspirationID string      `json:"sourceInspirationId,omitempty"`
	UserIdea            string      `json:"userIdea,omitempty"`
	Composition         Composition `json:"composition"`
	CreatedAt           int64       `json:"createdAt"`
	UpdatedAt           int64       `json:"updatedAt"`
}

type CreateProjectInput struct {
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Composition Composition `json:"composition"`
}

type UpdateCompositionInput struct {
	ProjectID   string      `json:"projectId"`
	Composition Composition `json:"composition"`
}

// ProjectStore is the shared SQLite backend for the CLI and optional MyGo native UI.
type ProjectStore struct {
	db   *sql.DB
	root string
}

func userDataRoot() (string, error) {
	root := os.Getenv("OTO_USER_DATA_DIR")
	if root == "" {
		root = os.Getenv("OTODESK_USER_DATA_DIR")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".oto")
	}
	return filepath.Abs(root)
}

func OpenProjectStore() (*ProjectStore, error) {
	root, err := userDataRoot()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}

	dsn := url.URL{Scheme: "file", Path: filepath.Join(root, "oto.sqlite")}
	db, err := sql.Open("sqlite", dsn.String()+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// Existing data is never dropped, reset, or rewritten by this schema check.
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS projects (
 id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT,
 source_track_id TEXT, source_inspiration_id TEXT, user_idea TEXT,
 composition_json TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS render_outputs (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, midi_path TEXT NOT NULL,
 wav_path TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS settings (
 key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS tracks (
 id TEXT PRIMARY KEY, source TEXT NOT NULL, source_id TEXT NOT NULL,
 title TEXT NOT NULL, artist TEXT NOT NULL, album TEXT, cover_url TEXT,
 duration INTEGER NOT NULL DEFAULT 0, lyric TEXT, created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL, UNIQUE(source, source_id)
);
CREATE TABLE IF NOT EXISTS inspirations (
 id TEXT PRIMARY KEY, track_id TEXT NOT NULL, analysis_id TEXT,
 title TEXT NOT NULL, note TEXT, mood_tags TEXT NOT NULL, genre_tags TEXT NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open oto projects: %w", err)
	}
	store := &ProjectStore{db: db, root: root}
	if err := store.initCreativeSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open creative projects: %w", err)
	}
	return store, nil
}

func (s *ProjectStore) close() error { return s.db.Close() }

// CloseProjectStore ends the database session without exposing a Close method
// to MyGo's automatic service binding.
func CloseProjectStore(s *ProjectStore) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.close()
}

func validProjectID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (s *ProjectStore) projectDir(id string) (string, error) {
	if !validProjectID(id) {
		return "", errors.New("invalid project ID")
	}
	return filepath.Join(s.root, "projects", id), nil
}

func validNumber(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func validateComposition(c Composition) []string {
	var errorsList []string
	if c.Version != "1.0" || strings.TrimSpace(c.Title) == "" || c.ID == "" || c.Key == "" ||
		!validNumber(c.BPM) || c.BPM < 60 || c.BPM > 160 ||
		c.Bars < 4 || c.Bars > 32 || c.TimeSignature != [2]int{4, 4} {
		errorsList = append(errorsList, "Invalid composition metadata (version, title, BPM, key, bars or time signature).")
	}
	if len(c.Tracks) < 4 || len(c.Tracks) > 8 {
		errorsList = append(errorsList, "Composition requires 4 to 8 tracks.")
	}
	roles := map[string]bool{}
	ids := map[string]bool{}
	total := 0
	for _, t := range c.Tracks {
		roles[t.Role] = true
		if ids[t.ID] || t.ID == "" {
			errorsList = append(errorsList, "Duplicate or empty track ID.")
		}
		ids[t.ID] = true
		if t.Name == "" || t.Channel < 0 || t.Channel > 15 || t.Volume < 0 || t.Volume > 127 || !validNumber(t.Pan) || t.Pan < -64 || t.Pan > 64 {
			errorsList = append(errorsList, "Invalid track properties: "+t.Name)
		}
		if t.Type == "drums" {
			if t.Channel != 9 || t.Role != "drums" {
				errorsList = append(errorsList, "Drum track must use channel 9.")
			}
		} else if t.Type == "instrument" {
			if t.Channel == 9 || t.Program < 0 || t.Program > 127 {
				errorsList = append(errorsList, "Invalid MIDI instrument/channel.")
			}
			switch t.Role {
			case "chords", "bass", "melody", "pad", "lead":
			default:
				errorsList = append(errorsList, "Invalid instrument track role.")
			}
		} else {
			errorsList = append(errorsList, "Invalid track type.")
		}
		if len(t.Notes) > 512 {
			errorsList = append(errorsList, "Too many notes in track: "+t.Name)
		}
		total += len(t.Notes)
		for _, n := range t.Notes {
			if n.Pitch < 0 || n.Pitch > 127 || n.Velocity < 1 || n.Velocity > 127 ||
				!validNumber(n.Start) || !validNumber(n.Duration) ||
				n.Start < 0 || n.Duration <= 0 || n.Start+n.Duration > float64(c.Bars*4)+0.00001 {
				errorsList = append(errorsList, "Invalid note in track: "+t.Name)
				break
			}
		}
	}
	for _, role := range []string{"drums", "bass", "chords", "melody"} {
		if !roles[role] {
			errorsList = append(errorsList, "Missing required track: "+role)
		}
	}
	if total > 2000 {
		errorsList = append(errorsList, "Composition has too many notes.")
	}
	return errorsList
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (s *ProjectStore) scan(rows *sql.Rows) ([]Project, error) {
	results := []Project{}
	for rows.Next() {
		var p Project
		var description, sourceTrack, sourceInspiration, userIdea sql.NullString
		var raw string
		if err := rows.Scan(&p.ID, &p.Title, &description, &sourceTrack, &sourceInspiration, &userIdea, &raw, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &p.Composition); err != nil {
			return nil, fmt.Errorf("project %s invalid composition: %w", p.ID, err)
		}
		p.Description = description.String
		p.SourceTrackID = sourceTrack.String
		p.SourceInspirationID = sourceInspiration.String
		p.UserIdea = userIdea.String
		results = append(results, p)
	}
	return results, rows.Err()
}

const projectColumns = "id,title,description,source_track_id,source_inspiration_id,user_idea,composition_json,created_at,updated_at"

func (s *ProjectStore) List(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+projectColumns+" FROM projects ORDER BY updated_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scan(rows)
}

func (s *ProjectStore) Get(ctx context.Context, id string) (*Project, error) {
	if !validProjectID(id) {
		return nil, errors.New("invalid project ID")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+projectColumns+" FROM projects WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects, err := s.scan(rows)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, nil
	}
	return &projects[0], nil
}

// File copies keep the existing editable project layout for both runtimes.
func (s *ProjectStore) writeFiles(p Project) error {
	dir, err := s.projectDir(p.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	composition, err := json.MarshalIndent(p.Composition, "", "  ")
	if err != nil {
		return err
	}
	metadata, err := json.MarshalIndent(struct {
		ID                  string `json:"id"`
		Title               string `json:"title"`
		Description         string `json:"description,omitempty"`
		SourceTrackID       string `json:"sourceTrackId,omitempty"`
		SourceInspirationID string `json:"sourceInspirationId,omitempty"`
		UserIdea            string `json:"userIdea,omitempty"`
		CreatedAt           int64  `json:"createdAt"`
		UpdatedAt           int64  `json:"updatedAt"`
	}{p.ID, p.Title, p.Description, p.SourceTrackID, p.SourceInspirationID, p.UserIdea, p.CreatedAt, p.UpdatedAt}, "", "  ")
	if err != nil {
		return err
	}
	for _, file := range []struct {
		name string
		data []byte
	}{
		{"composition.json", composition}, {"metadata.json", metadata},
	} {
		path := filepath.Join(dir, file.name)
		tmp, err := os.CreateTemp(dir, ".oto-*")
		if err != nil {
			return err
		}
		name := tmp.Name()
		if _, err = tmp.Write(file.data); err == nil {
			err = tmp.Close()
		} else {
			tmp.Close()
		}
		if err == nil {
			err = os.Rename(name, path)
		}
		if err != nil {
			os.Remove(name)
			return err
		}
	}
	return nil
}

func (s *ProjectStore) Create(ctx context.Context, input CreateProjectInput) (Project, error) {
	if errs := validateComposition(input.Composition); len(errs) > 0 {
		return Project{}, errors.New(strings.Join(errs, "; "))
	}
	id, err := newID()
	if err != nil {
		return Project{}, err
	}
	now := time.Now().UnixMilli()
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = input.Composition.Title
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = input.Composition.Description
	}
	p := Project{ID: id, Title: title, Description: description, Composition: input.Composition, CreatedAt: now, UpdatedAt: now}
	// Never touch an existing project if the random ID happens to collide.
	path, err := s.projectDir(id)
	if err != nil {
		return Project{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return Project{}, errors.New("project ID collision")
	}
	if err := s.writeFiles(p); err != nil {
		return Project{}, err
	}
	raw, _ := json.MarshalIndent(p.Composition, "", "  ")
	_, err = s.db.ExecContext(ctx, `INSERT INTO projects
        (id,title,description,source_track_id,source_inspiration_id,user_idea,composition_json,created_at,updated_at)
        VALUES(?,?,?,?,?,?,?,?,?)`, id, p.Title, p.Description, nil, nil, nil, string(raw), now, now)
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

func (s *ProjectStore) UpdateComposition(ctx context.Context, input UpdateCompositionInput) (Project, error) {
	if errs := validateComposition(input.Composition); len(errs) > 0 {
		return Project{}, errors.New(strings.Join(errs, "; "))
	}
	old, err := s.Get(ctx, input.ProjectID)
	if err != nil {
		return Project{}, err
	}
	if old == nil {
		return Project{}, errors.New("project not found")
	}
	old.Composition = input.Composition
	old.Composition.UpdatedAt = time.Now().UnixMilli()
	old.UpdatedAt = old.Composition.UpdatedAt
	old.Title = input.Composition.Title
	old.Description = input.Composition.Description
	if err := s.writeFiles(*old); err != nil {
		return Project{}, err
	}
	raw, _ := json.MarshalIndent(old.Composition, "", "  ")
	_, err = s.db.ExecContext(ctx, `UPDATE projects SET title=?,description=?,composition_json=?,updated_at=? WHERE id=?`,
		old.Title, old.Description, string(raw), old.UpdatedAt, old.ID)
	if err != nil {
		return Project{}, err
	}
	return *old, nil
}

func (s *ProjectStore) Remove(ctx context.Context, id string) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return errors.New("project not found")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM render_outputs WHERE project_id=?", id); err != nil {
		return err
	}
	if _, err = tx.Exec("DELETE FROM projects WHERE id=?", id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// The existing Node implementation also removes the project's own files.
	dir, err := s.projectDir(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
