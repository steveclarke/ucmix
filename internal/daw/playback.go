package daw

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Controller is the transport surface needed by the guarded playback probe.
type Controller interface {
	Trigger(context.Context, Action) error
}

const emergencyStopTimeout = 2 * time.Second

// PulsePlayback confirms Stop, attempts Play, and guarantees a bounded
// best-effort Stop. Duration is capped at one second from the Play attempt,
// including the time spent waiting for Play confirmation.
func PulsePlayback(
	ctx context.Context,
	controller Controller,
	duration time.Duration,
) (err error) {
	if duration < 0 || duration > time.Second {
		return fmt.Errorf("daw: playback duration must be between 0 and 1s")
	}
	if err := controller.Trigger(ctx, Stop); err != nil {
		return fmt.Errorf("daw: initial stop: %w", err)
	}

	stopped := false
	defer func() {
		if stopped {
			return
		}
		stopCtx, cancel := context.WithTimeout(
			context.Background(), emergencyStopTimeout,
		)
		defer cancel()
		stopErr := controller.Trigger(stopCtx, Stop)
		if stopErr != nil {
			err = errors.Join(
				err, fmt.Errorf("daw: emergency stop: %w", stopErr),
			)
		}
	}()

	stopDeadline := time.Now().Add(time.Second)
	playCtx, cancelPlay := context.WithDeadline(ctx, stopDeadline)
	if err := controller.Trigger(playCtx, Play); err != nil {
		cancelPlay()
		return fmt.Errorf("daw: play: %w", err)
	}
	cancelPlay()

	wait := duration
	if remaining := time.Until(stopDeadline); wait > remaining {
		wait = remaining
	}
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := controller.Trigger(ctx, Stop); err != nil {
		return fmt.Errorf("daw: final stop: %w", err)
	}
	stopped = true
	return nil
}
