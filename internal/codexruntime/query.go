package codexruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Call executes one read-only app-server RPC. It is used for model/account
// discovery and intentionally does not expose the raw transport to HTTP.
func Call(ctx context.Context, config Config, method string, params any) (json.RawMessage, error) {
	if strings.TrimSpace(config.Path) == "" {
		return nil, errors.New("Codex Runtime 未安装")
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	command := exec.Command(config.Path, "app-server")
	command.Dir = config.Workspace
	if len(config.Env) > 0 {
		command.Env = config.Env
	}
	configureProcessTree(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	var stderrTail synchronizedBuffer
	stderrTail.limit = 16 * 1024
	go func() { _, _ = io.Copy(&stderrTail, stderr) }()
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("启动 Codex app-server: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		terminateProcessTree(command)
		_ = command.Wait()
	}()

	type readResult struct {
		message rpcMessage
		err     error
	}
	readResults := make(chan readResult, 1)
	emitReadResult := func(result readResult) bool {
		select {
		case readResults <- result:
			return true
		case <-ctx.Done():
			return false
		}
	}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			var message rpcMessage
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				emitReadResult(readResult{err: fmt.Errorf("Codex app-server 返回无效 JSON: %w", err)})
				return
			}
			if !emitReadResult(readResult{message: message}) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			emitReadResult(readResult{err: err})
			return
		}
		if detail := strings.TrimSpace(stderrTail.Snapshot()); detail != "" {
			emitReadResult(readResult{err: fmt.Errorf("Codex app-server 已退出: %s", detail)})
			return
		}
		emitReadResult(readResult{err: io.EOF})
	}()
	var writeMu sync.Mutex
	write := func(value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		payload = append(payload, '\n')
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err = stdin.Write(payload)
		return err
	}
	read := func() (rpcMessage, error) {
		select {
		case <-ctx.Done():
			return rpcMessage{}, ctx.Err()
		case value := <-readResults:
			return value.message, value.err
		}
	}
	respond := func(message rpcMessage) error {
		return write(struct {
			ID    json.RawMessage `json:"id"`
			Error map[string]any  `json:"error"`
		}{ID: message.ID, Error: map[string]any{"code": -32000, "message": "EasyAgent 只允许通过 UI 处理该 app-server 请求"}})
	}
	request := func(id int, name string, value any) (json.RawMessage, error) {
		if err := write(struct {
			Method string `json:"method"`
			ID     int    `json:"id"`
			Params any    `json:"params,omitempty"`
		}{Method: name, ID: id, Params: value}); err != nil {
			return nil, err
		}
		for {
			message, err := read()
			if err != nil {
				return nil, err
			}
			if len(message.ID) > 0 && message.Method != "" {
				if err := respond(message); err != nil {
					return nil, err
				}
				continue
			}
			if len(message.ID) == 0 || string(message.ID) != fmt.Sprintf("%d", id) {
				continue
			}
			if len(message.Error) > 0 && string(message.Error) != "null" {
				return nil, rpcError(message.Error)
			}
			return message.Result, nil
		}
	}
	if _, err := request(1, "initialize", map[string]any{"clientInfo": map[string]string{"name": "easyagent", "title": "EasyAgent", "version": "0.1.0"}}); err != nil {
		return nil, err
	}
	if err := write(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return nil, err
	}
	return request(2, method, params)
}
