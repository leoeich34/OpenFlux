package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var testSpec = ExitSpec{
	KeyID:     "K1",
	CallLink:  "https://telemost.yandex.ru/j/1",
	Secret:    "secret-at-least-16-chars",
	LicenseWS: "ws://host.docker.internal:8098/exit/ws",
}

func TestDockerArgsForSpec(t *testing.T) {
	labels, cmd := containerCreateArgs(testSpec, "openflux:local")
	wantLabels := map[string]string{"openflux.managed": "1", "openflux.keyID": "K1"}
	if !reflect.DeepEqual(labels, wantLabels) {
		t.Errorf("labels = %v, want %v", labels, wantLabels)
	}
	wantCmd := []string{
		"--role=exit", "--mode=l4", "--transport=telemost",
		"--url=https://telemost.yandex.ru/j/1",
		"--encryption-key-file=/run/secrets/channel.key",
		"--license-server=ws://host.docker.internal:8098/exit/ws",
		"--license-key-id=K1",
	}
	if !reflect.DeepEqual(cmd, wantCmd) {
		t.Errorf("cmd = %v\nwant  %v", cmd, wantCmd)
	}
}

// recorder is an injected docker runner.
type recorder struct {
	calls [][]string
	out   map[string]string // first arg -> output
	err   map[string]error
}

func (r *recorder) run(args ...string) (string, error) {
	r.calls = append(r.calls, args)
	return r.out[args[0]], r.err[args[0]]
}

func newTestDocker(t *testing.T, r *recorder) (*DockerLauncher, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := NewDockerLauncher("openflux:local", WithStateDir(dir), withRunner(r.run))
	if err != nil {
		t.Fatalf("NewDockerLauncher: %v", err)
	}
	return l.(*DockerLauncher), dir
}

func TestDockerLaunchMountsSecretAndLabels(t *testing.T) {
	r := &recorder{out: map[string]string{"run": "abc123\n"}}
	l, dir := newTestDocker(t, r)
	h, err := l.Launch(testSpec)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if h.ID != "abc123" || h.KeyID != "K1" {
		t.Errorf("handle = %+v", h)
	}
	keyPath := filepath.Join(dir, "K1.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("secret file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secret file mode = %v, want 0600", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(keyPath); string(b) != testSpec.Secret {
		t.Errorf("secret file content mismatch")
	}
	argv := strings.Join(r.calls[0], " ")
	for _, want := range []string{
		"run -d --rm",
		"--label openflux.keyID=K1",
		"--label openflux.managed=1",
		"-v " + keyPath + ":/run/secrets/channel.key:ro",
		"--entrypoint /usr/local/bin/openflux openflux:local --role=exit",
		"--license-key-id=K1",
	} {
		if !strings.Contains(argv, want) {
			t.Errorf("docker argv missing %q:\n%s", want, argv)
		}
	}
}

func TestDockerLaunchFailureRemovesSecret(t *testing.T) {
	r := &recorder{err: map[string]error{"run": errors.New("no such image")}}
	l, dir := newTestDocker(t, r)
	if _, err := l.Launch(testSpec); err == nil {
		t.Fatalf("Launch should fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "K1.key")); !os.IsNotExist(err) {
		t.Errorf("secret file left behind after failed launch")
	}
}

func TestDockerStopRemovesContainerAndSecret(t *testing.T) {
	r := &recorder{out: map[string]string{"run": "abc123\n"}}
	l, dir := newTestDocker(t, r)
	h, _ := l.Launch(testSpec)
	if err := l.Stop(h); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	last := r.calls[len(r.calls)-1]
	if !reflect.DeepEqual(last, []string{"rm", "-f", "abc123"}) {
		t.Errorf("stop argv = %v", last)
	}
	if _, err := os.Stat(filepath.Join(dir, "K1.key")); !os.IsNotExist(err) {
		t.Errorf("secret file not removed on Stop")
	}
}

func TestDockerStopGoneContainerIsNotAnError(t *testing.T) {
	r := &recorder{
		out: map[string]string{"rm": "Error response from daemon: No such container: abc123"},
		err: map[string]error{"rm": errors.New("exit status 1")},
	}
	l, _ := newTestDocker(t, r)
	if err := l.Stop(ExitHandle{ID: "abc123", KeyID: "K1"}); err != nil {
		t.Errorf("Stop of an already-gone container: %v", err)
	}
}

func TestDockerListParsesManagedContainers(t *testing.T) {
	r := &recorder{out: map[string]string{"ps": "abc K1\ndef K2\n\n"}}
	l, _ := newTestDocker(t, r)
	got, err := l.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []ExitHandle{{ID: "abc", KeyID: "K1"}, {ID: "def", KeyID: "K2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v", got, want)
	}
	if argv := strings.Join(r.calls[0], " "); !strings.Contains(argv, "--filter label=openflux.managed=1") {
		t.Errorf("ps not filtered to managed containers: %s", argv)
	}
}
