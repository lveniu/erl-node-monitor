//go:build !windows

package sshprobe

import (
	"errors"
	"io"
	"net"
	"os"
)

func openSSHAgent() (io.ReadWriteCloser, error) {
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return nil, errors.New("SSH_AUTH_SOCK is empty")
	}
	return net.Dial("unix", socket)
}
