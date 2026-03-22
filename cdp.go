package butcherie

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// cdpClient is a minimal Firefox DevTools Protocol client.
// It multiplexes command responses and broadcasts events to subscribers.
type cdpClient struct {
	conn    *websocket.Conn
	writeMu sync.Mutex

	nextID  atomic.Int64
	pending sync.Map // map[int64]chan cdpResponse

	subsMu sync.RWMutex
	subs   []chan cdpEvent // broadcast to all subscribers

	done chan struct{}
}

type cdpMessage struct {
	ID     int64           `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cdpResponse struct {
	Result json.RawMessage
	Error  *cdpError
}

// cdpEvent is a CDP event message received from Firefox.
type cdpEvent struct {
	Method string
	Params json.RawMessage
}

// pendingRequest tracks an in-flight network request observed via CDP.
type pendingRequest struct {
	url     string
	method  string
	payload string
}

func newCDPClient(geckoPort int, sessionID string) (*cdpClient, error) {
	url := fmt.Sprintf("ws://localhost:%d/session/%s/moz/cdp", geckoPort, sessionID)
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, fmt.Errorf("dial CDP %s: %w", url, err)
	}
	c := &cdpClient{
		conn: conn,
		done: make(chan struct{}),
	}
	go c.readLoop()
	if err := c.enableNetwork(); err != nil {
		_ = conn.Close()
		<-c.done
		return nil, fmt.Errorf("enable network domain: %w", err)
	}
	return c, nil
}

func (c *cdpClient) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var m cdpMessage
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.ID != 0 {
			// Command response — deliver to the waiting caller.
			if ch, ok := c.pending.LoadAndDelete(m.ID); ok {
				ch.(chan cdpResponse) <- cdpResponse{Result: m.Result, Error: m.Error}
			}
		} else if m.Method != "" {
			// Event — broadcast to all subscribers.
			ev := cdpEvent{Method: m.Method, Params: m.Params}
			c.subsMu.RLock()
			for _, ch := range c.subs {
				select {
				case ch <- ev:
				default:
					// Subscriber too slow; drop rather than block the read loop.
				}
			}
			c.subsMu.RUnlock()
		}
	}
}

// send issues a CDP command and blocks until the response arrives (10 s timeout).
func (c *cdpClient) send(method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	ch := make(chan cdpResponse, 1)
	c.pending.Store(id, ch)

	msg, err := json.Marshal(map[string]interface{}{
		"id":     id,
		"method": method,
		"params": params,
	})
	if err != nil {
		c.pending.Delete(id)
		return nil, err
	}

	c.writeMu.Lock()
	err = c.conn.WriteMessage(websocket.TextMessage, msg)
	c.writeMu.Unlock()
	if err != nil {
		c.pending.Delete(id)
		return nil, fmt.Errorf("write CDP message: %w", err)
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("CDP error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-time.After(10 * time.Second):
		c.pending.Delete(id)
		return nil, fmt.Errorf("CDP command %q timed out", method)
	}
}

func (c *cdpClient) enableNetwork() error {
	_, err := c.send("Network.enable", map[string]interface{}{})
	return err
}

// subscribe returns a channel that receives all CDP events while the caller
// holds the subscription. Call unsubscribe when done.
func (c *cdpClient) subscribe(bufSize int) (events <-chan cdpEvent, unsubscribe func()) {
	ch := make(chan cdpEvent, bufSize)
	c.subsMu.Lock()
	c.subs = append(c.subs, ch)
	c.subsMu.Unlock()

	return ch, func() {
		c.subsMu.Lock()
		for i, s := range c.subs {
			if s == ch {
				c.subs = append(c.subs[:i], c.subs[i+1:]...)
				break
			}
		}
		c.subsMu.Unlock()
	}
}

// waitForNetworkIdle waits until all non-ignored in-flight requests complete,
// then waits for a 500 ms quiet period with no new requests. Returns an error
// (suitable for a 504 response) if ctx expires before that happens.
func (c *cdpClient) waitForNetworkIdle(ignorePatterns []*regexp.Regexp) func(done <-chan struct{}) error {
	// Subscribe before the action so we don't miss early events.
	events, unsub := c.subscribe(256)

	return func(done <-chan struct{}) error {
		defer unsub()

		pending := make(map[string]pendingRequest)
		lastActivity := time.Now()
		quietPeriod := 500 * time.Millisecond

		for {
			now := time.Now()
			idle := len(pending) == 0 && now.Sub(lastActivity) >= quietPeriod

			if idle {
				return nil
			}

			var waitDur time.Duration
			if len(pending) == 0 {
				waitDur = quietPeriod - now.Sub(lastActivity)
			} else {
				waitDur = 50 * time.Millisecond
			}

			select {
			case <-done:
				if len(pending) == 0 {
					return nil
				}
				var details []string
				for _, req := range pending {
					entry := fmt.Sprintf("%s %s", req.method, req.url)
					if req.payload != "" {
						entry += " (payload: " + req.payload + ")"
					}
					details = append(details, entry)
				}
				return fmt.Errorf("network timeout: %d request(s) still in-flight:\n%s",
					len(pending), strings.Join(details, "\n"))

			case ev := <-events:
				switch ev.Method {
				case "Network.requestWillBeSent":
					var p struct {
						RequestID string `json:"requestId"`
						Request   struct {
							URL      string `json:"url"`
							Method   string `json:"method"`
							PostData string `json:"postData"`
						} `json:"request"`
					}
					if err := json.Unmarshal(ev.Params, &p); err == nil {
						if !shouldIgnore(p.Request.URL, ignorePatterns) {
							pending[p.RequestID] = pendingRequest{
								url:     p.Request.URL,
								method:  p.Request.Method,
								payload: p.Request.PostData,
							}
							lastActivity = time.Now()
						}
					}

				case "Network.loadingFinished", "Network.loadingFailed":
					var p struct {
						RequestID string `json:"requestId"`
					}
					if err := json.Unmarshal(ev.Params, &p); err == nil {
						if _, was := pending[p.RequestID]; was {
							delete(pending, p.RequestID)
							lastActivity = time.Now()
						}
					}
				}

			case <-time.After(waitDur):
				// Loop back to re-evaluate idle condition.
			}
		}
	}
}

// captureDocumentResponse subscribes to Network.responseReceived and returns
// the status code and headers of the next Document-type response. The returned
// function must be called before the navigation action; it blocks until a
// document response is received or done is closed.
func (c *cdpClient) captureDocumentResponse() func(done <-chan struct{}) (int, map[string][]string) {
	events, unsub := c.subscribe(64)

	return func(done <-chan struct{}) (int, map[string][]string) {
		defer unsub()
		for {
			select {
			case <-done:
				return 0, nil
			case ev := <-events:
				if ev.Method != "Network.responseReceived" {
					continue
				}
				var p struct {
					Type     string `json:"type"`
					Response struct {
						Status  int                 `json:"status"`
						Headers map[string][]string `json:"headers"`
					} `json:"response"`
				}
				if err := json.Unmarshal(ev.Params, &p); err != nil {
					continue
				}
				if p.Type == "Document" {
					return p.Response.Status, p.Response.Headers
				}
			}
		}
	}
}

func shouldIgnore(url string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(url) {
			return true
		}
	}
	return false
}

func (c *cdpClient) close() {
	_ = c.conn.Close()
	<-c.done
}
