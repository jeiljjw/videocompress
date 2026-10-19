package engine

import (
	"context"
	"errors"

	"videocompress/internal/media"
)

// processSplit is implemented in Task 11.
func (e *Engine) processSplit(context.Context, Request, *media.Info, *media.SplitPlan, string, *progressReporter) (*Result, error) {
	return nil, errors.New("분할 모드는 아직 구현되지 않았습니다")
}
