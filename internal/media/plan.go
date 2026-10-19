package media

import (
	"errors"
	"math"
)

const (
	SafetyFactor = 0.95
	MinVideoKbps = 100
	MaxVideoKbps = 12000

	ModeSingle  = "single"
	ModeSplit   = "split"
	ModeSkipped = "skipped"
)

var (
	ErrInvalidInfo     = errors.New("영상 정보가 올바르지 않습니다")
	ErrInvalidSettings = errors.New("목표 크기와 조각 크기 설정이 올바르지 않습니다")
)

// Candidate is one step of the single-file resolution ladder.
type Candidate struct {
	Label     string
	Width     int
	Height    int
	VideoKbps int
}

// SplitPlan describes a split-mode encode.
type SplitPlan struct {
	Width          int
	Height         int
	VideoKbps      int
	AudioKbps      int
	SegmentSeconds int
	Parts          int
	LowQuality     bool
}

// Plan is the outcome of planning for one input file.
type Plan struct {
	Mode      string
	Info      *Info
	AudioKbps int
	AudioCopy bool
	Candidate Candidate
	Stages    []Candidate
	Split     *SplitPlan
}

type box struct {
	label  string
	width  int
	height int
}

var ladder = []box{
	{"1080p", 1920, 1080},
	{"720p", 1280, 720},
	{"480p", 854, 480},
}

var splitSegments = []int{120, 60, 30, 15}

// FitBox scales w×h to fit inside the box without upscaling, preserving the
// aspect ratio, and returns even dimensions (required by yuv420p).
func FitBox(w, h, boxW, boxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if w <= boxW && h <= boxH {
		return even(w), even(h)
	}
	scale := math.Min(float64(boxW)/float64(w), float64(boxH)/float64(h))
	return even(int(math.Round(float64(w) * scale))), even(int(math.Round(float64(h) * scale)))
}

func even(v int) int {
	if v%2 != 0 {
		v--
	}
	if v < 2 {
		v = 2
	}
	return v
}

// FloorKbps returns the minimum acceptable video bitrate for a resolution,
// based on the shorter side.
func FloorKbps(w, h int) int {
	short := w
	if h < short {
		short = h
	}
	switch {
	case short >= 1000:
		return 1500
	case short >= 700:
		return 800
	default:
		return 400
	}
}

// VideoKbpsFor computes the video bitrate budget for a size and duration.
// The result is clamped to [MinVideoKbps, MaxVideoKbps].
func VideoKbpsFor(bytes int64, seconds float64, audioKbps int) int {
	if seconds <= 0 {
		return MinVideoKbps
	}
	total := float64(bytes) * 8 / seconds / 1000
	v := int(math.Floor(total*SafetyFactor)) - audioKbps
	if v < MinVideoKbps {
		return MinVideoKbps
	}
	if v > MaxVideoKbps {
		return MaxVideoKbps
	}
	return v
}

// AudioPlan returns the audio bitrate to reserve and whether the source audio
// can be stream-copied. Unknown AAC bitrates are re-encoded to stay safe.
func AudioPlan(info *Info, forceEncode bool) (int, bool) {
	if info == nil || info.Audio == nil {
		return 0, false
	}
	a := info.Audio
	if !forceEncode && a.Codec == "aac" && a.BitrateKbps > 0 && a.BitrateKbps <= 128 {
		return a.BitrateKbps, true
	}
	if a.Channels == 1 {
		return 64, false
	}
	return 96, false
}

// SingleCandidates returns ladder steps, highest quality first, whose floor is
// satisfied by the target-driven bitrate. Identical dimensions are deduped.
func SingleCandidates(info *Info, targetBytes int64) []Candidate {
	if info == nil || info.Width <= 0 || info.Height <= 0 || info.Duration <= 0 {
		return nil
	}
	audioKbps, _ := AudioPlan(info, false)
	kbps := VideoKbpsFor(targetBytes, info.Duration, audioKbps)
	var out []Candidate
	seen := map[[2]int]bool{}
	for _, b := range ladder {
		w, h := FitBox(info.Width, info.Height, b.width, b.height)
		if w <= 0 || h <= 0 {
			continue
		}
		key := [2]int{w, h}
		if seen[key] {
			continue
		}
		seen[key] = true
		if kbps < FloorKbps(w, h) {
			continue
		}
		out = append(out, Candidate{Label: b.label, Width: w, Height: h, VideoKbps: kbps})
	}
	return out
}

// SplitPlanFor selects the longest segment length whose bitrate meets the
// quality floor for the split resolution (min of source and 720p).
func SplitPlanFor(info *Info, partBytes int64) (*SplitPlan, error) {
	if info == nil || info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		return nil, ErrInvalidInfo
	}
	w, h := FitBox(info.Width, info.Height, 1280, 720)
	audioKbps, _ := AudioPlan(info, true)
	floor := FloorKbps(w, h)
	for _, seg := range splitSegments {
		kbps := VideoKbpsFor(partBytes, float64(seg), audioKbps)
		if kbps >= floor {
			return &SplitPlan{
				Width: w, Height: h, VideoKbps: kbps, AudioKbps: audioKbps,
				SegmentSeconds: seg, Parts: partsFor(info.Duration, seg),
			}, nil
		}
	}
	seg := splitSegments[len(splitSegments)-1]
	return &SplitPlan{
		Width: w, Height: h, VideoKbps: VideoKbpsFor(partBytes, float64(seg), audioKbps),
		AudioKbps: audioKbps, SegmentSeconds: seg, Parts: partsFor(info.Duration, seg),
		LowQuality: true,
	}, nil
}

func partsFor(duration float64, seg int) int {
	n := int(math.Ceil(duration / float64(seg)))
	if n < 1 {
		n = 1
	}
	return n
}

// PlanFor decides between skipping, single-file compression, and splitting.
func PlanFor(info *Info, targetBytes, partBytes int64) (*Plan, error) {
	if info == nil || info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		return nil, ErrInvalidInfo
	}
	if targetBytes <= 0 || partBytes <= 0 || partBytes >= targetBytes {
		return nil, ErrInvalidSettings
	}
	p := &Plan{Info: info}
	if info.SizeBytes > 0 && info.SizeBytes <= targetBytes {
		p.Mode = ModeSkipped
		return p, nil
	}
	p.AudioKbps, p.AudioCopy = AudioPlan(info, false)
	if stages := SingleCandidates(info, targetBytes); len(stages) > 0 {
		p.Mode = ModeSingle
		p.Stages = stages
		p.Candidate = stages[0]
		return p, nil
	}
	sp, err := SplitPlanFor(info, partBytes)
	if err != nil {
		return nil, err
	}
	p.Mode = ModeSplit
	p.Split = sp
	return p, nil
}
