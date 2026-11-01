package schemas

import "sync"

type A2ARawCaptureHandle struct {
	manager   *A2ARawCaptureManager
	captureID string
}
type A2ARawCaptureManager struct {
	captures sync.Map
}
type A2ARawCapture struct {
	mu                                    sync.Mutex
	request, response                     []byte
	requestDropped, responseDropped       bool
	requestIncomplete, responseIncomplete bool
}

func (m *A2ARawCaptureManager) Start(captureID string) A2ARawCaptureHandle {
	m.captures.Store(captureID, &A2ARawCapture{})
	return A2ARawCaptureHandle{manager: m, captureID: captureID}
}

func (m *A2ARawCaptureManager) Delete(captureID string) {
	m.captures.Delete(captureID)
}

func (c *A2ARawCapture) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.request, c.response = nil, nil
	c.requestDropped, c.responseDropped = false, false
	c.requestIncomplete, c.responseIncomplete = false, false
}
