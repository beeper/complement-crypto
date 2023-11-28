package deploy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	"github.com/matrix-org/complement"
	"github.com/matrix-org/complement/must"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type SlidingSyncDeployment struct {
	complement.Deployment
	ReverseProxyController *ReverseProxyController
	postgres               testcontainers.Container
	slidingSync            testcontainers.Container
	reverseProxy           testcontainers.Container
	slidingSyncURL         string
	tcpdump                *exec.Cmd
}

func (d *SlidingSyncDeployment) SlidingSyncURL(t *testing.T) string {
	t.Helper()
	if d.slidingSync == nil || d.slidingSyncURL == "" {
		t.Fatalf("SlidingSyncURL: not set")
		return ""
	}
	return d.slidingSyncURL
}

func (d *SlidingSyncDeployment) Teardown(writeLogs bool) {
	if d.slidingSync != nil {
		if writeLogs {
			err := writeContainerLogs(d.slidingSync, "container-sliding-sync.log")
			if err != nil {
				log.Printf("failed to write sliding sync logs: %s", err)
			}
		}
		if err := d.slidingSync.Terminate(context.Background()); err != nil {
			log.Fatalf("failed to stop sliding sync: %s", err)
		}
	}
	if d.postgres != nil {
		if err := d.postgres.Terminate(context.Background()); err != nil {
			log.Fatalf("failed to stop postgres: %s", err)
		}
	}
	if d.reverseProxy != nil {
		d.ReverseProxyController.Terminate()
		if err := d.reverseProxy.Terminate(context.Background()); err != nil {
			log.Fatalf("failed to stop reverse proxy: %s", err)
		}
	}
	if d.tcpdump != nil {
		fmt.Println("Sent SIGINT to tcpdump, waiting for it to exit, err=", d.tcpdump.Process.Signal(os.Interrupt))
		fmt.Println("tcpdump finished, err=", d.tcpdump.Wait())
	}
}

func RunNewDeployment(t *testing.T, shouldTCPDump bool) *SlidingSyncDeployment {
	// allow 30s for everything to deploy
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Deploy the homeserver using Complement
	deployment := complement.Deploy(t, 2)
	networkName := deployment.Network()

	controller := NewReverseProxyController()
	controllerPort, err := controller.Listen()
	if err != nil {
		t.Fatalf("reverse proxy controller failed to listen: %v", err)
	}

	// make a reverse proxy.
	hs1ExposedPort := "3000/tcp"
	hs2ExposedPort := "3001/tcp"
	rpContainer, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/matrix-org/complement-crypto-reverse-proxy:latest",
			ExposedPorts: []string{hs1ExposedPort, hs2ExposedPort},
			Env: map[string]string{
				"REVERSE_PROXY_CONTROLLER_URL": fmt.Sprintf("http://host.docker.internal:", controllerPort),
				"REVERSE_PROXY_HOSTS":          "http://hs1,3000;http://hs2,3001",
			},
			WaitingFor: wait.ForLog("listening"),
			Networks:   []string{networkName},
			NetworkAliases: map[string][]string{
				networkName: {"reverseproxy"},
			},
		},
		Started: true,
	})
	must.NotError(t, "failed to start reverse proxy container", err)
	rpHS1URL := externalURL(t, rpContainer, hs1ExposedPort)
	rpHS2URL := externalURL(t, rpContainer, hs2ExposedPort)

	// Make a postgres container
	postgresContainer, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:13-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     "postgres",
				"POSTGRES_PASSWORD": "postgres",
				"POSTGRES_DB":       "syncv3",
			},
			WaitingFor: wait.ForExec([]string{"pg_isready"}).WithExitCodeMatcher(func(exitCode int) bool {
				fmt.Println("pg_isready exit code", exitCode)
				return exitCode == 0
			}).WithPollInterval(time.Second),
			Networks: []string{networkName},
			NetworkAliases: map[string][]string{
				networkName: {"postgres"},
			},
		},
		Started: true,
	})
	must.NotError(t, "failed to start postgres container", err)

	// Make a sliding sync proxy
	ssExposedPort := "6789/tcp"
	ssContainer, err := testcontainers.GenericContainer(ctx,
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "ghcr.io/matrix-org/sliding-sync:v0.99.12",
				ExposedPorts: []string{ssExposedPort},
				Env: map[string]string{
					"SYNCV3_SECRET":   "secret",
					"SYNCV3_BINDADDR": ":6789",
					"SYNCV3_SERVER":   "http://hs1:8008",
					"SYNCV3_DB":       "user=postgres dbname=syncv3 sslmode=disable password=postgres host=postgres",
				},
				WaitingFor: wait.ForLog("listening on"),
				Networks:   []string{networkName},
				NetworkAliases: map[string][]string{
					networkName: {"ssproxy"},
				},
			},
			Started: true,
		})
	must.NotError(t, "failed to start sliding sync container", err)

	ssURL := externalURL(t, ssContainer, ssExposedPort)
	csapi1 := deployment.UnauthenticatedClient(t, "hs1")
	csapi2 := deployment.UnauthenticatedClient(t, "hs2")

	// log for debugging purposes
	t.Logf("SlidingSyncDeployment created (network=%s):", networkName)
	t.Logf("  NAME          INT          EXT")
	t.Logf("  sliding sync: ssproxy      %s", ssURL)
	t.Logf("  synapse:      hs1          %s", csapi1.BaseURL)
	t.Logf("  synapse:      hs2          %s", csapi2.BaseURL)
	t.Logf("  postgres:     postgres")
	t.Logf("  reverseproxy: reverseproxy hs1=%s hs2=%s", rpHS1URL, rpHS2URL)
	var cmd *exec.Cmd
	if shouldTCPDump {
		t.Log("Running tcpdump...")
		su, _ := url.Parse(ssURL)
		cu1, _ := url.Parse(csapi1.BaseURL)
		cu2, _ := url.Parse(csapi2.BaseURL)
		filter := fmt.Sprintf("tcp port %s or port %s or port %s", su.Port(), cu1.Port(), cu2.Port())
		cmd = exec.Command("tcpdump", "-i", "any", "-s", "0", filter, "-w", "test.pcap")
		t.Log(cmd.String())
		if err := cmd.Start(); err != nil {
			t.Fatalf("tcpdump failed: %v", err)
		}
		// TODO needs sudo
		t.Logf("Started tcpdumping: PID %d", cmd.Process.Pid)
	}
	return &SlidingSyncDeployment{
		Deployment:             deployment,
		ReverseProxyController: controller,
		slidingSync:            ssContainer,
		postgres:               postgresContainer,
		reverseProxy:           rpContainer,
		slidingSyncURL:         ssURL,
		tcpdump:                cmd,
	}
}

func externalURL(t *testing.T, c testcontainers.Container, exposedPort string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	host, err := c.Host(ctx)
	must.NotError(t, "failed to get host", err)
	mappedPort, err := c.MappedPort(ctx, nat.Port(exposedPort))
	must.NotError(t, "failed to get mapped port", err)
	return fmt.Sprintf("http://%s:%s", host, mappedPort.Port())
}

func writeContainerLogs(container testcontainers.Container, filename string) error {
	w, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("os.Create: %s", err)
	}
	reader, err := container.Logs(context.Background())
	if err != nil {
		return fmt.Errorf("container.Logs: %s", err)
	}
	_, err = io.Copy(w, reader)
	if err != nil {
		return fmt.Errorf("io.Copy: %s", err)
	}
	return nil
}
