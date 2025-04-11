package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type RequestData struct {
	Image    string `json:"image"`
	Replicas string `json:"replicas"`
}

type MyPod struct {
	Port string
	Ip   string
}

func deploy(count uint32) {
	apiClient, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	// Define exposed port
	exposedPort, _ := nat.NewPort("tcp", "80")

	// Container config
	config := &container.Config{
		Image: "nginx:latest",
		ExposedPorts: nat.PortSet{
			exposedPort: struct{}{},
		},
	}

	hostConfig := &container.HostConfig{
		PortBindings: nat.PortMap{
			exposedPort: []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: "8080",
				},
			},
		},
	}

	// Platform: leave nil unless you want to target ARM/amd64 specifically
	var platform *ocispec.Platform = nil

	networkingConfig := &network.NetworkingConfig{}

	for idx := range count {
		containerName := fmt.Sprintf("nginx-0%c", idx)
		containerCreate, err := apiClient.ContainerCreate(
			context.Background(),
			config,
			hostConfig,
			networkingConfig,
			platform,
			containerName,
		)
	}

	if err != nil {
		panic(err)
	}

	// https: //github.com/docker/docker/blob/v28.0.4/client/container_create.go#L20
	fmt.Println("Container create:", containerCreate)

	err = apiClient.ContainerStart(context.Background(), containerCreate.ID, container.StartOptions{})

	if err != nil {
		panic(err)
	} else {
		fmt.Printf("Container with id: %s started ...", containerCreate.ID)
	}
}

func runIptablesRule(args ...string) error {
	cmd := exec.Command("iptables", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("iptables error: %v\n%s", err, output)
	}
	return nil
}

func loadBalance(containerIps []MyPod) {
	// Rule 1: 33% chance to IP1
	dest1 := fmt.Sprintf("%s:%s", containerIps[0].Port, containerIps[0].Ip)
	dest2 := fmt.Sprintf("%s:%s", containerIps[1].Port, containerIps[1].Ip)
	dest3 := fmt.Sprintf("%s:%s", containerIps[2].Port, containerIps[2].Ip)

	err := runIptablesRule(
		"-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", "80",
		"-m", "statistic", "--mode", "random", "--probability", "0.33",
		"-j", "DNAT", "--to-destination", dest1,
	)
	if err != nil {
		panic(err)
	}

	// Rule 2: 50% of remaining (≈33%) to IP2
	err = runIptablesRule(
		"-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", "80",
		"-m", "statistic", "--mode", "random", "--probability", "0.5",
		"-j", "DNAT", "--to-destination", dest2,
	)
	if err != nil {
		panic(err)
	}

	// Rule 3: fallback to IP3 (~34%)
	err = runIptablesRule(
		"-t", "nat", "-A", "PREROUTING",
		"-p", "tcp", "--dport", "80",
		"-j", "DNAT", "--to-destination", dest3,
	)
	if err != nil {
		panic(err)
	}
}

func deployHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data RequestData
	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Do something with the data
	fmt.Printf("Received: %+v\n", data)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": fmt.Sprintf("Hello, %s!", data.Name),
	})
}

func listContainersByLable(labelKey string, labelValue string) {
	filters := filters.NewArgs()
	searchPattern := fmt.Sprintf("%s=%s", labelKey, labelValue)
	filters.Add("label", searchPattern)

	apiClient, err := client.NewClientWithOpts(client.FromEnv)

	if err != nil {
		panic(err)
	}

	containers, err := apiClient.ContainerList(context.Background(), container.ListOptions{All: false, Filters: filters})

	if err != nil {
		panic(err)
	}

	for _, ctr := range containers {
		fmt.Println("Container:", ctr)
	}
}

func main() {
	http.HandleFunc("/deploy", deployHandler)
	http.ListenAndServe(":8100", nil)
}
