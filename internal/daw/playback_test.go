package daw

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeController struct {
	actions []Action
	failAt  int
}

func (f *fakeController) Trigger(_ context.Context, action Action) error {
	f.actions = append(f.actions, action)
	if f.failAt == len(f.actions) {
		return errors.New("injected failure")
	}
	return nil
}

func TestPulsePlaybackStopsBeforeAndAfterPlay(t *testing.T) {
	fake := &fakeController{}
	if err := PulsePlayback(context.Background(), fake, 0); err != nil {
		t.Fatal(err)
	}
	want := []Action{Stop, Play, Stop}
	if !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}

func TestPulsePlaybackStopsWhenPlayConfirmationFails(t *testing.T) {
	fake := &fakeController{failAt: 2}
	if err := PulsePlayback(context.Background(), fake, 0); err == nil {
		t.Fatal("PulsePlayback succeeded")
	}
	want := []Action{Stop, Play, Stop}
	if !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}

func TestPulsePlaybackDoesNotPlayWhenInitialStopFails(t *testing.T) {
	fake := &fakeController{failAt: 1}
	if err := PulsePlayback(context.Background(), fake, time.Second); err == nil {
		t.Fatal("PulsePlayback succeeded")
	}
	if want := []Action{Stop}; !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}

func TestPulsePlaybackRejectsDurationOverOneSecond(t *testing.T) {
	fake := &fakeController{}
	if err := PulsePlayback(
		context.Background(), fake, time.Second+time.Nanosecond,
	); err == nil {
		t.Fatal("PulsePlayback succeeded")
	}
	if len(fake.actions) != 0 {
		t.Fatalf("invalid duration sent actions %v", fake.actions)
	}
}

type cancelingController struct {
	fakeController
	cancel context.CancelFunc
}

func (f *cancelingController) Trigger(ctx context.Context, action Action) error {
	err := f.fakeController.Trigger(ctx, action)
	if action == Play {
		f.cancel()
	}
	return err
}

func TestPulsePlaybackStopsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &cancelingController{cancel: cancel}
	err := PulsePlayback(ctx, fake, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PulsePlayback = %v, want context.Canceled", err)
	}
	want := []Action{Stop, Play, Stop}
	if !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}

type deadlineController struct {
	actions     []Action
	playTimeout time.Duration
}

func (f *deadlineController) Trigger(ctx context.Context, action Action) error {
	f.actions = append(f.actions, action)
	if action != Play {
		return nil
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("play context has no deadline")
	}
	f.playTimeout = time.Until(deadline)
	return errors.New("play confirmation failed")
}

func TestPulsePlaybackBoundsPlayAttemptToOneSecond(t *testing.T) {
	fake := &deadlineController{}
	if err := PulsePlayback(context.Background(), fake, time.Second); err == nil {
		t.Fatal("PulsePlayback succeeded")
	}
	if fake.playTimeout <= 0 || fake.playTimeout > time.Second {
		t.Fatalf("play timeout = %s, want 0s < timeout <= 1s", fake.playTimeout)
	}
	want := []Action{Stop, Play, Stop}
	if !reflect.DeepEqual(fake.actions, want) {
		t.Fatalf("actions = %v, want %v", fake.actions, want)
	}
}
