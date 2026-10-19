package engine

import (
	"fmt"
	"math"

	"videocompress/internal/media"
)

// DurationTolerance allows container rounding: at least 2 seconds, growing
// slightly (0.1s) with the number of parts.
func DurationTolerance(parts int) float64 {
	tol := 2.0
	if byParts := float64(parts) * 0.1; byParts > tol {
		tol = byParts
	}
	return tol
}

// VerifySingle checks the size, presence of video, and duration of one output.
func VerifySingle(src, out *media.Info, targetBytes int64) error {
	if out == nil {
		return fmt.Errorf("출력 정보를 확인할 수 없습니다")
	}
	if out.SizeBytes > targetBytes {
		return fmt.Errorf("출력이 목표 크기를 초과했습니다: %d > %d", out.SizeBytes, targetBytes)
	}
	if out.Width <= 0 || out.Height <= 0 {
		return fmt.Errorf("출력에 비디오 스트림이 없습니다")
	}
	tol := DurationTolerance(1)
	if math.Abs(out.Duration-src.Duration) > tol {
		return fmt.Errorf("출력 길이가 원본과 다릅니다: %.1fs vs %.1fs", out.Duration, src.Duration)
	}
	return nil
}

// VerifyParts checks each part size, video presence, and the total duration.
func VerifyParts(src *media.Info, parts []*media.Info, partBytes int64) error {
	if len(parts) == 0 {
		return fmt.Errorf("분할 결과가 없습니다")
	}
	var sum float64
	for i, p := range parts {
		if p == nil {
			return fmt.Errorf("조각 %d 정보를 확인할 수 없습니다", i+1)
		}
		if p.SizeBytes > partBytes {
			return fmt.Errorf("조각 %d가 조각 크기를 초과했습니다: %d > %d", i+1, p.SizeBytes, partBytes)
		}
		if p.Width <= 0 || p.Height <= 0 {
			return fmt.Errorf("조각 %d에 비디오 스트림이 없습니다", i+1)
		}
		if p.Duration <= 0 {
			return fmt.Errorf("조각 %d의 길이를 확인할 수 없습니다", i+1)
		}
		sum += p.Duration
	}
	if len(parts[0].Path) > 0 && parts[0].StartTime > 0.5 {
		return fmt.Errorf("첫 조각이 0초에서 시작하지 않습니다: %.2fs", parts[0].StartTime)
	}
	tol := DurationTolerance(len(parts))
	if math.Abs(sum-src.Duration) > tol {
		return fmt.Errorf("조각 길이 합이 원본과 다릅니다: %.1fs vs %.1fs", sum, src.Duration)
	}
	return nil
}
