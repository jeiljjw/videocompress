package engine

// RetryProgress reports monotonic progress across retries: each failed attempt
// consumes half of the remaining span, so the bar slows down but never resets.
type RetryProgress struct {
	report func(stage string, pct float64)
	base   float64
	span   float64
	last   float64
}

func newRetryProgress(report func(stage string, pct float64)) *RetryProgress {
	return &RetryProgress{report: report, span: 90}
}

func (r *RetryProgress) within(stage string, frac float64) {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	r.emit(stage, r.base+frac*r.span)
}

// fail keeps the bar where it is and halves the remaining span.
func (r *RetryProgress) fail() {
	r.base += r.span / 2
	r.span /= 2
}

// succeed marks the encode phase complete so verification/saving fill the
// remaining 10%.
func (r *RetryProgress) succeed() {
	r.base = 90
	r.span = 0
	r.emit("verifying", 90)
}

func (r *RetryProgress) finish(stage string) {
	r.emit(stage, 100)
}

func (r *RetryProgress) emit(stage string, pct float64) {
	if r.report == nil {
		return
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	if pct < r.last {
		pct = r.last // a retry must never move the bar backwards
	}
	r.last = pct
	r.report(stage, pct)
}
