//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	readyTimeout    = 90 * time.Second
	readyPollPeriod = 500 * time.Millisecond
)

type dockerCLI struct {
	t *testing.T
}

func (d dockerCLI) run(args ...string) string {
	cmd := exec.Command("docker", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		d.t.Fatalf("docker %s failed: %v\n%s", strings.Join(args, " "), err, errOut.String())
	}
	return strings.TrimSpace(out.String())
}

func (d dockerCLI) remove(name string) {
	_ = exec.Command("docker", "rm", "-f", name).Run()
}

func execCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

type Gateway struct {
	t          *testing.T
	docker     dockerCLI
	name       string
	proxyPort  int
	adminPort  int
	artifactID string
}

type GatewayOptions struct {
	Image     string
	ConfigDir string
	Config    string
	Env       []string
	RunArgs   []string
}

func startGateway(t *testing.T, opts GatewayOptions) *Gateway {
	t.Helper()

	d := dockerCLI{t: t}
	name := fmt.Sprintf("aidc-e2e-%d-%s", time.Now().UnixNano()%1e6, sanitize(t.Name()))
	d.remove(name)

	proxyPort := freePort(t)
	adminPort := freePort(t)

	args := []string{
		"run", "-d", "--name", name,
		"-v", opts.Config + ":/kong/declarative/kong.yaml:ro,Z",
		"-e", "KONG_DATABASE=off",
		"-e", "KONG_DECLARATIVE_CONFIG=/kong/declarative/kong.yaml",
		"-e", "KONG_PROXY_LISTEN=0.0.0.0:8000",
		"-e", "KONG_ADMIN_LISTEN=0.0.0.0:8001",
		"-e", "KONG_LOG_LEVEL=info",
		"--add-host", "host.docker.internal:host-gateway",
		"-p", fmt.Sprintf("%d:8000", proxyPort),
		"-p", fmt.Sprintf("%d:8001", adminPort),
	}
	for _, env := range opts.Env {
		args = append(args, "-e", env)
	}
	args = append(args, opts.RunArgs...)
	args = append(args, opts.Image)

	id := d.run(args...)
	t.Logf("gateway %s started (image %s, proxy :%d, admin :%d)", name, opts.Image, proxyPort, adminPort)

	g := &Gateway{
		t:          t,
		docker:     d,
		name:       name,
		proxyPort:  proxyPort,
		adminPort:  adminPort,
		artifactID: id,
	}
	t.Cleanup(g.stop)
	return g
}

func (g *Gateway) stop() {
	if g.t.Failed() {
		writeArtifact(g.t, "kong-logs.txt", g.logs(500))
	}
	g.docker.remove(g.name)
}

func (g *Gateway) ProxyURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", g.proxyPort)
}

func (g *Gateway) AdminURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", g.adminPort)
}

func (g *Gateway) waitReady() {
	g.t.Helper()
	deadline := time.Now().Add(readyTimeout)
	for {
		if status, _, _ := httpGet(g.AdminURL() + "/status"); status == 200 {
			g.t.Log("gateway admin API is ready")
			return
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("gateway did not become ready within %s\n%s", readyTimeout, g.logs(200))
		}
		time.Sleep(readyPollPeriod)
	}
}

func (g *Gateway) logs(tail int) string {
	out, err := exec.Command("docker", "logs", "--tail", fmt.Sprint(tail), g.name).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("(could not read container logs: %v)", err)
	}
	return string(out)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocating a free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}
