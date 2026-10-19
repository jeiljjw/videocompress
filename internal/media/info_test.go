package media

import "testing"

const probeFull = `{
  "streams": [
    {"codec_type":"video","codec_name":"h264","width":1920,"height":1080,
     "duration":"120.5","avg_frame_rate":"30000/1001"},
    {"codec_type":"audio","codec_name":"aac","bit_rate":"127999","channels":2,"sample_rate":"48000"}
  ],
  "format": {"duration":"120.53","size":"52428800"}
}`

const probeRotated = `{
  "streams": [
    {"codec_type":"video","codec_name":"h264","width":1920,"height":1080,
     "side_data_list":[{"side_data_type":"Display Matrix","rotation":-90}]}
  ],
  "format": {"duration":"10.0"}
}`

const probeTagRotate = `{
  "streams": [
    {"codec_type":"video","codec_name":"h264","width":1080,"height":1920,
     "tags":{"rotate":"90"}}
  ],
  "format": {"duration":"10.0"}
}`

const probeAudioOnly = `{
  "streams": [{"codec_type":"audio","codec_name":"aac"}],
  "format": {"duration":"3.0"}
}`

const probeStreamDuration = `{
  "streams": [{"codec_type":"video","codec_name":"vp9","width":640,"height":360,"duration":"7.25"}],
  "format": {}
}`

func TestParseProbeFull(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeFull), `C:\videos\a.mp4`, 52428800)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("dims = %dx%d", info.Width, info.Height)
	}
	if info.Duration != 120.53 {
		t.Fatalf("duration = %v", info.Duration)
	}
	if info.Rotation != 0 {
		t.Fatalf("rotation = %d", info.Rotation)
	}
	// 127999 bit/s rounds to 128 kbps.
	if info.Audio == nil || info.Audio.Codec != "aac" || info.Audio.BitrateKbps != 128 || info.Audio.Channels != 2 {
		t.Fatalf("audio = %+v", info.Audio)
	}
	if info.SizeBytes != 52428800 {
		t.Fatalf("size = %d", info.SizeBytes)
	}
}

func TestParseProbeRotatesDimensions(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeRotated), "a.mp4", 1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Rotation != 270 {
		t.Fatalf("rotation = %d, want 270", info.Rotation)
	}
	if info.Width != 1080 || info.Height != 1920 {
		t.Fatalf("display dims = %dx%d, want 1080x1920", info.Width, info.Height)
	}
}

func TestParseProbeTagRotate(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeTagRotate), "a.mp4", 1)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Rotation != 90 || info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("got rotation=%d dims=%dx%d", info.Rotation, info.Width, info.Height)
	}
}

func TestParseProbeStreamDurationFallback(t *testing.T) {
	info, err := ParseProbeJSON([]byte(probeStreamDuration), "a.webm", 100)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if info.Duration != 7.25 {
		t.Fatalf("duration = %v", info.Duration)
	}
}

func TestParseProbeErrors(t *testing.T) {
	if _, err := ParseProbeJSON([]byte(probeAudioOnly), "a.mp3", 1); err != ErrNoVideo {
		t.Fatalf("audio only err = %v, want ErrNoVideo", err)
	}
	if _, err := ParseProbeJSON([]byte(`{"streams":[{"codec_type":"video","width":10,"height":10}]}`), "a.mp4", 1); err != ErrNoDuration {
		t.Fatalf("no duration err = %v, want ErrNoDuration", err)
	}
	if _, err := ParseProbeJSON([]byte(`not json`), "a.mp4", 1); err == nil {
		t.Fatal("want parse error")
	}
}

func TestHasVideoExt(t *testing.T) {
	yes := []string{"a.mp4", "A.MP4", "b.mov", "c.mkv", "d.webm", "e.avi", "f.m4v", "g.wmv"}
	no := []string{"a.txt", "b.jpg", "noext", "c"}
	for _, n := range yes {
		if !HasVideoExt(n) {
			t.Fatalf("%s should be a video ext", n)
		}
	}
	for _, n := range no {
		if HasVideoExt(n) {
			t.Fatalf("%s should not be a video ext", n)
		}
	}
}
