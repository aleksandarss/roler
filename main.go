package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Deployment struct {
	HostPort           string `json:"host_port"`
	ContainerPortSpace int    `json:"container_port_space"`
	Image              string `json:"image"`
	Replicas           int    `json:"replicas"`
	Name               string `json:"name"`
	ID                 int
}

type Container struct {
	ID           string
	Name         string
	Port         string
	DeploymentID int
	Ip           string
}

func deploy(deployment Deployment) {
	apiClient, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	exposedPort, _ := nat.NewPort("tcp", deployment.HostPort)

	// TODO: for arm64 this needs to be defined
	var platform *ocispec.Platform = nil

	networkingConfig := &network.NetworkingConfig{}

	var containerResponseSlice []container.CreateResponse

	config := &container.Config{
		Image: deployment.Image,
		ExposedPorts: nat.PortSet{
			exposedPort: struct{}{},
		},
		Labels: map[string]string{"deployment": deployment.Name},
	}

	var containers []Container

	for i := 0; i < deployment.Replicas; i++ {
		internalPort := strconv.Itoa(i + deployment.ContainerPortSpace)
		hostConfig := &container.HostConfig{
			PortBindings: nat.PortMap{
				exposedPort: []nat.PortBinding{
					{
						HostIP:   "0.0.0.0",
						HostPort: internalPort,
					},
				},
			},
		}

		containerName := fmt.Sprintf("nginx-0%d", i)
		containerCreate, err := apiClient.ContainerCreate(
			context.Background(),
			config,
			hostConfig,
			networkingConfig,
			platform,
			containerName,
		)

		if err != nil {
			panic(err)
		}

		fmt.Println("Container create:", containerCreate)

		containerResponseSlice = append(containerResponseSlice, containerCreate)

		containers = append(containers, Container{
			Name: containerName,
			Port: internalPort,
			ID:   containerCreate.ID,
		})
	}

	// https: //github.com/docker/docker/blob/v28.0.4/client/container_create.go#L20
	for i := range containers {
		err = apiClient.ContainerStart(context.Background(), containers[i].ID, container.StartOptions{})

		if err != nil {
			panic(err)
		}

		fmt.Printf("Container with id: %s started ...\n", containers[i].ID)

		containerInspec, _ := apiClient.ContainerInspect(context.Background(), containers[i].ID)

		containers[i].Ip = containerInspec.NetworkSettings.DefaultNetworkSettings.IPAddress

	}

	if deployment.Replicas > 1 {
		loadBalance(containers, deployment)
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

func loadBalance(containers []Container, deployment Deployment) {
	remaining := 1.0

	for i := 0; i < len(containers); i++ {
		containerAddress := fmt.Sprintf("%s:%s", containers[i].Ip, containers[i].Port)

		if i == len(containers)-1 {
			err := runIptablesRule(
				"-t", "nat", "-A", "PREROUTING",
				"-p", "tcp", "--dport", deployment.HostPort,
				"-j", "DNAT", "--to-destination", containerAddress,
			)

			if err != nil {
				panic(err)
			}

			break
		}

		prob := 1.0 / float64(len(containers)-i)
		probStr := fmt.Sprintf("%.6f", prob)
		err := runIptablesRule(
			"-t", "nat", "-A", "PREROUTING",
			"-p", "tcp", "--dport", deployment.HostPort,
			"-m", "statistic", "--mode", "random", "--probability", probStr,
			"-j", "DNAT", "--to-destination", containerAddress,
		)

		if err != nil {
			panic(err)
		}
		remaining -= prob

	}
}

func deployHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var data Deployment
	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	fmt.Printf("Received: %+v\n", data)

	deploy(data)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": fmt.Sprintf("Containers started, %s!", data.Name),
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
