package ws

import "testing"

func TestPublishAssignsSeqAndReplay(t *testing.T) {
	h := NewHub(8, 10)
	c, ok := h.Register("task1")
	if !ok {
		t.Fatal("register failed")
	}
	defer h.Unregister(c)

	s1 := h.Publish(Frame{Type: "stdout", TaskID: "task1", TargetID: "t1", Data: "a"})
	s2 := h.Publish(Frame{Type: "stdout", TaskID: "task1", TargetID: "t1", Data: "b"})
	if s1 != 1 || s2 != 2 {
		t.Fatalf("seq = %d,%d want 1,2", s1, s2)
	}
	// Drain the two live frames delivered to the client.
	<-c.Send
	<-c.Send

	frames := h.Replay("task1", map[string]int64{"t1": s1})
	if len(frames) != 1 || frames[0].Seq != s2 {
		t.Fatalf("replay = %+v, want one frame seq %d", frames, s2)
	}
}

func TestRingBufferIsBounded(t *testing.T) {
	h := NewHub(3, 10)
	c, _ := h.Register("task1")
	defer h.Unregister(c)
	for i := 0; i < 20; i++ {
		h.Publish(Frame{Type: "stdout", TaskID: "task1", TargetID: "t1", Data: "x"})
	}
	all := h.Replay("task1", nil)
	if len(all) > 3 {
		t.Fatalf("replay returned %d frames, ring is bounded to 3", len(all))
	}
	if all[len(all)-1].Seq != 20 {
		t.Fatalf("last seq = %d, want 20", all[len(all)-1].Seq)
	}
}

func TestEnsureSeqNeverLowers(t *testing.T) {
	h := NewHub(8, 10)
	h.EnsureSeq("t1", 42)
	if got := h.Publish(Frame{Type: "stdout", TaskID: "task1", TargetID: "t1"}); got != 43 {
		t.Fatalf("seq after EnsureSeq(42) = %d, want 43", got)
	}
	h.EnsureSeq("t1", 5)
	if got := h.Publish(Frame{Type: "stdout", TaskID: "task1", TargetID: "t1"}); got != 44 {
		t.Fatalf("seq after EnsureSeq(5) = %d, want 44", got)
	}
}

func TestConnectionLimit(t *testing.T) {
	h := NewHub(4, 1)
	c, ok := h.Register("task1")
	if !ok {
		t.Fatal("first register should succeed")
	}
	if _, ok := h.Register("task1"); ok {
		t.Fatal("second register should fail at the connection limit")
	}
	h.Unregister(c)
	if _, ok := h.Register("task1"); !ok {
		t.Fatal("register should succeed after unregister")
	}
}
