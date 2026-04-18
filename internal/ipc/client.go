package ipc

import (
	"bufio"
	"encoding/json"
	"net"
	"time"
)

// Client is a one-shot or multi-call connection to a daemon socket.
// Each Call performs one request/response round trip.
type Client struct {
	addr string
}

// NewClient returns a Client that dials addr for each call. Per-call
// connections keep wait-mode timeouts simple.
func NewClient(addr string) *Client { return &Client{addr: addr} }

// Call dials the daemon, writes the request, reads one response, and
// closes. overall caps total dial+read time (e.g. wait's timeout plus
// grace); use zero for no cap.
func (c *Client) Call(req *Request, overall time.Duration) (*Response, error) {
	conn, err := net.Dial("unix", c.addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if overall > 0 {
		_ = conn.SetDeadline(time.Now().Add(overall))
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if _, err := conn.Write(b); err != nil {
		return nil, err
	}
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Ping calls an arbitrary inexpensive op to confirm the daemon is
// live. Used by auto-spawn readiness polling.
func (c *Client) Ping() error {
	_, err := c.Call(&Request{Op: OpList}, 2*time.Second)
	return err
}
