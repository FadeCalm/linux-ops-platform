package exec

import "testing"

func TestCappedBufferTruncates(t *testing.T) {
	b := newCappedBuffer(5)
	// The writer must report every byte as consumed or io.Copy stops reading
	// the child's pipe.
	if n, err := b.Write([]byte("hello world")); err != nil || n != 11 {
		t.Fatalf("Write = (%d, %v), want (11, nil)", n, err)
	}
	if b.Len() != 5 {
		t.Fatalf("len = %d, want 5", b.Len())
	}
	if !b.Truncated() {
		t.Fatal("expected truncated flag")
	}
	if got := string(b.Slice(0, 100)); got != "hello" {
		t.Fatalf("slice = %q, want hello", got)
	}
	if got := b.Slice(5, 10); got != nil {
		t.Fatalf("slice past end = %q, want nil", got)
	}
}

func TestCappedBufferOffsets(t *testing.T) {
	b := newCappedBuffer(100)
	b.Write([]byte("abcdef"))
	if got := string(b.Slice(2, 3)); got != "cde" {
		t.Fatalf("slice(2,3) = %q, want cde", got)
	}
	if b.Truncated() {
		t.Fatal("should not be truncated")
	}
}
