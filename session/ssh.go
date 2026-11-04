package session

import (
	"fmt"
	"os"
	"runtime"

	"github.com/hewitt-iso/hewitt/docker"
)

// sshAgentSocket is where the forwarded agent appears in the container. Keys
// stay in the host agent; the container can only ask it to sign.
const sshAgentSocket = "/hewitt/ssh-agent.sock"

// dockerDesktopSSH is Docker Desktop's forwarded agent inside its VM; host
// socket paths are not reachable from containers there.
const dockerDesktopSSH = "/run/host-services/ssh-auth.sock"

// sshAgentMount returns the host agent socket mount. The socket is resolved at
// container creation: restarting the host agent needs a new session.
func sshAgentMount() (docker.BindMount, error) {
	if runtime.GOOS == "darwin" {
		return docker.BindMount{Source: dockerDesktopSSH, Target: sshAgentSocket}, nil
	}
	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		return docker.BindMount{}, &SSHAgentError{Reason: "SSH_AUTH_SOCK is not set"}
	}
	info, err := os.Stat(socket)
	if err != nil {
		return docker.BindMount{}, &SSHAgentError{Reason: "agent socket", Err: err}
	}
	if info.Mode()&os.ModeSocket == 0 {
		return docker.BindMount{}, &SSHAgentError{Reason: fmt.Sprintf("SSH_AUTH_SOCK %q is not a socket", socket)}
	}
	return docker.BindMount{Source: socket, Target: sshAgentSocket}, nil
}
