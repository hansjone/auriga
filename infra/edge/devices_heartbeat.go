package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Passive, bounded observer. Authentication and framing enforcement remain at
// the broker. Compressed/invalid frames never produce a heartbeat observation.
type deviceHeartbeatFrames struct {
	header    []byte
	need      int
	remaining uint64
	offset    uint64
	mask      [4]byte
	opcode    byte
	disabled  bool
	line      []byte
	skip      uint64
	overflow  bool
	beat      func()
}

func (p *deviceHeartbeatFrames) feed(data []byte) {
	for _, b := range data {
		if p.disabled {
			return
		}
		if p.remaining == 0 {
			p.header = append(p.header, b)
			if len(p.header) == 2 {
				if p.header[0]&0x70 != 0 || p.header[1]&0x80 == 0 {
					p.disabled = true
					return
				}
				p.need = 6
				if p.header[1]&127 == 126 {
					p.need = 8
				}
				if p.header[1]&127 == 127 {
					p.need = 14
				}
			}
			if p.need == 0 || len(p.header) != p.need {
				continue
			}
			size := uint64(p.header[1] & 127)
			if size == 126 {
				size = uint64(binary.BigEndian.Uint16(p.header[2:4]))
			}
			if size == 127 {
				size = binary.BigEndian.Uint64(p.header[2:10])
				if size>>63 != 0 {
					p.disabled = true
					return
				}
			}
			p.opcode = p.header[0] & 15
			if p.opcode >= 8 && (size > 125 || p.header[0]&128 == 0) {
				p.disabled = true
				return
			}
			copy(p.mask[:], p.header[p.need-4:])
			p.header = p.header[:0]
			p.need = 0
			p.remaining = size
			p.offset = 0
			if size == 0 {
				p.controlBeat()
			}
			continue
		}
		value := b ^ p.mask[p.offset%4]
		p.offset++
		p.remaining--
		if p.opcode <= 2 {
			p.natsByte(value)
		}
		if p.remaining == 0 {
			p.controlBeat()
		}
	}
}

func (p *deviceHeartbeatFrames) controlBeat() {
	if (p.opcode == 9 || p.opcode == 10) && p.beat != nil {
		p.beat()
	}
}

func (p *deviceHeartbeatFrames) natsByte(b byte) {
	if p.skip > 0 {
		p.skip--
		return
	}
	if b != '\n' {
		if len(p.line) < 32768 {
			p.line = append(p.line, b)
		} else {
			p.overflow = true
		}
		return
	}
	if !p.overflow {
		line := string(p.line)
		if line == "PING\r" || line == "PONG\r" {
			if p.beat != nil {
				p.beat()
			}
		}
		if strings.HasPrefix(line, "PUB ") || strings.HasPrefix(line, "HPUB ") {
			fields := strings.Fields(line)
			if n, err := strconv.ParseUint(fields[len(fields)-1], 10, 63); err == nil {
				p.skip = n + 2
			} else {
				p.disabled = true
			}
		}
	} else {
		p.disabled = true
	} // No guessing the payload boundary of an oversized command.
	p.line = p.line[:0]
	p.overflow = false
}

type deviceHeartbeatRecord struct {
	last   time.Time
	closed bool
}
type deviceHeartbeats struct {
	mu        sync.Mutex
	byAddress map[string]deviceHeartbeatRecord
	byBinding map[string]time.Time
}

func (h *deviceHeartbeats) remember(key string, beat time.Time) time.Time {
	if h == nil {
		return beat
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byBinding == nil {
		h.byBinding = make(map[string]time.Time)
	}
	previous, exists := h.byBinding[key]
	if previous.After(beat) {
		return previous
	}
	if !beat.IsZero() && (exists || len(h.byBinding) < 10000) {
		h.byBinding[key] = beat
	}
	return beat
}

func (h *deviceHeartbeats) update(address string, closed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.byAddress == nil {
		h.byAddress = make(map[string]deviceHeartbeatRecord)
	}
	_, exists := h.byAddress[address]
	if len(h.byAddress) >= 10000 && !exists { // Only old closed observations may be discarded.
		for key, value := range h.byAddress {
			if value.closed {
				delete(h.byAddress, key)
				break
			}
		}
		if len(h.byAddress) >= 10000 {
			return
		}
	}
	value := h.byAddress[address]
	if !closed {
		value.last = time.Now().UTC()
	}
	value.closed = closed
	h.byAddress[address] = value
}
func (h *deviceHeartbeats) last(ip string, port int) time.Time {
	if h == nil {
		return time.Time{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.byAddress[net.JoinHostPort(ip, strconv.Itoa(port))].last
}

type deviceHeartbeatBody struct {
	io.ReadWriteCloser
	frames deviceHeartbeatFrames
	closed func()
}

func (b *deviceHeartbeatBody) Write(data []byte) (int, error) {
	n, err := b.ReadWriteCloser.Write(data)
	b.frames.feed(data[:n])
	return n, err
}
func (b *deviceHeartbeatBody) Close() error { b.closed(); return b.ReadWriteCloser.Close() }

type deviceHeartbeatAddress struct{ value string }
type deviceHeartbeatKey struct{}

func (h *deviceHeartbeats) trace(req *http.Request) *http.Request {
	address := &deviceHeartbeatAddress{}
	ctx := httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { address.value = info.Conn.LocalAddr().String() }})
	return req.WithContext(context.WithValue(ctx, deviceHeartbeatKey{}, address))
}
