package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	labelManaged      = "openflux.managed"
	labelKeyID        = "openflux.keyID"
	containerKeyPath  = "/run/secrets/channel.key"
	containerBinary   = "/usr/local/bin/openflux"
	defaultSecretsDir = "accessd-secrets"
)

// DockerLauncher runs each exit as a container of the OpenFlux image via the
// docker CLI. The CLI (not the Docker SDK) keeps the build free of a large
// new module tree; any host that runs Docker has it.
//
// The key's secret reaches the container as a 0600 file in stateDir,
// bind-mounted read-only at /run/secrets/channel.key. Containers run with
// --rm and no restart policy: an exit that stops itself on revoke stays down.
type DockerLauncher struct {
	image    string
	stateDir string
	network  string
	run      func(args ...string) (string, error)
}

// DockerOption configures a DockerLauncher.
type DockerOption func(*DockerLauncher)

// WithStateDir sets where per-key secret files are written.
func WithStateDir(dir string) DockerOption { return func(l *DockerLauncher) { l.stateDir = dir } }

// WithNetwork attaches exit containers to a Docker network.
func WithNetwork(network string) DockerOption { return func(l *DockerLauncher) { l.network = network } }

// withRunner replaces the docker command runner (tests).
func withRunner(run func(args ...string) (string, error)) DockerOption {
	return func(l *DockerLauncher) { l.run = run }
}

// NewDockerLauncher returns an ExitLauncher that runs image.
func NewDockerLauncher(image string, opts ...DockerOption) (ExitLauncher, error) {
	l := &DockerLauncher{image: image, stateDir: defaultSecretsDir, run: dockerCLI}
	for _, o := range opts {
		o(l)
	}
	abs, err := filepath.Abs(l.stateDir)
	if err != nil {
		return nil, err
	}
	l.stateDir = abs
	if err := os.MkdirAll(l.stateDir, 0o700); err != nil {
		return nil, err
	}
	return l, nil
}

func dockerCLI(args ...string) (string, error) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	return string(out), err
}

// containerCreateArgs returns the labels and the openflux command line for
// an exit serving spec. The image's entrypoint is replaced by the binary.
func containerCreateArgs(spec ExitSpec, image string) (map[string]string, []string) {
	_ = image
	labels := map[string]string{labelManaged: "1", labelKeyID: spec.KeyID}
	cmd := []string{
		"--role=exit", "--mode=l4", "--transport=telemost",
		"--url=" + spec.CallLink,
		"--encryption-key-file=" + containerKeyPath,
		"--license-server=" + spec.LicenseWS,
		"--license-key-id=" + spec.KeyID,
	}
	return labels, cmd
}

func (l *DockerLauncher) secretPath(keyID string) string {
	return filepath.Join(l.stateDir, keyID+".key")
}

// Launch writes the secret file and starts the container.
func (l *DockerLauncher) Launch(spec ExitSpec) (ExitHandle, error) {
	keyPath := l.secretPath(spec.KeyID)
	if err := os.WriteFile(keyPath, []byte(spec.Secret), 0o600); err != nil {
		return ExitHandle{}, err
	}
	labels, cmd := containerCreateArgs(spec, l.image)
	args := []string{"run", "-d", "--rm", "--name", "openflux-exit-" + spec.KeyID}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}
	if l.network != "" {
		args = append(args, "--network", l.network)
	}
	args = append(args, "-v", keyPath+":"+containerKeyPath+":ro",
		"--entrypoint", containerBinary, l.image)
	args = append(args, cmd...)

	out, err := l.run(args...)
	if err != nil {
		_ = os.Remove(keyPath)
		return ExitHandle{}, fmt.Errorf("docker run: %v: %s", err, strings.TrimSpace(out))
	}
	id := strings.TrimSpace(out)
	if i := strings.LastIndexByte(id, '\n'); i >= 0 { // pull progress precedes the id
		id = id[i+1:]
	}
	return ExitHandle{ID: id, KeyID: spec.KeyID}, nil
}

// Stop removes the container and its secret file. A container that is
// already gone (it stopped itself and --rm removed it) is not an error.
func (l *DockerLauncher) Stop(h ExitHandle) error {
	out, err := l.run("rm", "-f", h.ID)
	if h.KeyID != "" {
		_ = os.Remove(l.secretPath(h.KeyID))
	}
	if err != nil && !strings.Contains(out, "No such container") {
		return fmt.Errorf("docker rm: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// List returns the running exit containers this launcher manages.
func (l *DockerLauncher) List() ([]ExitHandle, error) {
	out, err := l.run("ps", "--filter", "label="+labelManaged+"=1",
		"--format", `{{.ID}} {{.Label "`+labelKeyID+`"}}`)
	if err != nil {
		return nil, fmt.Errorf("docker ps: %v: %s", err, strings.TrimSpace(out))
	}
	var hs []ExitHandle
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		h := ExitHandle{ID: f[0]}
		if len(f) > 1 {
			h.KeyID = f[1]
		}
		hs = append(hs, h)
	}
	return hs, nil
}
