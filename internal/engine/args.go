package engine

import (
	"fmt"
)

// Audio modes for EncodeSpec.
const (
	AudioNone = "none"
	AudioCopy = "copy"
	AudioAAC  = "aac"
)

// EncodeSpec describes one ffmpeg invocation for single-file or segment output.
type EncodeSpec struct {
	Input          string
	Output         string
	PassLog        string
	Pass           int
	Width          int
	Height         int
	Scale          bool
	VideoKbps      int
	AudioMode      string
	AudioKbps      int
	AudioChannels  int
	FastStart      bool
	ForceKeySec    int
	SegmentSeconds int
	SegmentPattern string
}

// SingleArgs builds ffmpeg arguments. Pass 1 writes to the null muxer; pass 2
// writes a single MP4 or a segment pattern.
func SingleArgs(spec EncodeSpec) []string {
	args := []string{"-y", "-i", spec.Input, "-map", "0:v:0"}
	if spec.Scale && spec.Width > 0 && spec.Height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", spec.Width, spec.Height))
	}
	args = append(args,
		"-c:v", "libx264",
		"-preset", "medium",
		"-profile:v", "high",
		"-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", spec.VideoKbps),
		"-pass", fmt.Sprintf("%d", spec.Pass),
		"-passlogfile", spec.PassLog,
	)
	if spec.Pass == 1 {
		args = append(args, "-an", "-sn", "-dn", "-f", "null", "-")
		return args
	}
	args = appendAudioArgs(args, spec)
	if spec.ForceKeySec > 0 {
		args = append(args,
			"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", spec.ForceKeySec),
			"-sc_threshold", "0",
		)
	}
	if spec.SegmentPattern != "" {
		args = append(args,
			"-f", "segment",
			"-segment_time", fmt.Sprintf("%d", spec.SegmentSeconds),
			"-segment_format", "mp4",
			"-reset_timestamps", "1",
			"-segment_start_number", "1",
			spec.SegmentPattern,
		)
		return args
	}
	if spec.FastStart {
		args = append(args, "-movflags", "+faststart")
	}
	args = append(args, "-f", "mp4", spec.Output)
	return args
}

func appendAudioArgs(args []string, spec EncodeSpec) []string {
	switch spec.AudioMode {
	case AudioCopy:
		args = append(args, "-map", "0:a:0", "-c:a", "copy")
	case AudioAAC:
		ch := spec.AudioChannels
		if ch < 1 {
			ch = 2
		}
		if ch > 2 {
			ch = 2
		}
		args = append(args,
			"-map", "0:a:0",
			"-c:a", "aac",
			"-b:a", fmt.Sprintf("%dk", spec.AudioKbps),
			"-ac", fmt.Sprintf("%d", ch),
		)
	default:
		args = append(args, "-an")
	}
	return args
}

// RangeSpec describes re-encoding a time range of the source.
type RangeSpec struct {
	Input         string
	Output        string
	Start         float64
	Duration      float64
	Width         int
	Height        int
	Scale         bool
	VideoKbps     int
	AudioKbps     int
	AudioChannels int
	HasAudio      bool
}

// RangeArgs builds arguments that re-encode [Start, Start+Duration) from the
// source with a single pass and faststart.
func RangeArgs(spec RangeSpec) []string {
	args := []string{"-y"}
	if spec.Start > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", spec.Start))
	}
	args = append(args, "-t", fmt.Sprintf("%.3f", spec.Duration), "-i", spec.Input, "-map", "0:v:0")
	if spec.Scale {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", spec.Width, spec.Height))
	}
	args = append(args,
		"-c:v", "libx264",
		"-preset", "medium",
		"-profile:v", "high",
		"-pix_fmt", "yuv420p",
		"-b:v", fmt.Sprintf("%dk", spec.VideoKbps),
	)
	if spec.HasAudio {
		ch := spec.AudioChannels
		if ch < 1 {
			ch = 2
		}
		if ch > 2 {
			ch = 2
		}
		args = append(args, "-map", "0:a:0", "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", spec.AudioKbps), "-ac", fmt.Sprintf("%d", ch))
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-sn", "-dn", "-movflags", "+faststart", "-f", "mp4", spec.Output)
	return args
}

// RetryKbps scales the video bitrate by the size overshoot with 5% headroom.
func RetryKbps(current int, targetBytes, actualBytes int64) int {
	if actualBytes <= 0 {
		return current
	}
	next := int(float64(current) * float64(targetBytes) / float64(actualBytes) * 0.95)
	if next < 100 {
		next = 100
	}
	if next >= current {
		next = current - 1
	}
	if next < 100 {
		next = 100
	}
	return next
}
