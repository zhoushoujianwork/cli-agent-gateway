package acp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

type rpcResponse struct {
	ID     int
	Result any
	Error  any
}

type rpcRequest struct {
	ID     int
	Method string
	Params map[string]any
}

type rpcNotification struct {
	Method string
	Params map[string]any
}

type JsonRPCClient struct {
	command string
	cwd     string

	mu       sync.Mutex
	nextID   int
	proc     *exec.Cmd
	stdin    ioWriteCloser
	closed   bool
	pending  map[int]chan rpcResponse
	notifyCh chan rpcNotification
	reqCh    chan rpcRequest
	stderrCh chan string
}

type ioWriteCloser interface {
	Write([]byte) (int, error)
	Close() error
}

func NewJSONRPCClient(command, cwd string) *JsonRPCClient {
	return &JsonRPCClient{
		command:  command,
		cwd:      cwd,
		nextID:   1,
		pending:  map[int]chan rpcResponse{},
		notifyCh: make(chan rpcNotification, 256),
		reqCh:    make(chan rpcRequest, 256),
		stderrCh: make(chan string, 256),
	}
}

func (c *JsonRPCClient) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc != nil {
		return nil
	}
	cmd := exec.Command("/bin/sh", "-lc", c.command)
	cmd.Dir = c.cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c.proc = cmd
	c.stdin = stdin
	c.closed = false

	go c.readStdout(stdout)
	go c.readStderr(stderr)
	return nil
}

func (c *JsonRPCClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.stdin != nil {
		_ = c.stdin.Close()
		c.stdin = nil
	}
	if c.proc != nil && c.proc.Process != nil {
		_ = c.proc.Process.Kill()
		_, _ = c.proc.Process.Wait()
	}
	c.proc = nil
	return nil
}

func (c *JsonRPCClient) send(payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return errors.New("jsonrpc client stdin is nil")
	}
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *JsonRPCClient) StartRequest(method string, params map[string]any) (int, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	respCh := make(chan rpcResponse, 1)
	c.pending[id] = respCh
	c.mu.Unlock()

	if params == nil {
		params = map[string]any{}
	}
	err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return 0, err
	}
	return id, nil
}

func (c *JsonRPCClient) PollResponse(id int, timeout time.Duration) (*rpcResponse, error) {
	c.mu.Lock()
	respCh := c.pending[id]
	c.mu.Unlock()
	if respCh == nil {
		return nil, nil
	}
	select {
	case r := <-respCh:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return &r, nil
	case <-time.After(timeout):
		return nil, nil
	}
}

func (c *JsonRPCClient) SendRequest(method string, params map[string]any, timeout time.Duration) (any, error) {
	id, err := c.StartRequest(method, params)
	if err != nil {
		return nil, err
	}
	for {
		resp, err := c.PollResponse(id, 100*time.Millisecond)
		if err != nil {
			return nil, err
		}
		if resp != nil {
			if resp.Error != nil {
				return nil, fmt.Errorf("jsonrpc error method=%s error=%v", method, resp.Error)
			}
			return resp.Result, nil
		}
		timeout -= 100 * time.Millisecond
		if timeout <= 0 {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, fmt.Errorf("request timeout method=%s", method)
		}
	}
}

func (c *JsonRPCClient) SendResponse(id int, result any, errObj any) error {
	payload := map[string]any{"jsonrpc": "2.0", "id": id}
	if errObj != nil {
		payload["error"] = errObj
	} else {
		if result == nil {
			result = map[string]any{}
		}
		payload["result"] = result
	}
	return c.send(payload)
}

func (c *JsonRPCClient) PopNotification(timeout time.Duration) *rpcNotification {
	select {
	case n := <-c.notifyCh:
		return &n
	case <-time.After(timeout):
		return nil
	}
}

func (c *JsonRPCClient) PopRequest(timeout time.Duration) *rpcRequest {
	select {
	case r := <-c.reqCh:
		return &r
	case <-time.After(timeout):
		return nil
	}
}

func (c *JsonRPCClient) readStdout(stdout scannerReader) {
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if _, ok := msg["method"]; !ok {
			idf, ok := msg["id"].(float64)
			if !ok {
				continue
			}
			id := int(idf)
			resp := rpcResponse{ID: id, Result: msg["result"], Error: msg["error"]}
			c.mu.Lock()
			ch := c.pending[id]
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- resp:
				default:
				}
			}
			continue
		}

		method, _ := msg["method"].(string)
		params := map[string]any{}
		if p, ok := msg["params"].(map[string]any); ok {
			params = p
		}
		if _, hasID := msg["id"]; hasID {
			idf, _ := msg["id"].(float64)
			select {
			case c.reqCh <- rpcRequest{ID: int(idf), Method: method, Params: params}:
			default:
			}
		} else {
			select {
			case c.notifyCh <- rpcNotification{Method: method, Params: params}:
			default:
			}
		}
	}
}

type scannerReader interface {
	Read(p []byte) (n int, err error)
}

func (c *JsonRPCClient) readStderr(stderr scannerReader) {
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		select {
		case c.stderrCh <- line:
		default:
		}
	}
}
