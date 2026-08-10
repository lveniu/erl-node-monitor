//go:build windows

package sshprobe

import (
	"io"
	"os"
	"sync"
)

// Windows OpenSSH exposes its agent through a named pipe that may reject a
// second simultaneous client with ERROR_PIPE_BUSY. Exporter servers start in
// parallel, so serialize only the short agent-backed SSH handshake. The lock
// is released when dial closes the agent connection; server collection then
// continues concurrently over the established SSH clients.
var windowsSSHAgentMu sync.Mutex

type serializedAgentConnection struct {
	io.ReadWriteCloser
	once sync.Once
}

func openSSHAgent() (io.ReadWriteCloser, error) {
	windowsSSHAgentMu.Lock()
	connection, err := os.OpenFile(`\\.\pipe\openssh-ssh-agent`, os.O_RDWR, 0)
	if err != nil {
		windowsSSHAgentMu.Unlock()
		return nil, err
	}
	return &serializedAgentConnection{ReadWriteCloser: connection}, nil
}

func (connection *serializedAgentConnection) Close() error {
	var closeErr error
	connection.once.Do(func() {
		closeErr = connection.ReadWriteCloser.Close()
		windowsSSHAgentMu.Unlock()
	})
	return closeErr
}
