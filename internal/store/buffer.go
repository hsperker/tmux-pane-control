// Package store implements per-pane output stream retention with
// opaque checkpoint tokens, per spec §11.8.
//
// A Buffer is a byte-capped ring of stream bytes. Each byte in the
// stream has a stable absolute offset that never decreases; the
// Buffer retains only the most recent Capacity bytes, and reads
// below the retained window fail with an evicted-past signal.
package store

// Buffer is a per-pane byte ring. It is not goroutine-safe; Store
// serializes access.
type Buffer struct {
	capacity int
	data     []byte // fixed length == capacity once grown
	// start is the absolute offset of the oldest retained byte.
	// end is the absolute offset of the next byte to be appended.
	// Invariant: 0 <= end-start <= capacity.
	start int64
	end   int64
}

// NewBuffer returns a Buffer that retains at most capacity bytes.
// Capacity must be > 0.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		panic("store: capacity must be positive")
	}
	return &Buffer{
		capacity: capacity,
		data:     make([]byte, capacity),
	}
}

// End returns the absolute offset of the next byte to be appended.
// It is the current head of the stream and a valid checkpoint.
func (b *Buffer) End() int64 { return b.end }

// Start returns the absolute offset of the oldest retained byte.
func (b *Buffer) Start() int64 { return b.start }

// Len returns the number of bytes currently retained.
func (b *Buffer) Len() int { return int(b.end - b.start) }

// Append writes p into the ring, evicting oldest bytes as needed.
// It advances end by len(p) and may advance start by up to len(p).
func (b *Buffer) Append(p []byte) {
	n := len(p)
	if n == 0 {
		return
	}
	// If p is larger than the capacity, the leading bytes are
	// immediately evicted. Advance end for those bytes and drop them
	// from p before writing; this preserves the ring invariant that
	// data[offset%cap] holds the byte at that absolute offset.
	if n > b.capacity {
		dropped := n - b.capacity
		b.end += int64(dropped)
		if b.end-b.start > int64(b.capacity) {
			b.start = b.end - int64(b.capacity)
		}
		p = p[dropped:]
		n = b.capacity
	}
	head := int(b.end % int64(b.capacity))
	first := b.capacity - head
	if first >= n {
		copy(b.data[head:head+n], p)
	} else {
		copy(b.data[head:], p[:first])
		copy(b.data[:n-first], p[first:])
	}
	b.end += int64(n)
	if b.end-b.start > int64(b.capacity) {
		b.start = b.end - int64(b.capacity)
	}
}

// Read returns the bytes in the stream range [after, end) and the new
// end. It returns ok=false if after refers to evicted bytes (below
// start) or lies beyond end.
func (b *Buffer) Read(after int64) (out []byte, end int64, ok bool) {
	if after < b.start || after > b.end {
		return nil, b.end, false
	}
	n := int(b.end - after)
	if n == 0 {
		return nil, b.end, true
	}
	out = make([]byte, n)
	begin := int(after % int64(b.capacity))
	if begin+n <= b.capacity {
		copy(out, b.data[begin:begin+n])
	} else {
		first := b.capacity - begin
		copy(out, b.data[begin:])
		copy(out[first:], b.data[:n-first])
	}
	return out, b.end, true
}
