// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// stdioConn is an io.ReadWriteCloser over an MCP server subprocess: writes go to
// its stdin, reads come from its stdout. MCP's stdio transport is newline-framed
// JSON-RPC, which the Client's json encoder/decoder already speaks.
type stdioConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

func (c *stdioConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *stdioConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

// Close ends the conversation and the process: closing stdin signals EOF to a
// well-behaved server, and Kill + Wait reclaims it regardless.
func (c *stdioConn) Close() error {
	_ = c.stdin.Close()
	_ = c.stdout.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	return nil
}

// dialStdio spawns the configured command and returns a transport over its
// stdio. The child inherits the current environment plus the server's Env
// overrides; its stderr is discarded (a broken server surfaces as an
// initialize/list error, not noise on ours).
func dialStdio(cfg ServerConfig) (io.ReadWriteCloser, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %q: %w", cfg.Command, err)
	}
	return &stdioConn{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

// Connect dials a configured server and wraps it as a Client, ready for
// ServerRegistry.Add. stdio spawns a subprocess; http speaks Streamable HTTP to
// the server's URL. The legacy two-endpoint "sse" transport is not implemented —
// its servers are configured as http in current clients.
func Connect(s Server) (*Client, error) {
	switch s.Config.Transport() {
	case "stdio":
		rw, err := dialStdio(s.Config)
		if err != nil {
			return nil, err
		}
		return NewClient(rw), nil
	case "http":
		if s.Config.URL == "" {
			return nil, fmt.Errorf("mcp: server %q: http transport needs a url", s.Name)
		}
		return NewHTTPClient(s.Config.URL, s.Config.Headers), nil
	default:
		return nil, fmt.Errorf("mcp: server %q: %s transport not supported (use http for a remote server)", s.Name, s.Config.Transport())
	}
}
