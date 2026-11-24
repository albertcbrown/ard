package state

import (
	"reflect"
	"testing"
)

func TestPRWatchRoundTrip(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	if _, ok, err := s.GetPRWatch(ctx, 5); ok || err != nil {
		t.Fatalf("empty = %v, %v", ok, err)
	}
	w := PRWatch{PR: 5, Issue: 9, Head: "abc", ChecksState: "pending", HeadSeenAt: t0,
		PendingNotifiedHead: "abc", ClosedNotified: true}
	if err := s.PutPRWatch(ctx, w); err != nil {
		t.Fatal(err)
	}
	w.RedNotifiedHead, w.MergeRecorded = "abc", true
	if err := s.PutPRWatch(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPRWatch(ctx, 5)
	if err != nil || !ok || !reflect.DeepEqual(got, w) {
		t.Fatalf("got %+v %v %v, want %+v", got, ok, err, w)
	}
	if err := s.DeletePRWatch(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetPRWatch(ctx, 5); ok {
		t.Fatal("row survived delete")
	}
}
