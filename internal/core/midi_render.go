package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// MIDI remains an editable format. Audio is rendered offline with the existing bundled FluidSynth.
const ticksPerBeat = 480

type midiEvent struct {
	tick  int
	order int
	data  []byte
}

func encodeVarLen(value uint32) []byte {
	result := []byte{byte(value & 0x7f)}
	for value >>= 7; value > 0; value >>= 7 {
		result = append([]byte{byte(value&0x7f) | 0x80}, result...)
	}
	return result
}

func appendMidiTrack(file *bytes.Buffer, events []midiEvent) error {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].tick != events[j].tick {
			return events[i].tick < events[j].tick
		}
		return events[i].order < events[j].order
	})
	var track bytes.Buffer
	last := 0
	for _, event := range events {
		if event.tick < last {
			return errors.New("MIDI events out of order")
		}
		track.Write(encodeVarLen(uint32(event.tick - last)))
		track.Write(event.data)
		last = event.tick
	}
	track.WriteByte(0)
	track.Write([]byte{0xff, 0x2f, 0})
	file.WriteString("MTrk")
	if err := binary.Write(file, binary.BigEndian, uint32(track.Len())); err != nil {
		return err
	}
	_, err := file.Write(track.Bytes())
	return err
}

func makeMidi(c Composition) ([]byte, error) {
	if errs := validateComposition(c); len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	var out bytes.Buffer
	out.WriteString("MThd")
	binary.Write(&out, binary.BigEndian, uint32(6))
	binary.Write(&out, binary.BigEndian, uint16(1))
	binary.Write(&out, binary.BigEndian, uint16(len(c.Tracks)+1))
	binary.Write(&out, binary.BigEndian, uint16(ticksPerBeat))
	tempo := uint32(math.Round(60000000 / c.BPM))
	if err := appendMidiTrack(&out, []midiEvent{
		{0, 0, []byte{0xff, 0x51, 0x03, byte(tempo >> 16), byte(tempo >> 8), byte(tempo)}},
		{0, 1, []byte{0xff, 0x58, 0x04, 0x04, 0x02, 0x18, 0x08}},
	}); err != nil {
		return nil, err
	}

	for _, track := range c.Tracks {
		channel := byte(track.Channel)
		name := []byte(track.Name)
		if len(name) > 127 {
			name = name[:127]
		}
		events := []midiEvent{
			{0, 0, append([]byte{0xff, 0x03, byte(len(name))}, name...)},
			{0, 1, []byte{0xb0 | channel, 7, byte(track.Volume)}},
			{0, 2, []byte{0xb0 | channel, 10, byte(math.Round((track.Pan + 64) / 128 * 127))}},
		}
		if track.Type == "instrument" {
			events = append(events, midiEvent{0, 3, []byte{0xc0 | channel, byte(track.Program)}})
		}
		for _, note := range track.Notes {
			start := int(math.Round(note.Start * ticksPerBeat))
			end := int(math.Round((note.Start + note.Duration) * ticksPerBeat))
			if end <= start {
				end = start + 1
			}
			events = append(events,
				midiEvent{start, 5, []byte{0x90 | channel, byte(note.Pitch), byte(note.Velocity)}},
				midiEvent{end, 4, []byte{0x80 | channel, byte(note.Pitch), 0}},
			)
		}
		if err := appendMidiTrack(&out, events); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

type RenderToolStatus struct {
	FluidSynthAvailable bool   `json:"fluidsynthAvailable"`
	FluidSynthPath      string `json:"fluidsynthPath,omitempty"`
	SoundFontConfigured bool   `json:"soundFontConfigured"`
	SoundFontPath       string `json:"soundFontPath,omitempty"`
	CanRender           bool   `json:"canRender"`
	Message             string `json:"message,omitempty"`
}

type RenderOutput struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	MidiPath  string `json:"midiPath"`
	WavPath   string `json:"wavPath"`
	CreatedAt int64  `json:"createdAt"`
}

type RenderService struct{ projects *ProjectStore }

func NewRenderService(projects *ProjectStore) *RenderService {
	return &RenderService{projects: projects}
}

func (r *RenderService) legacySetting(key string) string {
	var value string
	// Older databases may lack the settings table: configured env vars still work.
	if err := r.projects.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func resourceCandidates(relative string) []string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "..", "Resources", relative))
	}
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(wd, "resources", relative))
	}
	return candidates
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func fluidEnv(path string) []string {
	env := os.Environ()
	lib := filepath.Join(filepath.Dir(path), "lib")
	if info, err := os.Stat(lib); err == nil && info.IsDir() {
		env = append(env, "DYLD_LIBRARY_PATH="+lib+":"+os.Getenv("DYLD_LIBRARY_PATH"))
	}
	return env
}

func (r *RenderService) paths() (string, string) {
	binName := "fluidsynth"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	relative := filepath.Join("bin", runtime.GOOS+"-"+runtime.GOARCH, binName)
	bins := resourceCandidates(relative)
	bins = append(bins, os.Getenv("OTO_FLUIDSYNTH_PATH"), r.legacySetting("fluidsynth.path"))
	fontPaths := resourceCandidates(filepath.Join("soundfonts", "VintageDreamsWaves-v2.sf2"))
	fontPaths = append(fontPaths, os.Getenv("OTO_SOUNDFONT_PATH"), r.legacySetting("soundfont.path"))
	return firstExisting(bins...), firstExisting(fontPaths...)
}

func (r *RenderService) CheckTools(ctx context.Context) (RenderToolStatus, error) {
	bin, font := r.paths()
	status := RenderToolStatus{
		FluidSynthPath: bin, SoundFontPath: font, SoundFontConfigured: font != "",
	}
	if bin != "" {
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(checkCtx, bin, "--version")
		cmd.Env = fluidEnv(bin)
		status.FluidSynthAvailable = cmd.Run() == nil
	}
	status.CanRender = status.FluidSynthAvailable && status.SoundFontConfigured
	if !status.FluidSynthAvailable {
		status.Message = "FluidSynth unavailable. Check the bundled binary or configured path."
	} else if !status.SoundFontConfigured {
		status.Message = "Bundled SoundFont unavailable."
	}
	return status, nil
}

func (r *RenderService) RenderProject(ctx context.Context, projectID string) (RenderOutput, error) {
	project, err := r.projects.Get(ctx, projectID)
	if err != nil {
		return RenderOutput{}, err
	}
	if project == nil {
		return RenderOutput{}, errors.New("project not found")
	}
	status, err := r.CheckTools(ctx)
	if err != nil {
		return RenderOutput{}, err
	}
	if !status.CanRender {
		return RenderOutput{}, errors.New(status.Message)
	}
	midi, err := makeMidi(project.Composition)
	if err != nil {
		return RenderOutput{}, err
	}
	dir, err := r.projects.projectDir(projectID)
	if err != nil {
		return RenderOutput{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return RenderOutput{}, err
	}
	midiPath := filepath.Join(dir, "song.mid")
	wavPath := filepath.Join(dir, "render.wav")
	if err := os.WriteFile(midiPath, midi, 0600); err != nil {
		return RenderOutput{}, err
	}

	renderCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(renderCtx, status.FluidSynthPath,
		"-ni", "-T", "wav", "-F", wavPath, status.SoundFontPath, midiPath)
	cmd.Env = fluidEnv(status.FluidSynthPath)
	data, err := cmd.CombinedOutput()
	if err != nil {
		return RenderOutput{}, fmt.Errorf("FluidSynth render failed: %w: %s", err, string(data[:min(len(data), 500)]))
	}
	info, err := os.Stat(wavPath)
	if err != nil {
		return RenderOutput{}, err
	}
	if info.Size() < 44 {
		return RenderOutput{}, errors.New("FluidSynth did not produce a valid WAV")
	}
	id, err := newID()
	if err != nil {
		return RenderOutput{}, err
	}
	now := time.Now().UnixMilli()
	_, err = r.projects.db.ExecContext(ctx, "INSERT INTO render_outputs(id,project_id,midi_path,wav_path,created_at) VALUES(?,?,?,?,?)",
		id, projectID, midiPath, wavPath, now)
	if err != nil {
		return RenderOutput{}, err
	}
	return RenderOutput{ID: id, ProjectID: projectID, MidiPath: midiPath, WavPath: wavPath,
		CreatedAt: now}, nil
}
