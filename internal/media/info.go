package media

import (
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrNoVideo    = errors.New("비디오 스트림이 없습니다")
	ErrNoDuration = errors.New("영상 길이를 확인할 수 없습니다")
)

// AudioInfo describes the first audio stream.
type AudioInfo struct {
	Codec       string
	BitrateKbps int
	Channels    int
	SampleRate  int
}

// Info is the subset of ffprobe data the planner needs. Width and Height are
// display dimensions: for videos with 90/270 degree rotation they are swapped
// relative to the stored stream size, matching what FFmpeg outputs after
// automatic rotation.
type Info struct {
	Path       string
	SizeBytes  int64
	Duration   float64
	StartTime  float64
	Width      int
	Height     int
	VideoCodec string
	Rotation   int
	Audio      *AudioInfo
}

func (i *Info) HasAudio() bool { return i != nil && i.Audio != nil }

type probeJSON struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

type probeStream struct {
	CodecType    string            `json:"codec_type"`
	CodecName    string            `json:"codec_name"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	Duration     string            `json:"duration"`
	BitRate      string            `json:"bit_rate"`
	Channels     int               `json:"channels"`
	SampleRate   string            `json:"sample_rate"`
	Tags         map[string]string `json:"tags"`
	SideDataList []probeSideData   `json:"side_data_list"`
}

type probeSideData struct {
	Rotation float64 `json:"rotation"`
}

type probeFormat struct {
	Duration  string `json:"duration"`
	StartTime string `json:"start_time"`
	Size      string `json:"size"`
}

// ParseProbeJSON converts ffprobe -print_format json output into Info.
func ParseProbeJSON(data []byte, path string, sizeBytes int64) (*Info, error) {
	var raw probeJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	info := &Info{Path: path, SizeBytes: sizeBytes}
	var video *probeStream
	for i := range raw.Streams {
		s := &raw.Streams[i]
		switch s.CodecType {
		case "video":
			if video == nil {
				video = s
			}
		case "audio":
			if info.Audio == nil {
				info.Audio = &AudioInfo{
					Codec:       s.CodecName,
					BitrateKbps: parseKbps(s.BitRate),
					Channels:    s.Channels,
					SampleRate:  parseDurationInt(s.SampleRate),
				}
			}
		}
	}
	if video == nil {
		return nil, ErrNoVideo
	}
	info.VideoCodec = video.CodecName
	info.Width, info.Height = video.Width, video.Height

	info.Duration = parseDuration(raw.Format.Duration)
	info.StartTime = parseDuration(raw.Format.StartTime)
	if info.Duration <= 0 {
		for _, s := range raw.Streams {
			if d := parseDuration(s.Duration); d > info.Duration {
				info.Duration = d
			}
		}
	}
	if info.Duration <= 0 {
		return nil, ErrNoDuration
	}

	rot := 0.0
	if len(video.SideDataList) > 0 {
		rot = video.SideDataList[0].Rotation
	} else if v, ok := video.Tags["rotate"]; ok {
		rot, _ = strconv.ParseFloat(v, 64)
	}
	info.Rotation = normalizeRotation(rot)
	if info.Rotation == 90 || info.Rotation == 270 {
		info.Width, info.Height = info.Height, info.Width
	}
	return info, nil
}

func normalizeRotation(r float64) int {
	n := int(math.Round(r)) % 360
	if n < 0 {
		n += 360
	}
	switch n {
	case 90, 180, 270:
		return n
	default:
		return 0
	}
}

func parseDuration(s string) float64 {
	if s == "" || s == "N/A" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

func parseKbps(s string) int {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int(math.Round(v / 1000))
}

func parseDurationInt(s string) int {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return v
}

var videoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".mkv": true,
	".webm": true, ".avi": true, ".wmv": true, ".flv": true,
	".ts": true, ".mts": true, ".mpg": true, ".mpeg": true, ".3gp": true,
}

// HasVideoExt reports whether the file name has a known video extension.
func HasVideoExt(name string) bool {
	return videoExts[strings.ToLower(filepath.Ext(name))]
}
