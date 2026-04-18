package store

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuffer_EmptyRead(t *testing.T) {
	b := NewBuffer(16)
	out, end, ok := b.Read(0)
	if !ok {
		t.Fatal("want ok")
	}
	if end != 0 || len(out) != 0 {
		t.Fatalf("end=%d out=%q", end, out)
	}
}

func TestBuffer_AppendAndReadFromStart(t *testing.T) {
	b := NewBuffer(16)
	b.Append([]byte("hello"))
	out, end, ok := b.Read(0)
	if !ok {
		t.Fatal("want ok")
	}
	if string(out) != "hello" {
		t.Fatalf("got %q", out)
	}
	if end != 5 {
		t.Fatalf("end = %d", end)
	}
}

func TestBuffer_ReadFromMiddle(t *testing.T) {
	b := NewBuffer(16)
	b.Append([]byte("hello"))
	start := b.End()
	b.Append([]byte("world"))
	out, end, ok := b.Read(start)
	if !ok {
		t.Fatal("want ok")
	}
	if string(out) != "world" {
		t.Fatalf("got %q", out)
	}
	if end != 10 {
		t.Fatalf("end = %d", end)
	}
}

func TestBuffer_ReadAtEnd(t *testing.T) {
	b := NewBuffer(16)
	b.Append([]byte("hello"))
	out, end, ok := b.Read(5)
	if !ok {
		t.Fatal("want ok")
	}
	if len(out) != 0 {
		t.Fatalf("got %q", out)
	}
	if end != 5 {
		t.Fatalf("end = %d", end)
	}
}

func TestBuffer_EvictionAfterOverflow(t *testing.T) {
	b := NewBuffer(8)
	b.Append([]byte("abcdefgh")) // fills the buffer exactly
	if b.Start() != 0 || b.End() != 8 {
		t.Fatalf("start=%d end=%d", b.Start(), b.End())
	}
	b.Append([]byte("ijkl")) // forces 4 bytes evicted
	if b.Start() != 4 {
		t.Fatalf("start = %d, want 4", b.Start())
	}
	if b.End() != 12 {
		t.Fatalf("end = %d, want 12", b.End())
	}
	// Reading from start (0) must fail: evicted.
	if _, _, ok := b.Read(0); ok {
		t.Fatal("expected evicted to fail")
	}
	// Read from new start should return the current window.
	out, _, ok := b.Read(b.Start())
	if !ok || string(out) != "efghijkl" {
		t.Fatalf("got %q ok=%v", out, ok)
	}
}

func TestBuffer_AppendLargerThanCapacity(t *testing.T) {
	b := NewBuffer(4)
	b.Append([]byte("abcdefghij")) // 10 bytes into a 4-byte buffer
	if b.Start() != 6 || b.End() != 10 {
		t.Fatalf("start=%d end=%d", b.Start(), b.End())
	}
	out, _, ok := b.Read(6)
	if !ok || string(out) != "ghij" {
		t.Fatalf("got %q ok=%v", out, ok)
	}
}

func TestBuffer_WrapAround(t *testing.T) {
	b := NewBuffer(6)
	b.Append([]byte("abcd"))
	b.Append([]byte("efgh")) // now start=2, end=8, wraps
	if b.Start() != 2 || b.End() != 8 {
		t.Fatalf("start=%d end=%d", b.Start(), b.End())
	}
	out, _, ok := b.Read(2)
	if !ok {
		t.Fatal("want ok")
	}
	if string(out) != "cdefgh" {
		t.Fatalf("got %q", out)
	}
}

func TestBuffer_ReadBeyondEndIsInvalid(t *testing.T) {
	b := NewBuffer(8)
	b.Append([]byte("abc"))
	if _, _, ok := b.Read(99); ok {
		t.Fatal("expected ok=false for future offset")
	}
}

func TestBuffer_ManyAppendsStableRead(t *testing.T) {
	b := NewBuffer(1024)
	var want bytes.Buffer
	for i := 0; i < 1000; i++ {
		chunk := []byte(strings.Repeat("x", 1))
		b.Append(chunk)
		want.Write(chunk)
	}
	// Keep only last 1024 bytes in "want"
	allBytes := want.Bytes()
	if len(allBytes) > 1024 {
		allBytes = allBytes[len(allBytes)-1024:]
	}
	out, _, ok := b.Read(b.Start())
	if !ok {
		t.Fatal("want ok")
	}
	if !bytes.Equal(out, allBytes) {
		t.Fatalf("mismatch: len got=%d want=%d", len(out), len(allBytes))
	}
}
